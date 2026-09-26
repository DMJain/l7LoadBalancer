package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/balancer"
	"github.com/DMJain/l7LoadBalancer/internal/circuit"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/health"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// TestMain silences the process default logger. Proxy reads slog.Default() at
// construction (ADR-0007), so tests that assert on logs swap the default in
// for the duration of one test.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// backendEntry pairs a backend's configured identity with the URL it should
// point at — usually a live httptest.Server.URL.
type backendEntry struct {
	name string
	url  string
}

// registryFrom builds a real backend.Registry from entries, preserving order
// (which LeastConnections' tie-break and RoundRobin's rotation depend on).
func registryFrom(t *testing.T, entries ...backendEntry) *backend.Registry {
	t.Helper()
	cfgs := make([]config.BackendConfig, len(entries))
	for i, e := range entries {
		cfgs[i] = config.BackendConfig{Name: e.name, URL: e.url}
	}
	reg, err := backend.NewRegistry(cfgs)
	require.NoError(t, err)
	return reg
}

// startBackend starts an httptest.Server that identifies itself in the
// response body, so a test can observe which backend served a request.
func startBackend(t *testing.T, name string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, name)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// failingBackend starts an httptest.Server that answers every request with 500,
// the modifyResponse failure signal shared by the fan-out and passive-outlier
// tests.
func failingBackend(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// deadBackendURL returns a URL that accepts TCP connections and immediately
// closes them, so a round trip fails deterministically with a transport error
// rather than depending on an unbound fixed port.
func deadBackendURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return "http://" + l.Addr().String()
}

// useLogger swaps the process default logger for the duration of one test, so
// a Proxy constructed afterwards picks it up via slog.Default().
func useLogger(t *testing.T, logger *slog.Logger) {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })
}

func TestProxyDistributionMatchesSelector(t *testing.T) {
	names := []string{"backend-a", "backend-b", "backend-c"}
	servers := make(map[string]*httptest.Server, len(names))
	for _, name := range names {
		servers[name] = startBackend(t, name)
	}

	reg := registryFrom(t,
		backendEntry{"backend-a", servers["backend-a"].URL},
		backendEntry{"backend-b", servers["backend-b"].URL},
		backendEntry{"backend-c", servers["backend-c"].URL},
	)
	front := httptest.NewServer(New(reg, balancer.NewRoundRobin(reg)))
	t.Cleanup(front.Close)

	got := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		resp, err := front.Client().Get(front.URL)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode)
		got = append(got, string(body))
	}

	assert.Equal(t, []string{
		"backend-a", "backend-b", "backend-c",
		"backend-a", "backend-b", "backend-c",
	}, got)
}

func TestProxyNoHealthyBackendReturns503(t *testing.T) {
	tests := []struct {
		name string
		reg  func(t *testing.T) *backend.Registry
	}{
		{
			name: "registry with no backends",
			reg:  func(t *testing.T) *backend.Registry { return registryFrom(t) },
		},
		{
			name: "all backends unhealthy",
			reg: func(t *testing.T) *backend.Registry {
				reg := registryFrom(t, backendEntry{"backend-a", "http://127.0.0.1:1"})
				reg.All()[0].MarkUnhealthy()
				return reg
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := tt.reg(t)
			p := New(reg, balancer.NewRoundRobin(reg))

			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		})
	}
}

func TestProxySelectorErrorReturns502(t *testing.T) {
	reg := registryFrom(t, backendEntry{"backend-a", deadBackendURL(t)})
	p := New(reg, errorSelector{err: errors.New("selector exploded")})

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestProxyBackendFailureReturns502AndReleases(t *testing.T) {
	reg := registryFrom(t, backendEntry{"backend-a", deadBackendURL(t)})
	p := New(reg, balancer.NewRoundRobin(reg))

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Equal(t, int64(0), reg.All()[0].ActiveConns(),
		"a failed round trip must still release the active-connection slot")
}

func TestProxyActiveConnsReturnToZeroAfterConcurrentRequests(t *testing.T) {
	const requests = 100

	var inFlight atomic.Int64
	release := make(chan struct{})
	backendSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlight.Add(1)
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(backendSrv.Close)

	reg := registryFrom(t, backendEntry{"backend-a", backendSrv.URL})
	front := httptest.NewServer(New(reg, balancer.NewLeastConnections(reg)))
	t.Cleanup(front.Close)

	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := front.Client().Get(front.URL)
			if err != nil {
				t.Errorf("request failed: %v", err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()
	}

	require.Eventually(t, func() bool {
		return inFlight.Load() == int64(requests)
	}, 5*time.Second, 10*time.Millisecond, "all requests should reach the backend and block")

	assert.Equal(t, int64(requests), reg.All()[0].ActiveConns(),
		"all in-flight requests should be counted")

	close(release)
	wg.Wait()

	require.Eventually(t, func() bool {
		return reg.All()[0].ActiveConns() == 0
	}, 5*time.Second, 10*time.Millisecond, "ActiveConns should drain back to zero")
}

// errorSelector is a Selector stub for exercising the proxy's non-sentinel
// error branch. It returns an error that is not ErrNoHealthyBackends.
type errorSelector struct {
	err error
}

func (s errorSelector) Select(context.Context, *http.Request) (*backend.Backend, error) {
	return nil, s.err
}

// TestProxyRecordsRoundTripLatencyOnSuccess proves the proxy records a real,
// non-zero, backend-round-trip-scoped latency on the backend that served the
// request — and only on that backend — even though the configured selector
// (RoundRobin) never reads it.
func TestProxyRecordsRoundTripLatencyOnSuccess(t *testing.T) {
	serving := startBackend(t, "backend-a")
	reg := registryFrom(t,
		backendEntry{"backend-a", serving.URL},
		backendEntry{"backend-b", "http://127.0.0.1:1"},
	)
	reg.All()[1].MarkUnhealthy()

	p := New(reg, balancer.NewRoundRobin(reg))
	p.RegisterObserver(NewLatencyObserver())

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Positive(t, reg.All()[0].EWMALatency(),
		"the backend that served the request must record a real round-trip latency")
	assert.Zero(t, reg.All()[1].EWMALatency(),
		"a backend that did not serve the request must not record a latency")
}

// TestProxyRecordsPenaltyOnBackendFailure proves the failure path records the
// fixed penalty rather than the real (much shorter) elapsed time-to-failure.
func TestProxyRecordsPenaltyOnBackendFailure(t *testing.T) {
	reg := registryFrom(t, backendEntry{"backend-a", deadBackendURL(t)})
	p := New(reg, balancer.NewRoundRobin(reg))
	p.RegisterObserver(NewLatencyObserver())

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusBadGateway, rec.Code)

	assert.Equal(t, p2cFailurePenalty, reg.All()[0].EWMALatency(),
		"a failed round trip must record the fixed penalty, not the real elapsed time")
}

// observerCall records one ObserveRoundTrip invocation.
type observerCall struct {
	backend *backend.Backend
	d       time.Duration
	success bool
}

// spyObserver records every round trip it is notified of and, when inner is
// non-nil, delegates to it after recording. The delegation is what lets a test
// assert an exact invocation count for an observer (the latency adapter) whose
// own side effect — an EWMA write — cannot itself be counted.
type spyObserver struct {
	inner RoundTripObserver

	mu    sync.Mutex
	calls []observerCall
}

func (s *spyObserver) ObserveRoundTrip(b *backend.Backend, d time.Duration, success bool) {
	s.mu.Lock()
	s.calls = append(s.calls, observerCall{backend: b, d: d, success: success})
	s.mu.Unlock()
	if s.inner != nil {
		s.inner.ObserveRoundTrip(b, d, success)
	}
}

func (s *spyObserver) snapshot() []observerCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]observerCall(nil), s.calls...)
}

// TestProxyObserverFanOutInvokesEachObserverExactlyOnce proves the fan-out
// notifies every registered observer exactly once per request on every
// terminal hook — a successful 2xx response and a 5xx response (both via
// modifyResponse, differing in the success flag) and a connection-refused
// backend (via errorHandler). It asserts invocation count, not merely content,
// so an accidental double registration cannot pass silently. The latency
// adapter is nested in a recording spy so its own invocation count (which its
// EWMA-write side effect cannot reveal) is assertable too.
func TestProxyObserverFanOutInvokesEachObserverExactlyOnce(t *testing.T) {
	tests := []struct {
		name        string
		backendURL  func(t *testing.T) string
		wantStatus  int
		wantSuccess bool
		// wantPenalty picks the duration expectation: the fixed failure penalty
		// exactly (errorHandler), or any real positive round-trip duration.
		wantPenalty bool
	}{
		{
			name:        "successful 2xx response",
			backendURL:  func(t *testing.T) string { return startBackend(t, "backend-a").URL },
			wantStatus:  http.StatusOK,
			wantSuccess: true,
		},
		{
			name: "5xx response",
			backendURL: func(t *testing.T) string {
				return failingBackend(t).URL
			},
			wantStatus:  http.StatusInternalServerError,
			wantSuccess: false,
		},
		{
			name:        "connection refused",
			backendURL:  deadBackendURL,
			wantStatus:  http.StatusBadGateway,
			wantSuccess: false,
			wantPenalty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := registryFrom(t, backendEntry{"backend-a", tt.backendURL(t)})
			p := New(reg, balancer.NewRoundRobin(reg))

			latency := &spyObserver{inner: NewLatencyObserver()}
			spy := &spyObserver{}
			p.RegisterObserver(latency)
			p.RegisterObserver(spy)

			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			require.Equal(t, tt.wantStatus, rec.Code)

			calls := spy.snapshot()
			require.Len(t, calls, 1, "each observer must be invoked exactly once per request")
			assert.Equal(t, reg.All()[0], calls[0].backend)
			assert.Equal(t, tt.wantSuccess, calls[0].success)

			assert.Len(t, latency.snapshot(), 1, "the latency adapter must be invoked exactly once")

			if tt.wantPenalty {
				assert.Equal(t, p2cFailurePenalty, calls[0].d)
				assert.Equal(t, p2cFailurePenalty, reg.All()[0].EWMALatency(),
					"the latency adapter must record the fixed failure penalty")
			} else {
				assert.Positive(t, calls[0].d)
				assert.Positive(t, reg.All()[0].EWMALatency(),
					"the latency adapter must record the round-trip duration")
			}
		})
	}
}

// logRecord is one parsed JSON slog line.
type logRecord map[string]any

func (r logRecord) msg() string {
	s, _ := r["msg"].(string)
	return s
}

func (r logRecord) level() string {
	s, _ := r["level"].(string)
	return s
}

// lockedBuffer is a concurrency-safe bytes.Buffer. slog's JSON handler does
// not serialize writes, and the drain tests log from a real server goroutine
// while the test goroutine reads the buffer, so the shared capture buffer must
// guard both sides.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLogger returns a logger writing JSON to an in-memory buffer, plus a
// function that parses every line emitted so far.
func captureLogger(t *testing.T) (*slog.Logger, func() []logRecord) {
	t.Helper()
	buf := &lockedBuffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))

	dump := func() []logRecord {
		var out []logRecord
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			rec := logRecord{}
			require.NoError(t, json.Unmarshal([]byte(line), &rec))
			out = append(out, rec)
		}
		return out
	}
	return logger, dump
}

// recordsWithField returns every record whose field key equals want.
func recordsWithField(recs []logRecord, key, want string) []logRecord {
	var out []logRecord
	for _, r := range recs {
		if r[key] == want {
			out = append(out, r)
		}
	}
	return out
}

func recordsWithMsg(recs []logRecord, msg string) []logRecord {
	return recordsWithField(recs, "msg", msg)
}

// recordsWithReason returns every record carrying exactly this reason, used to
// pin which tier a failed round trip was classified into.
func recordsWithReason(recs []logRecord, reason string) []logRecord {
	return recordsWithField(recs, "reason", reason)
}

func TestProxyLogsRequestCompleteOnSuccess(t *testing.T) {
	logger, dump := captureLogger(t)
	useLogger(t, logger)

	backendSrv := startBackend(t, "backend-a")
	reg := registryFrom(t, backendEntry{"backend-a", backendSrv.URL})
	p := New(reg, balancer.NewRoundRobin(reg))

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	completes := recordsWithMsg(dump(), "request complete")
	require.Len(t, completes, 1)
	got := completes[0]

	assert.Equal(t, "INFO", got.level())
	assert.Equal(t, "backend-a", got["backend"])
	assert.Equal(t, "GET", got["method"])
	assert.Equal(t, float64(http.StatusOK), got["status"])
	assert.Equal(t, "/ok", got["path"])
	assert.NotEmpty(t, got["remote_addr"])
	assert.Contains(t, got, "latency_ms")
}

func TestProxyLogsRequestCompleteOn503(t *testing.T) {
	logger, dump := captureLogger(t)
	useLogger(t, logger)

	reg := registryFrom(t, backendEntry{"backend-a", "http://127.0.0.1:1"})
	reg.All()[0].MarkUnhealthy()
	p := New(reg, balancer.NewRoundRobin(reg))

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/none", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	completes := recordsWithMsg(dump(), "request complete")
	require.Len(t, completes, 1)

	assert.Equal(t, "WARN", completes[0].level())
	assert.Equal(t, float64(http.StatusServiceUnavailable), completes[0]["status"])
	assert.Equal(t, "GET", completes[0]["method"])
	assert.Equal(t, "/none", completes[0]["path"])
}

// TestProxyFanOutFeedsPassiveOutlierDetectorAlongsideLatencyObserver proves the
// detector receives outcomes through the real proxy fan-out when registered
// next to other observers, and that a persistently failing backend is ejected
// without the test touching the detector's state directly. A spy sits alongside
// the real latency observer and the detector: the spy receiving exactly one
// call per request shows all three were fed the same request path, so the
// detector's ejection is genuinely attributable to the fan-out (the generic
// per-observer exactly-once guarantee itself is ticket 02's test, not this one).
func TestProxyFanOutFeedsPassiveOutlierDetectorAlongsideLatencyObserver(t *testing.T) {
	failing := failingBackend(t)

	reg := registryFrom(t, backendEntry{"backend-a", failing.URL})
	p := New(reg, balancer.NewRoundRobin(reg))
	latency := &spyObserver{inner: NewLatencyObserver()}
	spy := &spyObserver{}
	p.RegisterObserver(latency)
	p.RegisterObserver(spy)
	p.RegisterObserver(health.NewOutlierDetector(slog.Default(), metrics.NewCollector()))

	front := httptest.NewServer(p)
	t.Cleanup(front.Close)

	b := reg.All()[0]
	require.True(t, b.IsHealthy(), "NewRegistry starts every backend healthy")

	// Every 5xx is one passive failure. Once the detector ejects the backend,
	// selection returns ErrNoHealthyBackends and the loop stops early (503).
	requests := 0
	for i := 0; i < 50 && b.IsHealthy(); i++ {
		resp, err := front.Client().Get(front.URL)
		require.NoError(t, err)
		_ = resp.Body.Close()
		requests++
	}

	require.False(t, b.IsHealthy(),
		"the passive outlier detector must eject a backend failing through the real proxy fan-out")
	require.Positive(t, requests)

	// The other observers saw the same requests that drove the ejection.
	assert.Len(t, spy.snapshot(), requests, "the spy must receive every request's outcome")
	assert.Len(t, latency.snapshot(), requests, "the latency observer must receive every request's outcome")
	assert.Positive(t, b.EWMALatency())
}

// TestProxyFanOutFeedsDetectorMixedResponseAndTransportFailures proves the
// failure signal is the union the ticket specifies: a backend that alternately
// returns 500 (the modifyResponse path) and aborts the connection with no
// response at all (the errorHandler transport-failure path) is ejected once the
// mixed failures reach the threshold, not only when one kind repeats.
func TestProxyFanOutFeedsDetectorMixedResponseAndTransportFailures(t *testing.T) {
	var seen, serverErrors atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen.Add(1)%2 == 0 {
			serverErrors.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			serverErrors.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		_ = conn.Close() // transport failure: not even a response line
	}))
	t.Cleanup(srv.Close)

	reg := registryFrom(t, backendEntry{"backend-a", srv.URL})
	p := New(reg, balancer.NewRoundRobin(reg))
	p.RegisterObserver(health.NewOutlierDetector(slog.Default(), metrics.NewCollector()))

	front := httptest.NewServer(p)
	t.Cleanup(front.Close)

	b := reg.All()[0]
	require.True(t, b.IsHealthy())

	for i := 0; i < 50 && b.IsHealthy(); i++ {
		resp, err := front.Client().Get(front.URL)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}

	require.False(t, b.IsHealthy(),
		"mixed 5xx and transport failures must eject the backend")
	assert.Positive(t, serverErrors.Load(),
		"the run must have exercised the modifyResponse (5xx) path")
	assert.Greater(t, seen.Load(), serverErrors.Load(),
		"the run must have exercised the errorHandler (connection-abort) path")
}

func TestProxyLogsRequestCompleteOn502(t *testing.T) {
	logger, dump := captureLogger(t)
	useLogger(t, logger)

	reg := registryFrom(t, backendEntry{"backend-a", deadBackendURL(t)})
	p := New(reg, balancer.NewRoundRobin(reg))

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dead", nil))
	require.Equal(t, http.StatusBadGateway, rec.Code)

	recs := dump()
	completes := recordsWithMsg(recs, "request complete")
	require.Len(t, completes, 1)

	assert.Equal(t, "WARN", completes[0].level())
	assert.Equal(t, "backend-a", completes[0]["backend"])
	assert.Equal(t, float64(http.StatusBadGateway), completes[0]["status"])

	assert.NotEmpty(t, recordsWithMsg(recs, "backend round-trip failed"),
		"the transport error should be logged with its cause")
}

// fixedSelector always returns the same backend, ignoring health and circuit
// state. It forces ServeHTTP's pre-dispatch circuit admission path, which a
// real selector's Selectable() snapshot would otherwise route around.
type fixedSelector struct{ b *backend.Backend }

func (s fixedSelector) Select(context.Context, *http.Request) (*backend.Backend, error) {
	return s.b, nil
}

// openCircuit installs br as reg's gate and drives enough consecutive failures
// through it to open b's circuit. The minute-long cooldown keeps it open for
// the duration of a test.
func openCircuit(t *testing.T, reg *backend.Registry, br *circuit.Breaker, b *backend.Backend) {
	t.Helper()
	reg.SetCircuitGate(br)
	for i := 0; i < 10; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}
	require.False(t, reg.Allow(b), "setup: circuit must be open")
}

func TestProxyCircuitDenialReturns503WithoutIncActive(t *testing.T) {
	reg := registryFrom(t, backendEntry{"backend-a", startBackend(t, "backend-a").URL})
	b := reg.All()[0]
	br := circuit.New(time.Minute, slog.Default(), metrics.NewCollector())
	openCircuit(t, reg, br, b)

	p := New(reg, fixedSelector{b: b})

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/denied", nil))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, int64(0), b.ActiveConns(),
		"a circuit-denied request must never touch active-connection accounting")
}

func TestProxyLogsCircuitDenial(t *testing.T) {
	logger, dump := captureLogger(t)
	useLogger(t, logger)

	reg := registryFrom(t, backendEntry{"backend-a", "http://127.0.0.1:1"})
	b := reg.All()[0]
	br := circuit.New(time.Minute, slog.Default(), metrics.NewCollector())
	openCircuit(t, reg, br, b)

	p := New(reg, fixedSelector{b: b})
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/denied", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	recs := dump()
	denials := recordsWithMsg(recs, "request denied by circuit breaker")
	require.Len(t, denials, 1, "a denial must emit exactly one distinct WARN line")
	assert.Equal(t, "WARN", denials[0].level())
	assert.Equal(t, "backend-a", denials[0]["backend"])
	assert.Equal(t, "/denied", denials[0]["path"])

	completes := recordsWithMsg(recs, "request complete")
	require.Len(t, completes, 1)
	assert.Equal(t, "WARN", completes[0].level())
	assert.Equal(t, "backend-a", completes[0]["backend"])
	assert.Equal(t, float64(http.StatusServiceUnavailable), completes[0]["status"])
}

// TestProxyCircuitOpensOnRepeated5xxAndStopsRouting is MILESTONES.md's Sprint 3
// chaos-test exit criterion at unit-test scale: injecting 5xx responses on one
// backend eventually opens its circuit, after which selection stops routing to
// it while a healthy peer keeps serving.
func TestProxyCircuitOpensOnRepeated5xxAndStopsRouting(t *testing.T) {
	good := startBackend(t, "good")
	bad := failingBackend(t)

	reg := registryFrom(t,
		backendEntry{"good", good.URL},
		backendEntry{"bad", bad.URL},
	)
	br := circuit.New(time.Minute, slog.Default(), metrics.NewCollector())
	reg.SetCircuitGate(br)

	p := New(reg, balancer.NewRoundRobin(reg))
	p.RegisterObserver(NewLatencyObserver())
	p.RegisterObserver(br)

	front := httptest.NewServer(p)
	t.Cleanup(front.Close)

	badBackend := reg.All()[1]

	// Round-robin alternates the two backends; the bad backend's own 5xx
	// outcomes are consecutive from its perspective, so its circuit opens.
	for i := 0; i < 40; i++ {
		resp, err := front.Client().Get(front.URL)
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	require.NotContains(t, reg.Selectable(), badBackend,
		"repeated 5xx must open the bad backend's circuit and exclude it from selection")
	assert.Equal(t, int64(0), badBackend.ActiveConns())

	for i := 0; i < 10; i++ {
		resp, err := front.Client().Get(front.URL)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		_ = resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "good", string(body),
			"an open-circuit backend must stop receiving traffic")
	}
}

// TestProxyObserveSkipsRemovedBackend pins ADR-0015 decision 8 at the fan-out
// seam: once a backend is removed, neither a successful nor a failed round trip
// reaches any observer.
func TestProxyObserveSkipsRemovedBackend(t *testing.T) {
	const url = "http://127.0.0.1:9001"
	reg := registryFrom(t, backendEntry{"backend-a", url})
	b := reg.All()[0]

	p := New(reg, balancer.NewRoundRobin(reg))
	spy := &spyObserver{}
	p.RegisterObserver(spy)

	_, _, err := reg.Apply(
		config.BackendDiff{Removed: []config.BackendConfig{{Name: "backend-a", URL: url}}},
		nil,
	)
	require.NoError(t, err)

	p.observe(&reqState{backend: b}, time.Millisecond, true)
	p.observe(&reqState{backend: b}, p2cFailurePenalty, false)

	assert.Empty(t, spy.snapshot(),
		"a removed backend must report nothing to any observer, success or failure")
}

// TestProxySuppressesObserversForRequestCompletingAfterRemoval is the
// integration counterpart: a request selected before its backend is removed is
// still in flight when the swap lands. On completion it must reach no observer,
// yet its active-connection slot must still be released — removed backends are
// suppressed from observers but not from connection accounting.
func TestProxySuppressesObserversForRequestCompletingAfterRemoval(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "2xx completion", status: http.StatusOK},
		{name: "5xx completion", status: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				close(entered)
				<-release
				w.WriteHeader(tt.status)
			}))
			t.Cleanup(srv.Close)

			reg := registryFrom(t, backendEntry{"backend-a", srv.URL})
			b := reg.All()[0]
			p := New(reg, balancer.NewRoundRobin(reg))
			spy := &spyObserver{}
			p.RegisterObserver(spy)

			done := make(chan struct{})
			go func() {
				defer close(done)
				p.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
			}()

			<-entered // the request is dispatched and blocked in the backend

			_, removed, err := reg.Apply(
				config.BackendDiff{Removed: []config.BackendConfig{{Name: "backend-a", URL: srv.URL}}},
				nil,
			)
			require.NoError(t, err)
			require.Len(t, removed, 1)

			close(release)
			<-done

			assert.Empty(t, spy.snapshot(),
				"a request completing on a removed backend must reach no observer")
			assert.Zero(t, b.ActiveConns(),
				"the removed backend's active-connection slot must still be released")
		})
	}
}

// TestProxyDrainCancelBeforeHeadersReturns502 proves the drain join: retiring a
// backend with an in-flight request whose response headers have not arrived
// aborts the round trip, which the proxy turns into a 502, releases the
// active-connection slot, logs a window_expired reason, and (because the
// backend was already removed) reaches no observer. The removal-before-retire
// order mirrors production: a drain starts only for a backend a reload removed.
func TestProxyDrainCancelBeforeHeadersReturns502(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	reg := registryFrom(t, backendEntry{"backend-a", srv.URL})
	b := reg.All()[0]

	logger, dump := captureLogger(t)
	useLogger(t, logger)

	p := New(reg, balancer.NewRoundRobin(reg))
	spy := &spyObserver{}
	p.RegisterObserver(spy)
	front := httptest.NewServer(p)
	t.Cleanup(front.Close)

	type result struct {
		resp *http.Response
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := front.Client().Get(front.URL)
		resCh <- result{resp, err}
	}()

	<-entered
	require.Eventually(t, func() bool { return b.ActiveConns() == 1 },
		5*time.Second, 10*time.Millisecond, "the request should be in flight and counted")

	_, removed, err := reg.Apply(
		config.BackendDiff{Removed: []config.BackendConfig{{Name: "backend-a", URL: srv.URL}}},
		nil,
	)
	require.NoError(t, err)
	require.Len(t, removed, 1)

	b.Retire()

	got := <-resCh
	require.NoError(t, got.err)
	defer got.resp.Body.Close()
	assert.Equal(t, http.StatusBadGateway, got.resp.StatusCode)

	close(release)
	require.Eventually(t, func() bool { return b.ActiveConns() == 0 },
		5*time.Second, 10*time.Millisecond, "the drain-cancelled request must release its slot")

	assert.Empty(t, spy.snapshot(), "a drain cancellation on a removed backend must reach no observer")

	var fails []logRecord
	require.Eventually(t, func() bool {
		fails = recordsWithMsg(dump(), "backend round-trip failed")
		return len(fails) == 1
	}, 5*time.Second, 10*time.Millisecond, "the drain cancellation must emit exactly one WARN line")
	assert.Equal(t, "WARN", fails[0].level())
	assert.Equal(t, "window_expired", fails[0]["reason"])
	assert.Equal(t, "backend-a", fails[0]["backend"])
	assert.Empty(t, recordsWithReason(dump(), "client_canceled"),
		"a drain cancellation must classify as window_expired, not client-gone")
}

// TestProxyClientCancelReachesNoObserverAndRecords499 is the T5 chaos test: a
// client that disconnects mid-request (its request context is cancelled, which
// is exactly what net/http does on a dropped connection) must be classified as
// client-gone, not as a backend failure. It reaches no observer, records no
// EWMA latency, releases its active-connection slot, and is recorded as 499 →
// status_class "4xx" with an INFO line carrying reason client_canceled. The
// request is held open by a gated backend so the cancellation lands on an
// in-flight round trip deterministically.
func TestProxyClientCancelReachesNoObserverAndRecords499(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	reg := registryFrom(t, backendEntry{"backend-a", srv.URL})
	b := reg.All()[0]

	logger, dump := captureLogger(t)
	useLogger(t, logger)

	p := New(reg, balancer.NewRoundRobin(reg))
	spy := &spyObserver{inner: NewLatencyObserver()}
	p.RegisterObserver(spy)
	c := metrics.NewCollector()
	p.SetMetrics(c)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/cancel", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		p.ServeHTTP(rec, req)
	}()

	<-entered
	require.Equal(t, int64(1), b.ActiveConns(), "the request must be in flight before the client leaves")

	cancel()
	<-done
	close(release)

	assert.Equal(t, 499, rec.Code, "a client-gone request must be recorded as 499")
	assert.Empty(t, spy.snapshot(), "a client cancellation must reach no observer")
	assert.Zero(t, b.EWMALatency(), "a client cancellation must record no EWMA latency")
	assert.Zero(t, b.ActiveConns(), "a client cancellation must still release its active-connection slot")

	completes := recordsWithMsg(dump(), "request complete")
	require.Len(t, completes, 1)
	assert.Equal(t, "INFO", completes[0].level())
	assert.Equal(t, float64(499), completes[0]["status"])

	fails := recordsWithMsg(dump(), "backend round-trip failed")
	require.Len(t, fails, 1, "the client cancellation must emit exactly one cause line")
	assert.Equal(t, "INFO", fails[0].level())
	assert.Equal(t, "client_canceled", fails[0]["reason"])
	assert.Equal(t, "backend-a", fails[0]["backend"])

	counter := labeledSeries(t, c, "lb_requests_total",
		map[string]string{"backend": "backend-a", "method": "GET", "status_class": "4xx"})
	require.NotNil(t, counter, "client churn must stay visible as a 4xx request")
	assert.Equal(t, 1.0, counter.GetCounter().GetValue())
	assert.Nil(t, labeledSeries(t, c, "lb_requests_total",
		map[string]string{"backend": "backend-a", "method": "GET", "status_class": "5xx"}),
		"a client cancellation must never land in the 5xx class reserved for backend failures")
}

// TestProxyResponseHeaderTimeoutReachesObserversAsFailure is the guard on the
// other side of the T5 predicate: a transport's own response-header timer
// leaves the client's request context alive, so the failure must fall through
// to the genuine-transport-failure tier — observers get the fixed penalty, the
// response is 502, and the cause line is the WARN failure line, not
// client_canceled. The proxy runs on an explicit transport here (configuring
// the production transport is S4.T8); a gated backend that never sends headers
// trips the timer deterministically.
func TestProxyResponseHeaderTimeoutReachesObserversAsFailure(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	reg := registryFrom(t, backendEntry{"backend-a", srv.URL})
	b := reg.All()[0]

	logger, dump := captureLogger(t)
	useLogger(t, logger)

	p := New(reg, balancer.NewRoundRobin(reg))
	p.rp.Transport = &http.Transport{ResponseHeaderTimeout: 50 * time.Millisecond}
	spy := &spyObserver{inner: NewLatencyObserver()}
	p.RegisterObserver(spy)

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slow", nil))
	close(release)

	assert.Equal(t, http.StatusBadGateway, rec.Code,
		"a backend that never answers headers must fail as a transport failure, not client-gone")

	calls := spy.snapshot()
	require.Len(t, calls, 1, "a response-header timeout must reach the observers as a failure")
	assert.False(t, calls[0].success)
	assert.Equal(t, p2cFailurePenalty, calls[0].d)
	assert.Equal(t, p2cFailurePenalty, b.EWMALatency(),
		"a backend timeout must record the failure penalty, not be over-suppressed")
	assert.Zero(t, b.ActiveConns())

	fails := recordsWithMsg(dump(), "backend round-trip failed")
	require.Len(t, fails, 1)
	assert.Equal(t, "WARN", fails[0].level())
	assert.Empty(t, recordsWithReason(dump(), "client_canceled"),
		"a backend timeout must not be misclassified as client-gone")
}

// TestProxyClientCancelRearmsHalfOpenTrial is the regression test for the wedge
// the T5 suppression would otherwise introduce: a client-gone request that was
// admitted as a half-open circuit's single trial must re-arm the trial, or the
// circuit would deny every later request to a live backend forever. It forces
// the trial path with a fixed selector and a real circuit gate, cancels the
// in-flight trial request, and asserts a later admission is allowed again.
func TestProxyClientCancelRearmsHalfOpenTrial(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	reg := registryFrom(t, backendEntry{"backend-a", srv.URL})
	b := reg.All()[0]

	const cooldown = 20 * time.Millisecond
	br := circuit.New(cooldown, slog.Default(), metrics.NewCollector())
	reg.SetCircuitGate(br)
	for i := 0; i < 3; i++ {
		br.ObserveRoundTrip(b, 0, false)
	}
	require.True(t, b.CircuitOpen(cooldown), "setup: circuit must be open")

	p := New(reg, fixedSelector{b: b})
	p.RegisterObserver(br)

	require.Eventually(t, func() bool { return !b.CircuitOpen(cooldown) },
		5*time.Second, time.Millisecond, "the circuit must promote to half-open after its cooldown")

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/trial", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.ServeHTTP(rec, req)
	}()

	<-entered
	assert.False(t, p.reg.Allow(b), "while the trial is out, no other request may be admitted")

	cancel()
	<-done
	close(release)

	assert.Equal(t, 499, rec.Code)
	assert.True(t, p.reg.Allow(b),
		"a client-gone trial must be re-armed so a later request can probe the backend")
}

// pathSelector routes by request path to a fixed backend, so one proxy can hold
// a drain-cancelled request on one backend while another backend serves
// normally.
type pathSelector struct {
	byPath map[string]*backend.Backend
}

func (s pathSelector) Select(_ context.Context, r *http.Request) (*backend.Backend, error) {
	if b, ok := s.byPath[r.URL.Path]; ok {
		return b, nil
	}
	return nil, balancer.ErrNoHealthyBackends
}

// TestProxyDrainCancelDoesNotAffectOtherBackends proves retiring one backend
// cancels only its own in-flight requests: a request held on the retired
// backend gets a 502 while a request on another backend completes 200.
func TestProxyDrainCancelDoesNotAffectOtherBackends(t *testing.T) {
	gateEntered := make(chan struct{})
	gateRelease := make(chan struct{})
	gated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(gateEntered)
		<-gateRelease
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(gated.Close)

	healthy := startBackend(t, "backend-b")

	reg := registryFrom(t,
		backendEntry{"backend-a", gated.URL},
		backendEntry{"backend-b", healthy.URL},
	)
	a, b := reg.All()[0], reg.All()[1]

	p := New(reg, pathSelector{byPath: map[string]*backend.Backend{
		"/a": a,
		"/b": b,
	}})
	front := httptest.NewServer(p)
	t.Cleanup(front.Close)

	resCh := make(chan *http.Response, 1)
	go func() {
		resp, err := front.Client().Get(front.URL + "/a")
		if err == nil {
			resCh <- resp
		}
	}()
	<-gateEntered

	respB, err := front.Client().Get(front.URL + "/b")
	require.NoError(t, err)
	bodyB, err := io.ReadAll(respB.Body)
	require.NoError(t, err)
	require.NoError(t, respB.Body.Close())
	assert.Equal(t, http.StatusOK, respB.StatusCode)
	assert.Equal(t, "backend-b", string(bodyB), "the other backend must serve normally")

	a.Retire()

	respA := <-resCh
	defer respA.Body.Close()
	assert.Equal(t, http.StatusBadGateway, respA.StatusCode,
		"retiring backend-a must cancel only backend-a's request")

	close(gateRelease)
	require.Eventually(t, func() bool { return a.ActiveConns() == 0 && b.ActiveConns() == 0 },
		5*time.Second, 10*time.Millisecond)
}

// TestProxyDrainCancelAfterHeadersTruncatesBody proves that retirement after
// response headers have been sent truncates the body rather than producing a
// second outcome: the success recorded when the headers arrived stands, no
// failure is recorded, and the active-connection slot still releases. The
// truncation is a voluntary, proxy-initiated stop, so it must not be logged as
// a backend death either.
func TestProxyDrainCancelAfterHeadersTruncatesBody(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Streaming (unknown Content-Length) makes ReverseProxy flush each
		// write immediately, so the client observes the partial body.
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "hello")
		_ = http.NewResponseController(w).Flush()
		<-release
		_, _ = io.WriteString(w, "world")
	}))
	t.Cleanup(srv.Close)

	reg := registryFrom(t, backendEntry{"backend-a", srv.URL})
	b := reg.All()[0]

	logger, dump := captureLogger(t)
	useLogger(t, logger)

	p := New(reg, balancer.NewRoundRobin(reg))
	spy := &spyObserver{inner: NewLatencyObserver()}
	p.RegisterObserver(spy)
	front := httptest.NewServer(p)
	t.Cleanup(front.Close)

	resp, err := front.Client().Get(front.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	prefix := make([]byte, len("hello"))
	_, err = io.ReadFull(resp.Body, prefix)
	require.NoError(t, err)
	require.Equal(t, "hello", string(prefix))

	require.Len(t, spy.snapshot(), 1, "headers must have recorded exactly one success")

	b.Retire()

	rest, readErr := io.ReadAll(resp.Body)
	assert.Error(t, readErr, "the body must be truncated after retirement")
	assert.Empty(t, rest, "no bytes written after retirement may reach the client")

	close(release)

	assert.Len(t, spy.snapshot(), 1,
		"a post-headers retirement must not record a second outcome")
	assert.True(t, spy.snapshot()[0].success,
		"the outcome recorded at headers was a success and must stay one")
	assert.Positive(t, b.EWMALatency(),
		"the success recorded at headers must stand")
	assert.Zero(t, b.ActiveConns(), "the slot must still release after a truncated body")

	assert.Empty(t, recordsWithReason(dump(), "backend_died_mid_response"),
		"a drain cancellation after headers is a voluntary stop, not a backend death")
}

// TestProxyClientCancelMidBodyIsNotMidBodyDeath proves the client-gone tier also
// holds on the body path: when the client disconnects while the response body
// is streaming, the resulting read error is a client-side cancellation, not a
// backend death, and must not be logged as one. The client reads the first body
// bytes before disconnecting, so the request is unambiguously past the headers
// and the failure lands on releaseBody.Read, not the error handler.
func TestProxyClientCancelMidBodyIsNotMidBodyDeath(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "hello")
		_ = http.NewResponseController(w).Flush()
		<-release
		_, _ = io.WriteString(w, "world")
	}))
	t.Cleanup(srv.Close)

	reg := registryFrom(t, backendEntry{"backend-a", srv.URL})
	b := reg.All()[0]

	logger, dump := captureLogger(t)
	useLogger(t, logger)

	p := New(reg, balancer.NewRoundRobin(reg))
	front := httptest.NewServer(p)
	t.Cleanup(front.Close)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, front.URL, nil)
	require.NoError(t, err)
	resp, err := front.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	prefix := make([]byte, len("hello"))
	_, err = io.ReadFull(resp.Body, prefix)
	require.NoError(t, err)
	require.Equal(t, "hello", string(prefix))

	cancel()
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	close(release)

	require.Eventually(t, func() bool { return b.ActiveConns() == 0 },
		5*time.Second, 10*time.Millisecond, "the cancelled request must release its slot")

	assert.Empty(t, recordsWithReason(dump(), "backend_died_mid_response"),
		"a client cancellation mid-body is a client-gone stop, not a backend death")
}

// midBodyDeathBackend starts an httptest.Server that answers with 200 headers
// promising more body bytes than it sends, then abruptly closes the connection.
// The proxy's body read therefore ends on a non-EOF error (an unexpected EOF),
// which is exactly the mid-body death S4.T6 detects.
func midBodyDeathBackend(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			return
		}
		// Content-Length is larger than the five bytes actually sent, so the
		// transport reads a truncated body and reports io.ErrUnexpectedEOF.
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 1024\r\n\r\nhello")
		_ = buf.Flush()
		_ = conn.Close()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestProxyBackendDiesMidBodyLogsAndKeepsSuccess is the T6 test: a backend that
// sends response headers then dies before completing the body is logged at WARN
// with reason backend_died_mid_response, carrying the backend, the path, and the
// bytes already copied. The success recorded when the headers arrived stands —
// the observer is called exactly once, for that success, never a second time for
// the death — and the active-connection slot still releases.
func TestProxyBackendDiesMidBodyLogsAndKeepsSuccess(t *testing.T) {
	logger, dump := captureLogger(t)
	useLogger(t, logger)

	srv := midBodyDeathBackend(t)
	reg := registryFrom(t, backendEntry{"backend-a", srv.URL})
	b := reg.All()[0]

	p := New(reg, balancer.NewRoundRobin(reg))
	spy := &spyObserver{inner: NewLatencyObserver()}
	p.RegisterObserver(spy)

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/partial", nil))

	assert.Equal(t, http.StatusOK, rec.Code,
		"the headers arrived, so the response is the success the observer recorded")
	assert.Equal(t, "hello", rec.Body.String(),
		"the bytes received before the death must reach the client")
	assert.Zero(t, b.ActiveConns(), "the slot must release after a truncated body")

	calls := spy.snapshot()
	require.Len(t, calls, 1, "the headers-time success must be the only observer call")
	assert.True(t, calls[0].success, "the recorded success must stand")
	assert.NotEqual(t, p2cFailurePenalty, calls[0].d,
		"a mid-body death must not record a second, failure event")

	deaths := recordsWithReason(dump(), "backend_died_mid_response")
	require.Len(t, deaths, 1, "the mid-body death must emit exactly one line")
	assert.Equal(t, "WARN", deaths[0].level())
	assert.Equal(t, "backend-a", deaths[0]["backend"])
	assert.Equal(t, "/partial", deaths[0]["path"])
	assert.Equal(t, float64(len("hello")), deaths[0]["bytes_copied"])

	completes := recordsWithMsg(dump(), "request complete")
	require.Len(t, completes, 1)
	assert.Equal(t, float64(http.StatusOK), completes[0]["status"])
}

// TestProxyCleanBodyReadLogsNoDeath pins the other half of the T6 predicate: a
// normally completed body ends on io.EOF, which is a clean end and must not be
// logged as a death.
func TestProxyCleanBodyReadLogsNoDeath(t *testing.T) {
	logger, dump := captureLogger(t)
	useLogger(t, logger)

	srv := startBackend(t, "backend-a")
	reg := registryFrom(t, backendEntry{"backend-a", srv.URL})
	p := New(reg, balancer.NewRoundRobin(reg))

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Empty(t, recordsWithReason(dump(), "backend_died_mid_response"),
		"a clean body end is io.EOF and must not log a death")
}
