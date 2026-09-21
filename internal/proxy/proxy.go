package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"strconv"
	"sync"
	"time"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/balancer"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// p2cFailurePenalty is the latency recorded for a round trip that failed
// before a response was received. A fixed penalty, not the real
// time-to-failure: a backend failing fast (e.g. connection refused) would
// otherwise record a near-zero latency and look attractively fast to
// PowerOfTwoChoicesEWMA — the opposite of the intended effect. 2s comfortably
// dominates the committed dummy-backend latencies (50/150/300ms) with headroom
// because SLEEP_MS has no enforced ceiling. It is the fixed duration the
// failure path hands to the observer fan-out; the latency observer folds it
// through the EWMA like any observation rather than hard-setting the field, so
// repeated failures converge the estimate toward 2s. See ADR-0010.
const p2cFailurePenalty = 2 * time.Second

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

	// dispatchStart is captured at the end of director(), just before the
	// request is dispatched, and consumed in modifyResponse when the response
	// headers arrive. Its window is the backend round trip only. It is
	// deliberately NOT the `start` ServeHTTP captures for the "request
	// complete" log line's latency_ms: that window also includes selection
	// overhead and the full response-body copy to the client, so reusing one
	// measurement for both would conflate backend speed with client consume
	// time. See ADR-0010.
	dispatchStart time.Time
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

// RoundTripObserver is notified of every backend round trip's outcome. It is
// a consumer-defined interface (ADR-0002's Selector exception does not apply
// here): proxy only stores and calls it, while passive outlier detection
// (S3.T2) and the circuit breaker (S3.T3) implement it in their own packages.
//
// ObserveRoundTrip is called unconditionally — success and failure alike,
// regardless of the configured selector or the circuit's current state —
// mirroring how IncActive/DecActive already run unconditionally. "Recording is
// unconditional; gating is conditional": a future early-return guard on this
// call site looks like a reasonable simplification and is actively wrong,
// because it is the only path that feeds a half-open trial's result back into
// the circuit's decision. See ADR-0011 decision 9.
type RoundTripObserver interface {
	ObserveRoundTrip(b *backend.Backend, d time.Duration, success bool)
}

// latencyObserver feeds every round trip's duration into its backend's
// EWMA latency estimate — the behavior the proxy hardcoded before ADR-0011
// decision 9 generalized it into an observer fan-out. It ignores success: the
// duration it is handed already encodes the failure penalty on the error path.
type latencyObserver struct{}

func (latencyObserver) ObserveRoundTrip(b *backend.Backend, d time.Duration, _ bool) {
	b.RecordLatency(d)
}

var _ RoundTripObserver = latencyObserver{}

// NewLatencyObserver returns the round trip observer that records each
// duration into the serving backend's EWMA latency estimate (ADR-0010). main
// registers it after constructing the Proxy, alongside the other observers;
// see ADR-0011 decision 9.
func NewLatencyObserver() RoundTripObserver {
	return latencyObserver{}
}

// Proxy wraps net/http/httputil.ReverseProxy, delegating backend selection
// to a balancer.Selector on each request and tracking ActiveConns around the
// round trip. Implemented in S1.T6.
//
// Concurrency: the request path is stateless apart from the per-request
// reqState carried in the request context and the atomics on *backend.Backend.
// rp is built once in New and is safe for concurrent use. observers are
// appended to at construction time via RegisterObserver and only read
// afterwards, so the request path needs no lock — see RegisterObserver.
type Proxy struct {
	reg       *backend.Registry
	sel       balancer.Selector
	rp        *httputil.ReverseProxy
	logger    *slog.Logger
	observers []RoundTripObserver
	metrics   *metrics.Collector
}

// New constructs a Proxy over reg using sel for backend selection.
//
// New registers no RoundTripObservers: a bare New records nothing. Callers
// that want latency recording — main does, via newHandler — must call
// RegisterObserver(NewLatencyObserver()) (plus any other observers) before
// serving. The separation is deliberate; see ADR-0011 decision 9.
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

// RegisterObserver adds o to the set notified of every backend round trip.
// It is additive: New(reg, sel)'s frozen two-argument signature is untouched.
//
// RegisterObserver must be called before the Proxy begins serving traffic —
// observers are appended to a slice that ServeHTTP's goroutine reads without a
// lock. This matches the intended wiring: main registers every observer at
// construction time, before ListenAndServe.
func (p *Proxy) RegisterObserver(o RoundTripObserver) {
	p.observers = append(p.observers, o)
}

// SetMetrics installs the collector that receives one whole-request
// observation per request. It is optional and additive, mirroring
// RegisterObserver: a bare New(reg, sel) records no metrics, and New's frozen
// two-argument signature is untouched. No fan-out interface is introduced —
// metrics is this hook's only consumer (ADR-0013 decision 14).
//
// SetMetrics must be called before the Proxy begins serving traffic: the
// request path reads the field without a lock, matching how observers are
// registered at construction time in main.
func (p *Proxy) SetMetrics(c *metrics.Collector) {
	p.metrics = c
}

// observe fans a round trip's outcome out to every registered observer. It is
// the single unconditional recording path for both terminal hooks. See
// ADR-0011 decision 9.
func (p *Proxy) observe(state *reqState, d time.Duration, success bool) {
	for _, o := range p.observers {
		o.ObserveRoundTrip(state.backend, d, success)
	}
}

// ServeHTTP selects a backend, then proxies the request to it.
//
// Selection happens here rather than in Director because Director cannot
// write a response, so the ErrNoHealthyBackends path could not short-circuit
// to a 503. After selection the circuit gate is consulted via
// Registry.Allow(b) and only then is IncActive called: a denied request is
// answered 503 before dispatch and never touches active-connection accounting
// (ADR-0011 decision 7). On the dispatch path the chosen backend is attached
// to the request context for Director/ModifyResponse/ErrorHandler. One
// "request complete" line is logged per request via the deferred call, which
// also runs on the http.ErrAbortHandler panic path.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	state := &reqState{}
	defer p.recordRequest(r, state, start)

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
	if !p.reg.Allow(b) {
		state.status = http.StatusServiceUnavailable
		p.logger.Warn("request denied by circuit breaker",
			"backend", b.Name, "path", r.URL.Path)
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}

	b.IncActive()

	ctx := context.WithValue(r.Context(), reqStateKey{}, state)
	p.rp.ServeHTTP(w, r.WithContext(ctx))
}

// director reads the backend chosen in ServeHTTP off the request context and
// rewrites only the destination scheme and host. It never calls Select. It
// also marks the start of the backend round trip for the latency recorded in
// modifyResponse/errorHandler.
func (p *Proxy) director(r *http.Request) {
	state := stateFrom(r.Context())
	if state == nil {
		return
	}
	r.URL.Scheme = state.backend.URL.Scheme
	r.URL.Host = state.backend.URL.Host
	state.dispatchStart = time.Now()
}

// modifyResponse records the backend's status for logging, fans the backend
// round-trip outcome out to every registered observer, and wraps the response
// body so the active-connection slot is released when the client finishes
// consuming (or abandons) the body. Decrementing here directly would signal
// "done" while a streamed body is still being read. The actual decrement is
// once-guarded, so ErrorHandler calling release too is safe.
//
// The fan-out runs unconditionally, regardless of the configured selector —
// like IncActive/DecActive, it is not gated on any observer actually reading
// it. success is "this response is not a server error": a 5xx still reached
// the backend and produced a response, but is a failure signal for passive
// detection and the circuit. See ADR-0010 and ADR-0011 decision 9.
func (p *Proxy) modifyResponse(resp *http.Response) error {
	state := stateFrom(resp.Request.Context())
	if state == nil {
		return nil
	}
	state.status = resp.StatusCode
	p.observe(state, time.Since(state.dispatchStart), resp.StatusCode < 500)
	resp.Body = &releaseBody{ReadCloser: resp.Body, release: state.release}
	return nil
}

// errorHandler runs when no response could be proxied. It fans the fixed
// failure penalty out to every registered observer, releases the request's
// active-connection slot, logs the cause (the canonical field vocabulary has
// no error field, so the cause is a separate WARN line), and responds 502.
func (p *Proxy) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	attrs := []any{"err", err, "path", r.URL.Path}
	if state := stateFrom(r.Context()); state != nil {
		state.status = http.StatusBadGateway
		p.observe(state, p2cFailurePenalty, false)
		state.release()
		attrs = append(attrs, "backend", state.backend.Name)
	}
	p.logger.Warn("backend round-trip failed", attrs...)
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

// recordRequest is the single per-request completion hook. It pushes the
// whole-request metrics observation and emits the existing "request complete"
// log line from one place, sharing ServeHTTP's start and state, so the two
// observability surfaces can never disagree about a request's outcome. It runs
// on every exit path — success, both 503 short-circuits, and ErrorHandler —
// and on the http.ErrAbortHandler panic path, exactly where the deferred
// logRequest call already ran.
func (p *Proxy) recordRequest(r *http.Request, state *reqState, start time.Time) {
	p.observeRequest(r, state, start)
	p.logRequest(r, state, start)
}

// observeRequest feeds one whole client-facing request into the metrics
// collector: a counter increment and a duration observation on the same
// backend/method/status_class label set. The duration is the whole-request
// window (the same start as latency_ms), deliberately not RoundTripObserver's
// backend-round-trip-only measurement — there is exactly one definition of
// "request duration" on the metrics surface (ADR-0013 decision 3).
//
// The backend label is "" when no backend was chosen (no healthy backend
// found), derived exactly as logRequest derives it; a circuit-denied request
// still carries the real backend Select already identified. It is a no-op when
// no collector is installed (ADR-0013 decision 14).
func (p *Proxy) observeRequest(r *http.Request, state *reqState, start time.Time) {
	if p.metrics == nil {
		return
	}
	p.metrics.ObserveRequest(backendName(state), r.Method, statusClass(state.status), time.Since(start))
}

// backendName is the canonical backend label for a request: the serving
// backend's name, or "" when none was chosen. Shared by the metrics
// observation and the request-complete log line so the two cannot derive
// different labels.
func backendName(state *reqState) string {
	if state.backend == nil {
		return ""
	}
	return state.backend.Name
}

// statusClass maps an HTTP status code to its class label ("2xx", "5xx"),
// matching the metric's status_class vocabulary. A per-code label would make
// Prometheus cardinality unbounded from arbitrary upstream statuses
// (ADR-0013 decision 3).
func statusClass(status int) string {
	return strconv.Itoa(status/100) + "xx"
}

// logRequest emits the single per-request "request complete" line using the
// canonical field vocabulary. 5xx responses log at WARN, everything else at
// INFO.
func (p *Proxy) logRequest(r *http.Request, state *reqState, start time.Time) {
	latencyMS := float64(time.Since(start).Microseconds()) / 1000.0
	attrs := []any{
		"backend", backendName(state),
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
