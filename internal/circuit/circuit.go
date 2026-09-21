package circuit

import (
	"log/slog"
	"time"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// circuitFailuresBeforeOpen is the consecutive-failure-to-open threshold. An
// unexported Go constant, not a config field (ADR-0011 decision 10), matching
// the active checker's three consecutive probe failures: the smallest count
// that reads as a trend rather than one bad request. See ADR-0012 decision 6.
const circuitFailuresBeforeOpen = 3

// Breaker is the per-backend circuit breaker's policy. The state itself lives
// on *backend.Backend as one CAS-guarded snapshot (ADR-0011 decision 5); this
// type holds only the tunables plus the observability sinks (logger and
// metrics collector) and routes calls to the right Backend method. It imports
// internal/backend and never the reverse, so the package graph stays acyclic —
// and because it depends only on backend, it can satisfy
// proxy.RoundTripObserver structurally without importing proxy (ADR-0012).
// Transition logging and the lb_circuit_state gauge both live here, at the call
// sites that observe a transition, so internal/backend needs no logging or
// metrics dependency (ADR-0013 decisions 10 and 15).
//
// Breaker implements backend.CircuitGate (Open/Allow) as well as the observer
// contract: main installs it as the registry's gate and registers it as a
// proxy round-trip observer.
//
// Concurrency: a Breaker is immutable after construction (cooldown, log, and
// metrics are set by New and only read afterwards), so its methods are safe to
// call from the concurrent request path and the health-check goroutines alike;
// all mutable state lives on the *backend.Backend passed in.
type Breaker struct {
	cooldown time.Duration
	log      *slog.Logger
	metrics  *metrics.Collector
}

// New builds a Breaker with the configured cooldown: how long a tripped circuit
// stays Open before a read may lazily promote it to Half-Open. The cooldown is
// read from Config by main (ADR-0011 decision 10), so it is passed in rather
// than imported here. log receives one line per genuine circuit transition
// (ADR-0013 decision 10) and collector receives one lb_circuit_state update
// from the same edge-triggered site (ADR-0013 decision 15); both must be
// non-nil.
func New(cooldown time.Duration, log *slog.Logger, collector *metrics.Collector) *Breaker {
	return &Breaker{cooldown: cooldown, log: log, metrics: collector}
}

// Open reports whether b's circuit is open, performing the lazy Open→Half-Open
// promotion. It is the gate's pool-eligibility question and never takes the
// half-open trial (ADR-0012 decision 1).
//
// A promotion this call performs is neither written to lb_circuit_state nor
// logged: Open is the Registry.Selectable read path and returns no transition,
// so a scan-won promotion has no logger or collector reachable from it — a
// documented, permanent gap (ADR-0013 decision 13).
func (br *Breaker) Open(b *backend.Backend) bool {
	return b.CircuitOpen(br.cooldown)
}

// Allow reports whether b may dispatch a request now, taking the half-open
// trial when that is the circuit's current state. It is the proxy's
// per-request admission gate.
//
// When this call is the one that promotes Open→Half-Open and takes the trial it
// writes lb_circuit_state=half_open and logs circuit_half_opened/
// cooldown_elapsed at INFO, both from the same CircuitTransition check so the
// gauge and the line can never disagree (ADR-0013 decision 15). A promotion
// already won by a Registry.Selectable scan is neither written nor logged here
// (the trial admission did not perform it) — the documented gap (ADR-0013
// decision 13).
func (br *Breaker) Allow(b *backend.Backend) bool {
	allowed, transition := b.CircuitAllow(br.cooldown)
	if transition == backend.CircuitHalfOpened {
		br.metrics.SetCircuitState(b.Name, metrics.CircuitStateHalfOpen)
		br.log.Info("circuit half-opened",
			"backend", b.Name,
			"event", logger.EventCircuitHalfOpened,
			"reason", logger.ReasonCooldownElapsed,
		)
	}
	return allowed
}

// ObserveRoundTrip folds one round trip's outcome into b's circuit. The failure
// signal is the observer contract's success flag: false is the union of a 5xx
// response and an errorHandler transport failure, the same signal passive
// outlier detection watches (ADR-0011 decisions 8 and 12). It is called
// unconditionally for every round trip; recording is unconditional, gating is
// conditional (ADR-0011 decision 9).
//
// Exactly one line and one lb_circuit_state write are emitted per genuine
// transition: circuit_opened/consecutive_failures or circuit_opened/
// trial_failure at WARN (state open), circuit_closed/trial_success at INFO
// (state closed). A repeated outcome that changes nothing (e.g. a failure while
// already Open) logs nothing and writes nothing, so a sustained failure streak
// does not repeat the opening line (ADR-0013 decision 15).
func (br *Breaker) ObserveRoundTrip(b *backend.Backend, _ time.Duration, success bool) {
	if success {
		if b.CircuitSuccess() == backend.CircuitClosed {
			br.metrics.SetCircuitState(b.Name, metrics.CircuitStateClosed)
			br.log.Info("circuit closed",
				"backend", b.Name,
				"event", logger.EventCircuitClosed,
				"reason", logger.ReasonTrialSuccess,
			)
		}
		return
	}
	switch b.CircuitFailure(circuitFailuresBeforeOpen) {
	case backend.CircuitOpened:
		br.metrics.SetCircuitState(b.Name, metrics.CircuitStateOpen)
		br.log.Warn("circuit opened",
			"backend", b.Name,
			"event", logger.EventCircuitOpened,
			"reason", logger.ReasonConsecutiveFailures,
		)
	case backend.CircuitReopened:
		br.metrics.SetCircuitState(b.Name, metrics.CircuitStateOpen)
		br.log.Warn("circuit reopened",
			"backend", b.Name,
			"event", logger.EventCircuitOpened,
			"reason", logger.ReasonTrialFailure,
		)
	}
}

var (
	_ backend.CircuitGate = (*Breaker)(nil)

	// Breaker satisfies proxy.RoundTripObserver structurally. The assertion
	// cannot name proxy.RoundTripObserver without importing internal/proxy,
	// which the AGENTS.md package graph keeps circuit free of (`circuit`
	// depends on `backend` only — an import of `proxy` would be acyclic but
	// undocumented). main registering the breaker against the interface is the
	// real compile-time check; this local shape pins the method signature.
	_ interface {
		ObserveRoundTrip(*backend.Backend, time.Duration, bool)
	} = (*Breaker)(nil)
)
