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

// captureLogger returns a logger writing JSON to an in-memory buffer, plus a
// function that parses every line emitted so far.
func captureLogger(t *testing.T) (*slog.Logger, func() []logRecord) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

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

func recordsWithMsg(recs []logRecord, msg string) []logRecord {
	var out []logRecord
	for _, r := range recs {
		if r.msg() == msg {
			out = append(out, r)
		}
	}
	return out
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
	br := circuit.New(time.Minute, slog.Default())
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
	br := circuit.New(time.Minute, slog.Default())
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
	br := circuit.New(time.Minute, slog.Default())
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
