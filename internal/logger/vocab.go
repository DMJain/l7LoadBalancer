package logger

// Transition event names — the closed value vocabulary for the canonical
// `event` structured-log field. They name a state transition produced by
// Sprint 3's resilience subsystems (active health checks, passive outlier
// detection, the per-backend circuit breaker).
//
// The values are snake_case Go constants, matching the algorithm-identifier
// convention in internal/config (round_robin, least_conn, ...), so a future
// agent grepping logs for circuit_opened cannot be defeated by a typo'd
// variant: there is no free-form string to misspell. Adding a transition is a
// deliberate edit to this constant block and to the ADR that records the
// vocabulary — see ADR-0013 decision 12.
//
// `event` is transition-scoped, unlike the six request-scoped fields in
// doc.go: every transition line carries a `backend` and a paired `reason`
// (one of the Reason* constants), not a method/status/path.
const (
	// EventHealthEjected is emitted when a backend is marked unhealthy.
	// reason is ReasonProbeFailures when the active health checker crossed its
	// consecutive-failure threshold, or ReasonOutlierWindow when passive
	// outlier detection crossed its in-window failure threshold.
	EventHealthEjected = "health_ejected"

	// EventHealthReinstated is emitted when a backend becomes healthy again:
	// either a previously ejected backend is marked healthy by the active
	// health checker (reason ReasonProbeRecovered), or a backend a reload just
	// added is admitted by its first successful probe (reason
	// ReasonInitialProbe). Passive outlier detection never reinstates a
	// backend; only an active probe may prove health (ADR-0011 decision 2,
	// ADR-0015 decision 10).
	EventHealthReinstated = "health_reinstated"

	// EventCircuitOpened is emitted when a backend's circuit moves to Open.
	// reason is ReasonConsecutiveFailures (Closed → Open) or ReasonTrialFailure
	// (a Half-Open trial failed → Open).
	EventCircuitOpened = "circuit_opened"

	// EventCircuitClosed is emitted when a Half-Open trial succeeds and the
	// circuit returns to Closed. reason is ReasonTrialSuccess.
	EventCircuitClosed = "circuit_closed"

	// EventCircuitHalfOpened is emitted when an Open circuit's cooldown has
	// elapsed and a trial request is admitted; reason is ReasonCooldownElapsed.
	// A failed trial does not half-open again — it reopens with
	// EventCircuitOpened/ReasonTrialFailure (the circuit was Open, not
	// Half-Open, once the trial failed). A Half-Open promotion whose CAS is won
	// by a Registry.Selectable() scan is never logged — a documented, permanent
	// gap (ADR-0013 decision 13).
	EventCircuitHalfOpened = "circuit_half_opened"

	// EventConfigReloaded is emitted once per successful SIGHUP reload,
	// carrying the added, removed, and unchanged backend counts. It is logged
	// at WARN when unchanged is zero (a blue/green reload with a brief
	// empty-selectable window) and INFO otherwise (ADR-0015 decision 11).
	EventConfigReloaded = "config_reloaded"

	// EventConfigReloadFailed is emitted once per rejected reload, carrying a
	// reason: ReasonParseError, ReasonValidationError, ReasonNonBackendChange,
	// or the defensive ReasonApplyError. A non-backend change additionally
	// carries the changed field names under a `fields` attribute.
	EventConfigReloadFailed = "config_reload_failed"

	// EventBackendDrained is emitted once per removed backend when its drain
	// finishes: it became idle before the window (reason ReasonIdle) or the
	// window elapsed and its remaining requests were cancelled (reason
	// ReasonWindowExpired). The line carries the number of requests cancelled.
	// See ADR-0016 decision 7.
	EventBackendDrained = "backend_drained"
)

// Transition reason names — the closed value vocabulary for the canonical
// `reason` structured-log field, paired with an Event* value on the same line.
// Grouped by the subsystem that produces them, matching ADR-0013 decision 12.
const (
	// ReasonProbeFailures is paired with EventHealthEjected: the active health
	// checker observed its consecutive-failure threshold.
	ReasonProbeFailures = "probe_failures"

	// ReasonProbeRecovered is paired with EventHealthReinstated: the active
	// health checker observed its consecutive-success threshold.
	ReasonProbeRecovered = "probe_recovered"

	// ReasonInitialProbe is paired with EventHealthReinstated: a backend a
	// reload just added answered its first successful probe, admitting it to
	// the selectable set. Distinct from ReasonProbeRecovered so first admission
	// is legible apart from ejection recovery (ADR-0015 decision 10).
	ReasonInitialProbe = "initial_probe"

	// ReasonOutlierWindow is paired with EventHealthEjected: passive outlier
	// detection observed its in-window failure threshold.
	ReasonOutlierWindow = "outlier_window"

	// ReasonConsecutiveFailures is paired with EventCircuitOpened: the circuit's
	// consecutive-failure count reached its failure-to-open threshold.
	ReasonConsecutiveFailures = "consecutive_failures"

	// ReasonTrialSuccess is paired with EventCircuitClosed: a Half-Open trial
	// request succeeded.
	ReasonTrialSuccess = "trial_success"

	// ReasonTrialFailure is paired with EventCircuitOpened: a Half-Open trial
	// request failed, reopening the circuit without an inner threshold.
	ReasonTrialFailure = "trial_failure"

	// ReasonCooldownElapsed is paired with EventCircuitHalfOpened: the Open
	// circuit's cooldown elapsed, admitting a trial request.
	ReasonCooldownElapsed = "cooldown_elapsed"

	// ReasonParseError is paired with EventConfigReloadFailed: the reloaded
	// file could not be strictly decoded.
	ReasonParseError = "parse_error"

	// ReasonValidationError is paired with EventConfigReloadFailed: the
	// reloaded config failed Validate.
	ReasonValidationError = "validation_error"

	// ReasonNonBackendChange is paired with EventConfigReloadFailed: the
	// reloaded config changed a field other than the backend list, so the
	// reload is rejected whole (ADR-0015 decision 4). The changed field names
	// ride a `fields` attribute.
	ReasonNonBackendChange = "non_backend_change"

	// ReasonApplyError is paired with EventConfigReloadFailed: applying the
	// diff to the registry failed. Defensive only — config validation rules
	// out the inconsistent diffs Apply rejects, so production should never
	// emit it.
	ReasonApplyError = "apply_error"

	// ReasonWindowExpired attributes a request that a drain cancelled when a
	// removed backend's drain window elapsed. It is the reason on the WARN
	// "backend round-trip failed" line the proxy emits for such a request; it
	// is not a backend failure, so it reaches no observer and no circuit,
	// health, or outlier state (ADR-0016 decision 4). It is also the reason on
	// the backend drained event when the drain's window elapsed with requests
	// still in flight (ADR-0016 decision 7).
	ReasonWindowExpired = "window_expired"

	// ReasonIdle is paired with EventBackendDrained: a removed backend's drain
	// finished because its active-connection count reached zero before the
	// window elapsed, so no in-flight request had to be cancelled (ADR-0016
	// decision 7).
	ReasonIdle = "idle"

	// ReasonClientCanceled attributes a failed round trip the client abandoned
	// before a response arrived: the client's own request context was done, so
	// the cancellation is client-side, not a backend failure. It is the reason
	// on the proxy's "backend round-trip failed" line, which the proxy logs at
	// INFO rather than WARN. A client-gone request reaches no observer, records
	// no EWMA latency, releases its active-connection slot, and is recorded as
	// 499 ("4xx") — reserving the 5xx class for backend-caused failures
	// (S4.T5).
	ReasonClientCanceled = "client_canceled"

	// ReasonBackendDiedMidResponse attributes a backend that sent response
	// headers but died before completing the body. It is the reason on the
	// proxy's WARN "backend died mid-response" line, which carries the backend,
	// the path, and the bytes already copied. The death reaches no observer and
	// records no second outcome: the success recorded when the headers arrived
	// stands, because a second failure event for the same request would corrupt
	// the outlier window's counts (S4.T6).
	ReasonBackendDiedMidResponse = "backend_died_mid_response"
)
