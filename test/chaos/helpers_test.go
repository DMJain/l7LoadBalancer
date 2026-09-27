// Package chaos_test holds the Sprint 3 chaos scenarios. It is an external
// test package (package chaos_test, no non-test files) so its import graph is
// exactly a consumer's and the acyclic package guarantee stays visible: the
// tests assemble the system through internal/app's Build/Run seam (S4.T0),
// which is the same wiring main uses, and reach the subsystems through their
// exported APIs only.
//
// The harness here is shared with S3.T9's circuit chaos test: assemble()
// builds through internal/app, flippableBackend injects failures, and the
// log/gauge helpers read the two observability surfaces back.
package chaos_test

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// flippableBackend is the chaos harness's failure-injection point: a real HTTP
// server whose response class can be swapped (200 / 500) and whose listener can
// be killed outright. Alongside assemble it is the only mutation surface the
// tests touch; nothing reaches into a backend's or the proxy's internals.
type flippableBackend struct {
	t    *testing.T
	id   string
	addr string

	// handler is swapped by Serve200/Serve500 while health-checker and proxy
	// goroutines invoke it, so it is an atomic pointer rather than a plain
	// field: the swap must serialize without a mutex around the request path
	// (the race-detector surface this helper exists to keep clean).
	handler atomic.Pointer[http.Handler]

	// requests counts every request that reaches the backend. It is
	// incremented in serve before the installed handler runs — before any
	// gating — so a test can assert that traffic shifted to one backend or
	// froze on another without reading proxy internals.
	requests atomic.Int64

	mu  sync.Mutex
	srv *http.Server
}

// newFlippableBackend starts a 200-serving backend with the given identity.
func newFlippableBackend(t *testing.T, id string) *flippableBackend {
	t.Helper()
	fb := &flippableBackend{t: t, id: id}
	fb.Serve200()
	fb.start()
	t.Cleanup(fb.Kill)
	return fb
}

// newFlippableBackends starts n backends named backend-a, backend-b, ...
func newFlippableBackends(t *testing.T, n int) []*flippableBackend {
	t.Helper()
	ids := []string{"backend-a", "backend-b", "backend-c", "backend-d"}
	require.LessOrEqual(t, n, len(ids), "more flippable backends than preset ids")
	fbs := make([]*flippableBackend, n)
	for i := 0; i < n; i++ {
		fbs[i] = newFlippableBackend(t, ids[i])
	}
	return fbs
}

// URL is the backend's http://host:port base, stable across Kill/Restart.
func (fb *flippableBackend) URL() string { return "http://" + fb.addr }

// Serve200 makes the backend answer 200 with its id as the body.
func (fb *flippableBackend) Serve200() {
	h := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, fb.id)
	}))
	fb.handler.Store(&h)
}

// Serve500 makes the backend answer 500, the "up but broken" signal passive
// outlier detection and the circuit breaker both watch.
func (fb *flippableBackend) Serve500() {
	h := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	fb.handler.Store(&h)
}

// Kill takes the backend down: closing the server closes its listener and any
// live connections, so a subsequent probe or proxy dispatch gets a real
// ECONNREFUSED rather than an HTTP error. This is the "process died" failure
// mode, not a handler swap to 503.
func (fb *flippableBackend) Kill() {
	fb.mu.Lock()
	srv := fb.srv
	fb.srv = nil
	fb.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
}

// Restart reopens a listener on the same address and resumes serving.
// Rebinding races the OS releasing the old port, so it retries briefly.
func (fb *flippableBackend) Restart() {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if fb.srv != nil {
		return
	}
	var (
		ln  net.Listener
		err error
	)
	for i := 0; i < 100; i++ {
		ln, err = net.Listen("tcp", fb.addr)
		if err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	require.NoError(fb.t, err, "rebind %s", fb.addr)
	srv := &http.Server{Handler: http.HandlerFunc(fb.serve)}
	fb.srv = srv
	go func() { _ = srv.Serve(ln) }()
}

// serve dispatches to the currently-installed handler.
func (fb *flippableBackend) serve(w http.ResponseWriter, r *http.Request) {
	fb.requests.Add(1)
	(*fb.handler.Load()).ServeHTTP(w, r)
}

// RequestCount is the number of requests that have reached the backend,
// including requests still held open by a gate.
func (fb *flippableBackend) RequestCount() int64 { return fb.requests.Load() }

// start binds the first listener and records the address.
func (fb *flippableBackend) start() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(fb.t, err)
	fb.addr = ln.Addr().String()
	srv := &http.Server{Handler: http.HandlerFunc(fb.serve)}
	fb.mu.Lock()
	fb.srv = srv
	fb.mu.Unlock()
	go func() { _ = srv.Serve(ln) }()
}

// TestFlippableBackendRequestCount proves the harness's per-backend request
// counter: it increments exactly once per request at handler entry — before
// any gating, so a request held open by the gate is already counted — and is
// readable without touching proxy or registry internals.
func TestFlippableBackendRequestCount(t *testing.T) {
	fb := newFlippableBackend(t, "backend-a")
	require.Zero(t, fb.RequestCount(), "a fresh backend has served no requests")

	// A gated request is counted while it is still held open at the gate.
	release := make(chan struct{})
	entered := make(chan string, 1)
	fb.ServeGated(entered, release)

	statusCh := make(chan int, 1)
	go func() {
		resp, err := http.Get(fb.URL())
		if err != nil {
			t.Error(err)
			statusCh <- 0
			return
		}
		defer resp.Body.Close()
		statusCh <- resp.StatusCode
	}()

	select {
	case <-entered:
	case <-time.After(eventuallyDeadline):
		t.Fatal("the gated request never reached the backend")
	}
	require.Equal(t, int64(1), fb.RequestCount(), "a held-open request is counted at entry, before gating")

	close(release)
	require.Equal(t, http.StatusOK, <-statusCh, "the gated request must complete once released")
	require.Equal(t, int64(1), fb.RequestCount(), "completing a request must not double-count")

	// Every later request increments the counter exactly once.
	fb.Serve200()
	for i := 0; i < 3; i++ {
		resp, err := http.Get(fb.URL())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	}
	require.Equal(t, int64(4), fb.RequestCount())
}

// doRequest drives one request through the proxy handler directly, with no
// client-server hop, and returns the status and body. It is safe to call inside
// a require.Eventually condition: it never touches *testing.T.
func doRequest(h http.Handler) (int, string) {
	return doRequestPath(h, "/")
}

// doRequestPath is doRequest with an explicit path, so a test can assert the
// path on a request-scoped log line.
func doRequestPath(h http.Handler, path string) (int, string) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}
