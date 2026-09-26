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
	"github.com/DMJain/l7LoadBalancer/internal/logger"
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

// statusClientClosedRequest is nginx's client-closed-request code: the client
// disconnected before the proxy could answer, so no status was ever sent on the
// wire. The proxy writes it to the recorder, metrics, and logs only — it makes
// client churn legible as a 499/"4xx" rather than a backend-caused 5xx. It is
// deliberately not written as a 5xx because the client, not the backend, ended
// the request (S4.T5).
const statusClientClosedRequest = 499

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
	// metrics is the collector whose lb_active_connections gauge mirrors this
	// request's slot. It is the same reference ServeHTTP reads off Proxy, copied
	// here so release() can decrement the gauge next to DecActive without the
	// release closures needing the Proxy. nil means no collector is installed
	// and both the increment and decrement are skipped, so the gauge can never
	// drift from Backend.ActiveConns (ADR-0013 decision 7).
	metrics *metrics.Collector
	// clientCtx is the client request's context, captured in ServeHTTP before
	// the cancel-with-cause derivation. It is the discriminator the error
	// handler uses to tell a client-gone cancellation (clientCtx.Err() != nil)
	// from a transport failure: a drain cancellation lands on the derived
	// context's cause, and the transport's own timers leave this context alive,
	// so only a genuine client disconnect cancels it. Concurrency follows
	// ADR-0007: the state is created on and only touched by the request
	// goroutine, and the error handler runs on that same goroutine (S4.T5).
	clientCtx context.Context
	status    int
	once      sync.Once

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

// activate claims this request's active-connection slot, reporting it to both
// the backend's own counter and the lb_active_connections gauge at the same
// call site so the two cannot drift. It is called only on the dispatch path,
// after the circuit gate admits the request — a denied request never touches
// either (ADR-0013 decision 7).
func (s *reqState) activate() {
	s.backend.IncActive()
	if s.metrics != nil {
		s.metrics.IncActiveConnections(s.backend.Name)
	}
}

// release drops this request's active-connection slot exactly once, mirroring
// activate on both the backend counter and the gauge.
func (s *reqState) release() {
	s.once.Do(func() {
		s.backend.DecActive()
		if s.metrics != nil {
			s.metrics.DecActiveConnections(s.backend.Name)
		}
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
// afterwards, and the metrics reference is installed via SetMetrics at
// construction time and only read afterwards, so the request path needs no
// lock — see RegisterObserver and SetMetrics.
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
// that want latency recording — internal/app's Build does — must call
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
//
// A backend removed by a registry swap reports nothing: the whole fan-out is
// skipped for it, so no circuit, health, outlier, or metric write can come from
// a backend that is no longer in the fleet — this is what keeps every observer
// ignorant of reload and a same-name fresh backend's series reflecting only its
// own traffic (ADR-0015 decision 8). Active-connection accounting is not
// observer-driven and is deliberately not gated here: a removed backend must
// still release its slot in release()/releaseBody, or ActiveConns would never
// drain.
func (p *Proxy) observe(state *reqState, d time.Duration, success bool) {
	if state.backend.IsRemoved() {
		return
	}
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
	state := &reqState{metrics: p.metrics, clientCtx: r.Context()}
	defer p.completeRequest(r, state, start)

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

	state.activate()

	// Join this request to the backend's retired context. Retiring the backend
	// (a removed backend whose drain window expired, S4.T4) cancels this
	// request's outbound context with ErrDrainWindowExpired, which the
	// transport surfaces as a failed round trip and ErrorHandler turns into a
	// 502. context.AfterFunc registers the callback without a goroutine while
	// the request lives; the deferred stop removes the registration on
	// completion so a long-lived backend does not accumulate one dead callback
	// per request. No goroutine per request and no cancel-func registry
	// (ADR-0016 decision 3).
	reqCtx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	retired := b.RetiredContext()
	stopRetire := context.AfterFunc(retired, func() {
		cancel(context.Cause(retired))
	})
	defer stopRetire()

	ctx := context.WithValue(reqCtx, reqStateKey{}, state)
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
// The body wrapper also observes read errors, so a backend that dies after the
// headers but before completing the body leaves a WARN trace instead of a
// silent truncation (S4.T6) — while a drain or client-gone cancellation on the
// same path is recognised and not misreported as a backend death. The observer
// fan-out above runs before the body is streamed, so that death cannot be
// un-rung: the success recorded here stands.
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
	resp.Body = &releaseBody{
		ReadCloser: resp.Body,
		release:    state.release,
		logger:     p.logger,
		backend:    state.backend.Name,
		path:       resp.Request.URL.Path,
		reqCtx:     resp.Request.Context(),
		clientCtx:  state.clientCtx,
	}
	return nil
}

// errorHandler runs when no response could be proxied. It classifies the failed
// round trip into exactly one of three tiers, in this order, and only the
// genuine-transport tier counts as a backend failure:
//
//  1. Drain cancellation — context.Cause(r.Context()) is ErrDrainWindowExpired
//     (ADR-0016 decision 4). The backend was already removed, so observe()
//     suppresses the fan-out; the line carries reason window_expired at WARN.
//  2. Client-gone — state.clientCtx.Err() != nil: the client's own request
//     context is done, so the cancellation originated client-side. Observers
//     are not called, no EWMA latency is recorded, and the line carries reason
//     client_canceled at INFO. The request is recorded as 499, not 502.
//  3. Genuine transport failure — dial timeout, response-header timeout,
//     connection refused. Observers get the fixed 2s penalty, the line is a
//     WARN failure line with no reason, and the response is 502.
//
// The order is the predicate: a drain cancellation is checked first because it
// is the most specific cause; the client check comes before the transport
// fallback because the transport can only cancel the outbound context via
// parent propagation (client context done), the drain after-func, or its own
// timers — and the transport's own timers leave the client context alive, so a
// backend timeout still reaches tier 3. Context cancellation is sticky, so
// checking at error-handler time is race-free. On shutdown the HTTP server
// cancels in-flight request contexts, so shutdown-time cancellations classify
// as client-gone too; that is accepted and desirable, since suppressing
// observer writes while the process exits is correct (S4.T5).
//
// In every tier the active-connection slot is released (once-guarded, so the
// success path's body-wrapper Close and this path cannot double-decrement,
// ADR-0007). The canonical field vocabulary has no error field, so the cause
// rides its own line (ADR-0007 decision 5).
func (p *Proxy) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	attrs := []any{"err", err, "path", r.URL.Path}
	state := stateFrom(r.Context())
	if state == nil {
		p.logger.Warn("backend round-trip failed", attrs...)
		http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		return
	}
	attrs = append(attrs, "backend", state.backend.Name)

	level := slog.LevelWarn
	switch {
	case errors.Is(context.Cause(r.Context()), backend.ErrDrainWindowExpired):
		state.status = http.StatusBadGateway
		attrs = append(attrs, "reason", logger.ReasonWindowExpired)
		p.observe(state, p2cFailurePenalty, false)
	case state.clientCtx.Err() != nil:
		state.status = statusClientClosedRequest
		attrs = append(attrs, "reason", logger.ReasonClientCanceled)
		// No round trip completed, so nothing is observed — but if this
		// request was a half-open circuit's trial, its slot must be re-armed
		// or the circuit would deny every later request to a live backend
		// (ADR-0017).
		state.backend.RearmTrial()
		level = slog.LevelInfo
	default:
		state.status = http.StatusBadGateway
		p.observe(state, p2cFailurePenalty, false)
	}
	state.release()
	p.logger.Log(r.Context(), level, "backend round-trip failed", attrs...)

	http.Error(w, http.StatusText(state.status), state.status)
}

// releaseBody streams the response body while watching for a mid-body backend
// death, releases the request's active-connection slot when the body is closed,
// then closes the underlying body.
//
// Read passes every read straight through and counts the bytes delivered. A
// read error that is not io.EOF and was not a proxy-initiated stop — a drain
// cancellation or a client-gone cancellation — means the backend died after the
// headers (a connection reset, an unexpected end on a Content-Length response)
// and is logged at WARN with the bytes copied so far; io.EOF is a clean end and
// logs nothing. Reusing the error handler's discriminators keeps a voluntary
// drain stop or a client disconnect from being misreported as an involuntary
// backend death (ADR-0016, ADR-0017). The recorded success is deliberately not
// revisited: the observer already saw a success when the headers arrived, and a
// second failure event for the same request would corrupt the outlier window's
// counts (S4.T6). A backend that consistently dies after headers is therefore
// never ejected by passive detection — a known limitation, not fixed here.
//
// Concurrency: Read and Close run on the request goroutine, in ReverseProxy's
// synchronous body copy, so bytesCopied needs no synchronization (ADR-0007).
// Close's release stays once-guarded, so the success path and errorHandler
// cannot double-decrement.
type releaseBody struct {
	io.ReadCloser
	release func()

	// The mid-body-death fields are captured in ModifyResponse, when the
	// serving backend, the request path, and both contexts are known. reqCtx is
	// the outbound request's drain-joined context and clientCtx is the client's
	// own request context — the same discriminators the error handler uses.
	logger    *slog.Logger
	backend   string
	path      string
	reqCtx    context.Context
	clientCtx context.Context

	bytesCopied int64
}

func (b *releaseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.bytesCopied += int64(n)
	if err != nil && !errors.Is(err, io.EOF) && !b.canceled() {
		b.logger.Warn("backend died mid-response",
			"backend", b.backend,
			"path", b.path,
			"bytes_copied", b.bytesCopied,
			"reason", logger.ReasonBackendDiedMidResponse,
		)
	}
	return n, err
}

// canceled reports whether the failed read was ended by a proxy-initiated stop
// rather than a backend death: a drain cancellation (the outbound context's
// ErrDrainWindowExpired cause, ADR-0016) or a client-gone cancellation (the
// client's own context is done, ADR-0017). Context cancellation is sticky, so
// checking at read-error time is race-free; either way no backend died.
func (b *releaseBody) canceled() bool {
	if errors.Is(context.Cause(b.reqCtx), backend.ErrDrainWindowExpired) {
		return true
	}
	return b.clientCtx.Err() != nil
}

func (b *releaseBody) Close() error {
	b.release()
	return b.ReadCloser.Close()
}

// completeRequest is the single per-request completion hook. It pushes the
// whole-request metrics observation and emits the existing "request complete"
// log line from one place, sharing ServeHTTP's start and state, so the two
// observability surfaces can never disagree about a request's outcome. It runs
// on every exit path — success, both 503 short-circuits, and ErrorHandler —
// and on the http.ErrAbortHandler panic path, exactly where the deferred
// logRequest call already ran.
func (p *Proxy) completeRequest(r *http.Request, state *reqState, start time.Time) {
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
//
// Unlike the RoundTripObserver fan-out in observe, this whole-request hook is
// NOT suppressed for a removed backend. The removed-backend rule (ADR-0015
// decision 8) governs the observers — circuit, health, outlier, and their
// gauges — which a reload deletes; the request counter and latency histogram
// are aggregated by backend name like Backend.ActiveConns and cannot be reset
// per instance, so suppressing one late request would not isolate a same-name
// fresh backend's series anyway. The active-connection gauge's own increment
// and decrement stay in activate/release regardless, so this hook's label and
// the gauge it mirrors never disagree.
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

// statusClass maps an HTTP status code to its class label ("2xx", "4xx",
// "5xx", …), matching the metric's status_class vocabulary. A per-code label
// would make
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
