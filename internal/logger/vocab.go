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

	// EventHealthReinstated is emitted when a previously ejected backend is
	// marked healthy again by the active health checker. reason is
	// ReasonProbeRecovered. Passive outlier detection never reinstates a
	// backend; only an active probe may prove recovery (ADR-0011 decision 2).
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
)
