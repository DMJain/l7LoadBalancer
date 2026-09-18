package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/balancer"
)

// reqState is the per-request bookkeeping shared between ServeHTTP, Director,
// ModifyResponse, and ErrorHandler.
//
// Concurrency: created on the request goroutine and only touched there —
// ReverseProxy handles a request synchronously — so status needs no lock.
// once guards release() because two different triggers may run for one
// request: the response-body wrapper's Close() (success path) and
// ErrorHandler (transport-failure path). Exactly one of them must decrement
// ActiveConns. See ADR-0007.
type reqState struct {
	backend *backend.Backend
	status  int
	once    sync.Once
}

// release drops this request's active-connection slot exactly once.
func (s *reqState) release() {
	s.once.Do(func() {
		s.backend.DecActive()
	})
}

// reqStateKey is the unexported context-key type carrying a *reqState. It is
// unexported so no other package can collide with or read the value.
type reqStateKey struct{}

func stateFrom(ctx context.Context) *reqState {
	state, _ := ctx.Value(reqStateKey{}).(*reqState)
	return state
}

// Proxy wraps net/http/httputil.ReverseProxy, delegating backend selection
// to a balancer.Selector on each request and tracking ActiveConns around the
// round trip. Implemented in S1.T6.
//
// Concurrency: the request path is stateless apart from the per-request
// reqState carried in the request context and the atomics on *backend.Backend.
// rp is built once in New and is safe for concurrent use.
type Proxy struct {
	reg    *backend.Registry
	sel    balancer.Selector
	rp     *httputil.ReverseProxy
	logger *slog.Logger
}

// New constructs a Proxy over reg using sel for backend selection.
//
// The logger is the process default captured at construction; the frozen
// New(reg, sel) signature does not take one, and main wires the default via
// internal/logger. See ADR-0007.
func New(reg *backend.Registry, sel balancer.Selector) *Proxy {
	p := &Proxy{
		reg:    reg,
		sel:    sel,
		logger: slog.Default(),
	}
	p.rp = &httputil.ReverseProxy{
		Director:       p.director,
		ModifyResponse: p.modifyResponse,
		ErrorHandler:   p.errorHandler,
	}
	return p
}

// ServeHTTP selects a backend, then proxies the request to it.
//
// Selection happens here rather than in Director because Director cannot
// write a response, so the ErrNoHealthyBackends path could not short-circuit
// to a 503. On the success path IncActive is called and the chosen backend is
// attached to the request context for Director/ModifyResponse/ErrorHandler.
// One "request complete" line is logged per request via the deferred call,
// which also runs on the http.ErrAbortHandler panic path.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	state := &reqState{}
	defer p.logRequest(r, state, start)

	b, err := p.sel.Select(r.Context(), r)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, balancer.ErrNoHealthyBackends) {
			status = http.StatusServiceUnavailable
		}
		state.status = status
		http.Error(w, http.StatusText(status), status)
		return
	}

	state.backend = b
	b.IncActive()

	ctx := context.WithValue(r.Context(), reqStateKey{}, state)
	p.rp.ServeHTTP(w, r.WithContext(ctx))
}

// director reads the backend chosen in ServeHTTP off the request context and
// rewrites only the destination scheme and host. It never calls Select.
func (p *Proxy) director(r *http.Request) {
	state := stateFrom(r.Context())
	if state == nil {
		return
	}
	r.URL.Scheme = state.backend.URL.Scheme
	r.URL.Host = state.backend.URL.Host
}

// modifyResponse records the backend's status for logging and wraps the
// response body so the active-connection slot is released when the client
// finishes consuming (or abandons) the body. Decrementing here directly would
// signal "done" while a streamed body is still being read. The actual
// decrement is once-guarded, so ErrorHandler calling release too is safe.
func (p *Proxy) modifyResponse(resp *http.Response) error {
	state := stateFrom(resp.Request.Context())
	if state == nil {
		return nil
	}
	state.status = resp.StatusCode
	resp.Body = &releaseBody{ReadCloser: resp.Body, release: state.release}
	return nil
}

// errorHandler runs when no response could be proxied. It releases the
// request's active-connection slot, logs the cause (the canonical field
// vocabulary has no error field, so the cause is a separate WARN line), and
// responds 502.
func (p *Proxy) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	state := stateFrom(r.Context())
	if state != nil {
		state.status = http.StatusBadGateway
		state.release()
		p.logger.Warn("backend round-trip failed",
			"err", err, "backend", state.backend.Name, "path", r.URL.Path)
	} else {
		p.logger.Warn("backend round-trip failed", "err", err, "path", r.URL.Path)
	}
	http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
}

// releaseBody releases the request's active-connection slot when the body is
// closed, then closes the underlying body.
type releaseBody struct {
	io.ReadCloser
	release func()
}

func (b *releaseBody) Close() error {
	b.release()
	return b.ReadCloser.Close()
}

// logRequest emits the single per-request "request complete" line using the
// canonical field vocabulary. 5xx responses log at WARN, everything else at
// INFO.
func (p *Proxy) logRequest(r *http.Request, state *reqState, start time.Time) {
	backendName := ""
	if state.backend != nil {
		backendName = state.backend.Name
	}
	latencyMS := float64(time.Since(start).Microseconds()) / 1000.0
	attrs := []any{
		"backend", backendName,
		"method", r.Method,
		"status", state.status,
		"latency_ms", latencyMS,
		"remote_addr", r.RemoteAddr,
		"path", r.URL.Path,
	}
	if state.status >= 500 {
		p.logger.Warn("request complete", attrs...)
		return
	}
	p.logger.Info("request complete", attrs...)
}

var _ http.Handler = (*Proxy)(nil)
