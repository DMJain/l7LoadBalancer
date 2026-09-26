// Package logger configures the application's log/slog structured logger.
//
// Canonical structured field names. All logging across this project MUST
// use these names so log lines stay grep-able and consistent.
//
// Request-scoped fields:
//
//	backend      - name of the backend a log line concerns
//	method       - HTTP method
//	status       - HTTP response status code
//	latency_ms   - request latency in milliseconds
//	remote_addr  - client remote address
//	path         - request path
//
// Transition-scoped fields (Sprint 3; values are the closed Go-constant
// vocabularies in vocab.go). A transition line also carries the request-scoped
// backend field, naming the backend the transition concerns:
//
//	backend      - backend the transition concerns (shared with request-scoped lines)
//	event        - a state transition that occurred, one of the Event* constants
//	reason       - why it occurred, one of the Reason* constants
//
// Reload-scoped fields (Sprint 4; the reload events are in the same Event*
// vocabulary):
//
//	event        - config_reloaded or config_reload_failed
//	reason       - why a reload failed, one of the Reason* constants
//	added        - number of backends the reload added (config_reloaded)
//	removed      - number of backends the reload removed (config_reloaded)
//	unchanged    - number of backends the reload left untouched (config_reloaded)
//	fields       - names of the changed non-backend fields (config_reload_failed
//	               with reason non_backend_change)
//
// Drain-scoped fields (Sprint 4; ADR-0016). reason window_expired appears on
// both the proxy's "backend round-trip failed" line for a request a drain
// cancelled and the "backend drained" line for a drain that had to cancel:
//
//	event        - backend_drained, once per removed backend when its drain ends
//	reason       - idle (became idle before the window) or window_expired (the
//	               window elapsed with requests still in flight)
//	cancelled    - number of requests the drain cancelled (backend_drained)
//
// Cancellation-scoped fields (Sprint 4; S4.T5). reason client_canceled appears
// on the proxy's "backend round-trip failed" line, logged at INFO when the
// client's own request context was done before a response arrived. The request
// is recorded as 499 (status class 4xx), reaches no observer, and records no
// EWMA latency:
//
//	reason       - client_canceled, a client-gone cancellation, not a backend
//	               failure
//
// Mid-body-death-scoped fields (Sprint 4; S4.T6). reason
// backend_died_mid_response appears on the proxy's WARN "backend died
// mid-response" line when a non-EOF body read error follows the response
// headers — the backend died before completing the body. The success recorded
// when the headers arrived stands; no observer is fed a second event:
//
//	backend      - backend that died
//	path         - request path
//	bytes_copied - response-body bytes copied to the client before the read error
//	reason       - backend_died_mid_response
//
// See docs/design/sprint-1-contracts.md "Log field vocabulary" for an
// example log line per event type.
package logger
