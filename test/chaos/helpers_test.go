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
	(*fb.handler.Load()).ServeHTTP(w, r)
}

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

// doRequest drives one request through the proxy handler directly, with no
// client-server hop, and returns the status and body. It is safe to call inside
// a require.Eventually condition: it never touches *testing.T.
func doRequest(h http.Handler) (int, string) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}
