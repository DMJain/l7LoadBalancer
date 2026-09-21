package health

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// newTestRegistry builds a real *backend.Registry (every backend healthy, per
// NewRegistry) whose backends point at the given URLs. Names are synthesised
// because the health checker cares only about URL and health state.
func newTestRegistry(t *testing.T, urls ...string) *backend.Registry {
	t.Helper()
	cfgs := make([]config.BackendConfig, 0, len(urls))
	for i, u := range urls {
		cfgs = append(cfgs, config.BackendConfig{Name: fmt.Sprintf("backend-%d", i), URL: u})
	}
	reg, err := backend.NewRegistry(cfgs)
	require.NoError(t, err)
	return reg
}

// statusServer returns an httptest.Server that answers every request with code.
func statusServer(t *testing.T, code int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestProbeClassifiesResponses pins the "2xx is success, everything else is
// failure" rule at its boundary: 2xx codes pass, and every non-2xx class —
// including 3xx — fails.
func TestProbeClassifiesResponses(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   bool
	}{
		{"200 ok is success", http.StatusOK, true},
		{"204 no content is success", http.StatusNoContent, true},
		{"299 is success", 299, true},
		{"300 multiple choices is failure", http.StatusMultipleChoices, false},
		{"404 not found is failure", http.StatusNotFound, false},
		{"500 internal server error is failure", http.StatusInternalServerError, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := newTestRegistry(t, statusServer(t, tc.status).URL)
			c := New(reg, time.Second, time.Second, discardLogger(), metrics.NewCollector())

			assert.Equal(t, tc.want, c.probe(context.Background(), reg.All()[0]))
		})
	}
}

// TestProbeTreatsRedirectAsFailure proves the checker observes the redirect
// rather than following it. The redirect target answers 200, so a client with
// default redirect-following would report success — the checker must not.
func TestProbeTreatsRedirectAsFailure(t *testing.T) {
	dest := statusServer(t, http.StatusOK)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)

	reg := newTestRegistry(t, redirect.URL)
	c := New(reg, time.Second, time.Second, discardLogger(), metrics.NewCollector())

	assert.False(t, c.probe(context.Background(), reg.All()[0]),
		"a redirecting backend must read as unhealthy even though its redirect target answers 2xx")
}

// TestProbeConnectionRefusedIsFailure covers the transport-failure path: no
// HTTP status is produced at all.
func TestProbeConnectionRefusedIsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	url := srv.URL
	srv.Close() // now nothing is listening on url

	reg := newTestRegistry(t, url)
	c := New(reg, time.Second, time.Second, discardLogger(), metrics.NewCollector())

	assert.False(t, c.probe(context.Background(), reg.All()[0]))
}

// TestProberMarksUnhealthyAfterConsecutiveFailures drives the state machine
// through failing fixtures: the backend must survive N-1 failures and flip
// unhealthy on the Nth.
func TestProberMarksUnhealthyAfterConsecutiveFailures(t *testing.T) {
	fixtures := []struct {
		name string
		url  func(t *testing.T) string
	}{
		{
			name: "5xx responses",
			url:  func(t *testing.T) string { return statusServer(t, http.StatusInternalServerError).URL },
		},
		{
			name: "connection refused",
			url: func(t *testing.T) string {
				srv := statusServer(t, http.StatusOK)
				url := srv.URL
				srv.Close()
				return url
			},
		},
	}

	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			reg := newTestRegistry(t, fx.url(t))
			c := New(reg, time.Second, time.Second, discardLogger(), metrics.NewCollector())
			b := reg.All()[0]
			p := c.newProber(b)
			require.True(t, b.IsHealthy(), "NewRegistry starts every backend healthy")

			for i := 0; i < probeFailuresBeforeUnhealthy-1; i++ {
				assert.False(t, p.probeOnce(context.Background()), "failure %d below threshold", i+1)
			}
			require.True(t, b.IsHealthy(),
				"fewer than %d consecutive failures must leave the backend healthy", probeFailuresBeforeUnhealthy)

			assert.False(t, p.probeOnce(context.Background()))
			assert.False(t, b.IsHealthy(),
				"%d consecutive failures must mark the backend unhealthy", probeFailuresBeforeUnhealthy)
		})
	}
}

// TestProberMarksHealthyAfterConsecutiveSuccesses drives recovery: an
// unhealthy backend becomes healthy only once M consecutive probes succeed.
func TestProberMarksHealthyAfterConsecutiveSuccesses(t *testing.T) {
	reg := newTestRegistry(t, statusServer(t, http.StatusOK).URL)
	c := New(reg, time.Second, time.Second, discardLogger(), metrics.NewCollector())
	b := reg.All()[0]
	p := c.newProber(b)
	b.MarkUnhealthy()

	for i := 0; i < probeSuccessesBeforeHealthy-1; i++ {
		assert.True(t, p.probeOnce(context.Background()), "success %d below threshold", i+1)
	}
	require.False(t, b.IsHealthy(),
		"fewer than %d consecutive successes must leave the backend unhealthy", probeSuccessesBeforeHealthy)

	assert.True(t, p.probeOnce(context.Background()))
	assert.True(t, b.IsHealthy(),
		"%d consecutive successes must mark the backend healthy", probeSuccessesBeforeHealthy)
}

// TestProberReinstatesPassivelyEjectedBackendWithAccumulatorPastThreshold covers
// the S3.T6.5 drift. Passive outlier detection ejects an "up but erroring"
// backend while active probes keep succeeding in the background, so the
// prober's consecutive-successes accumulator is already past M at ejection
// time. The reinstatement gate must compare with >= against M, not ==: with ==
// the separate >= block still calls MarkHealthy (so IsHealthy reads true) but
// the guarded health_reinstated log line and lb_backend_healthy write, which
// live behind the equality, never fire. The log-count assertion is the
// mechanical verification of the fix — it fails under == and passes under >=.
func TestProberReinstatesPassivelyEjectedBackendWithAccumulatorPastThreshold(t *testing.T) {
	l, dump := captureLogger(t)
	reg := newTestRegistry(t, statusServer(t, http.StatusOK).URL)
	c := New(reg, time.Second, time.Second, l, metrics.NewCollector())
	b := reg.All()[0]
	p := c.newProber(b)

	// Background probing runs the accumulator past M while the backend is still
	// healthy. The genuine-state guard must suppress any reinstatement line here.
	for i := 0; i < probeSuccessesBeforeHealthy+2; i++ {
		require.True(t, p.probeOnce(context.Background()))
	}
	require.Empty(t, recordsWithEvent(dump(), logger.EventHealthReinstated),
		"an always-healthy backend emits no reinstatement line")
	require.Greater(t, p.successes, probeSuccessesBeforeHealthy,
		"precondition: the accumulator must already be past M at ejection time")

	// Passive outlier detection ejects it mid-success-streak.
	b.MarkUnhealthy()
	require.False(t, b.IsHealthy())

	// M more successful probes must reinstate it. Under the == gate the
	// accumulator is already past M, so neither the log line nor the gauge fires.
	for i := 0; i < probeSuccessesBeforeHealthy; i++ {
		require.True(t, p.probeOnce(context.Background()))
	}

	assert.True(t, b.IsHealthy(), "a passively-ejected backend must recover via active probes")
	reinstated := recordsWithEvent(dump(), logger.EventHealthReinstated)
	require.Len(t, reinstated, 1, "exactly one reinstatement line per genuine transition")
	assert.Equal(t, logger.ReasonProbeRecovered, reinstated[0].str("reason"))
	assert.Equal(t, b.Name, reinstated[0].str("backend"))
}

// TestProberConsecutiveCountersReset proves the counters are consecutive, not
// cumulative: a single success wipes failure progress (and vice versa), so an
// intermittent backend neither ejects nor recovers prematurely.
func TestProberConsecutiveCountersReset(t *testing.T) {
	var mu sync.Mutex
	status := http.StatusInternalServerError
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		s := status
		mu.Unlock()
		w.WriteHeader(s)
	}))
	t.Cleanup(srv.Close)

	setStatus := func(s int) {
		mu.Lock()
		status = s
		mu.Unlock()
	}

	reg := newTestRegistry(t, srv.URL)
	c := New(reg, time.Second, time.Second, discardLogger(), metrics.NewCollector())
	b := reg.All()[0]
	p := c.newProber(b)

	// N-1 failures, then one success, resets failure progress.
	for i := 0; i < probeFailuresBeforeUnhealthy-1; i++ {
		p.probeOnce(context.Background())
	}
	setStatus(http.StatusOK)
	require.True(t, p.probeOnce(context.Background()))

	// N-1 more failures still must not eject, because the success reset the count.
	setStatus(http.StatusInternalServerError)
	for i := 0; i < probeFailuresBeforeUnhealthy-1; i++ {
		p.probeOnce(context.Background())
	}
	assert.True(t, b.IsHealthy(), "an intervening success must reset the consecutive-failure count")

	// One more failure now reaches the threshold.
	p.probeOnce(context.Background())
	assert.False(t, b.IsHealthy())
}

// TestCheckerUsesDedicatedTransport proves the probe client does not resolve
// to the shared http.DefaultTransport, which httputil.ReverseProxy also falls
// back to. Without its own transport, probe tuning would silently couple to
// live-request transport settings (ADR-0011 decision 11).
func TestCheckerUsesDedicatedTransport(t *testing.T) {
	reg := newTestRegistry(t, statusServer(t, http.StatusOK).URL)
	c := New(reg, time.Second, time.Second, discardLogger(), metrics.NewCollector())

	require.NotNil(t, c.client.Transport)
	assert.NotSame(t, http.DefaultTransport, c.client.Transport)
}

// TestProbeHonorsItsOwnClientTimeout proves the checker's dedicated client
// timeout bounds a probe, independent of internal/proxy's transport.
func TestProbeHonorsItsOwnClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	reg := newTestRegistry(t, srv.URL)

	short := New(reg, time.Second, 20*time.Millisecond, discardLogger(), metrics.NewCollector())
	assert.False(t, short.probe(context.Background(), reg.All()[0]),
		"the checker's own client timeout must bound the probe")

	generous := New(reg, time.Second, time.Second, discardLogger(), metrics.NewCollector())
	assert.True(t, generous.probe(context.Background(), reg.All()[0]),
		"with an adequate timeout the same backend probes healthy")
}

// TestProbeMalformedURLIsFailure covers the defensive request-build path: a
// backend whose URL cannot form a request fails the probe rather than
// panicking. config.Validate rejects such URLs, so this is reachable only if
// that guarantee is ever weakened.
func TestProbeMalformedURLIsFailure(t *testing.T) {
	reg := newTestRegistry(t, statusServer(t, http.StatusOK).URL)
	c := New(reg, time.Second, time.Second, discardLogger(), metrics.NewCollector())
	b := reg.All()[0]
	b.URL = &url.URL{Scheme: "http", Host: "exa mple.com"}

	assert.False(t, c.probe(context.Background(), b))
}

// TestStartProbesUntilContextCancelled covers the goroutine wiring: Start
// launches one probe loop per backend, and cancellation stops them.
func TestStartProbesUntilContextCancelled(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	reg := newTestRegistry(t, srv.URL, srv.URL)
	c := New(reg, 5*time.Millisecond, time.Second, discardLogger(), metrics.NewCollector())

	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx)

	require.Eventually(t, func() bool { return hits.Load() >= 2 }, time.Second, time.Millisecond,
		"Start must probe every backend on its interval")

	cancel()
	time.Sleep(20 * time.Millisecond) // let any in-flight probe settle
	after := hits.Load()
	time.Sleep(30 * time.Millisecond)

	assert.Equal(t, after, hits.Load(), "no probes may run after ctx is cancelled")
}
