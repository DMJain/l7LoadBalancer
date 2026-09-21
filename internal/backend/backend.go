package backend

import (
	"net/url"
	"sync/atomic"
	"time"
)

// ewmaAlpha is α in the latency EWMA update
// latency_new = α·observed + (1-α)·latency_old. 0.1 smooths normal
// per-request jitter while adapting to a sustained regression within roughly
// ten requests. It is a Go constant, not a config field, matching the
// "constant, not config" posture ADR-0009 took for ε. See ADR-0010.
const ewmaAlpha = 0.1

// circuitState is the circuit breaker's three-state enum. It is unexported so
// callers must reach circuit state through Backend's methods, never a field.
// See ADR-0011 decision 5 and ADR-0012.
type circuitState int32

const (
	circuitClosed circuitState = iota
	circuitOpen
	circuitHalfOpen
)

// circuitSnapshot is the circuit breaker's entire state as one immutable value,
// replaced atomically: the state enum, the consecutive-failure count, the
// half-open trial flag, and the instant the circuit opened. Bundling them into
// one pointer means every compound transition — open, close, promote, take the
// trial — is a single CompareAndSwap, which is what gives the half-open trial
// slot its exactly-one guarantee without a mutex (ADR-0011 decision 6). A nil
// pointer reads as the zero snapshot, i.e. a closed circuit, so a plain
// &Backend{} needs no initialization.
type circuitSnapshot struct {
	state    circuitState
	failures int32
	trial    bool
	openedAt time.Time
}

// CircuitTransition reports what one call to a Backend circuit method did to
// the circuit's state, so a caller (internal/circuit.Breaker) can log exactly
// the genuine transitions and nothing else. Returning a plain value keeps
// internal/backend free of any logging dependency (ADR-0013 decision 10).
//
// The values map 1:1 onto the circuit's loggable transitions:
//
//	CircuitOpened     Closed → Open (consecutive failures reached the threshold)
//	CircuitReopened   Half-Open → Open (the trial request failed)
//	CircuitClosed     Half-Open → Closed (the trial request succeeded)
//	CircuitHalfOpened Open → Half-Open (the cooldown elapsed; a trial is admitted)
//	CircuitNoChange   the call changed nothing
//
// CircuitOpened and CircuitReopened are distinct even though both leave the
// circuit Open, because they carry different reasons (consecutive_failures vs
// trial_failure) into the log line; a four-value enum reporting only the
// resulting state could not tell them apart. See ADR-0013's 2026-09-21
// amendment.
type CircuitTransition int

const (
	// CircuitNoChange means the call did not change the circuit's state.
	CircuitNoChange CircuitTransition = iota
	// CircuitOpened means the consecutive-failure threshold was reached while
	// Closed, opening the circuit.
	CircuitOpened
	// CircuitReopened means a Half-Open trial failed, reopening the circuit.
	CircuitReopened
	// CircuitClosed means a Half-Open trial succeeded, closing the circuit.
	CircuitClosed
	// CircuitHalfOpened means an Open circuit's cooldown elapsed and the call
	// admitted the trial.
	CircuitHalfOpened
)

// Backend represents one upstream server the load balancer can route to.
//
// Concurrency: healthy is written by the health checker (active probes and
// passive outlier detection, both Sprint 3) and read by every selector and
// the proxy on the hot path. active is incremented/decremented by the
// proxy around each round trip (S1.T6) and read by LeastConnections and
// metrics (Sprint 3). latencyEWMA is written by the proxy on every round
// trip (S2.T3) and read by PowerOfTwoChoicesEWMA. circuit is the whole
// circuit-breaker state (S3.T3), read and CAS-updated by every selector
// (via Registry.Selectable) and the proxy admission gate. All fields are
// unexported; callers MUST use
// IsHealthy/MarkHealthy/MarkUnhealthy/IncActive/DecActive/ActiveConns/
// RecordLatency/EWMALatency/CircuitOpen/CircuitAllow/CircuitSuccess/
// CircuitFailure and never touch the fields directly — this keeps the field
// representation free to change without touching balancer or proxy code.
// See docs/design/sprint-1-contracts.md "Concurrency ownership table",
// ADR-0010, ADR-0011, and ADR-0012.
type Backend struct {
	Name string
	URL  *url.URL

	healthy     atomic.Bool
	active      atomic.Int64
	latencyEWMA atomic.Int64
	circuit     atomic.Pointer[circuitSnapshot]
}

// IsHealthy reports whether the backend is currently eligible for
// selection. Implemented in S1.T3.
func (b *Backend) IsHealthy() bool {
	return b.healthy.Load()
}

// MarkHealthy makes the backend eligible for selection again. NewRegistry
// seeds every freshly-built backend with it; at runtime, by convention
// (ADR-0011 decision 2) only the active health-check subsystem calls it: a
// backend ejected by passive outlier detection recovers via the next
// successful active probe, never on a passive timer. Go cannot enforce caller
// identity — the same limitation ADR-0006 accepted for the single SetHealthy
// this method splits from. Symmetric with MarkUnhealthy/IncActive/DecActive
// living directly on Backend.
func (b *Backend) MarkHealthy() {
	b.healthy.Store(true)
}

// MarkUnhealthy makes the backend ineligible for selection. Both active
// health checks and passive outlier detection call this (ADR-0011 decisions
// 2 and 3). Owned by the health-check subsystem by convention: its
// per-backend goroutines call it directly on the *Backend they hold. No
// production caller exists yet; S1.T8's cross-selector tests drive health
// transitions with it until Sprint 3 wires the checkers.
func (b *Backend) MarkUnhealthy() {
	b.healthy.Store(false)
}

// IncActive increments the active connection count. Called by the proxy
// before dispatching a request. Implemented in S1.T3.
func (b *Backend) IncActive() {
	b.active.Add(1)
}

// DecActive decrements the active connection count. Called by the proxy
// once the response completes (success, error, or timeout). Implemented
// in S1.T3.
func (b *Backend) DecActive() {
	b.active.Add(-1)
}

// ActiveConns returns the current active connection count. Implemented in
// S1.T3.
func (b *Backend) ActiveConns() int64 {
	return b.active.Load()
}

// RecordLatency folds one observed round-trip duration into the backend's
// EWMA-smoothed latency estimate.
//
// Cold start: the first-ever call stores d directly rather than blending from
// a zero baseline, so a freshly-added or just-recovered backend is not made to
// look artificially fast by an unmeasured history. The zero value doubles as
// "no sample yet" — a real round trip cannot complete in exactly 0ns against
// Go's monotonic clock. A zero d is still a valid observation and blends on
// every call after the first. See ADR-0010.
//
// Concurrency: updated via a compare-and-swap retry loop, not a mutex, so the
// lock-free read path PowerOfTwoChoicesEWMA depends on stays lock-free. Any
// package holding a *Backend may call this; the proxy is the production
// writer.
func (b *Backend) RecordLatency(d time.Duration) {
	for {
		old := b.latencyEWMA.Load()
		var next int64
		if old == 0 {
			next = int64(d)
		} else {
			next = int64(ewmaAlpha*float64(d) + (1-ewmaAlpha)*float64(old))
		}
		if b.latencyEWMA.CompareAndSwap(old, next) {
			return
		}
	}
}

// EWMALatency returns the backend's current EWMA-smoothed round-trip latency
// estimate, or zero if no sample has been recorded. Select treats zero as
// "fastest possible" — an intentional, self-correcting bootstrap: a backend
// that has never been sampled wins its next comparison, receives a real
// sample, and stops reading zero. See ADR-0010.
func (b *Backend) EWMALatency() time.Duration {
	return time.Duration(b.latencyEWMA.Load())
}

// currentCircuit returns the backend's circuit state, lazily promoting an Open
// circuit whose cooldown has elapsed to Half-Open on the spot. The second
// return reports whether this call performed that promotion. There is no timer
// or goroutine: the promotion happens inside this read, so
// Registry.Selectable and the per-request admission gate observe the same
// transition (ADR-0011 decision 6). cooldown is passed in by internal/circuit
// so the tuning value stays in that package (ADR-0012 decision 4).
//
// The promotion flag exists so CircuitAllow can report CircuitHalfOpened when
// it is the call that promoted. A promotion won by CircuitOpen (the
// Registry.Selectable read path) is never logged — a documented, permanent gap
// (ADR-0013 decision 13).
func (b *Backend) currentCircuit(cooldown time.Duration) (circuitSnapshot, bool) {
	for {
		old := b.circuit.Load()
		cur := circuitSnapshot{}
		if old != nil {
			cur = *old
		}
		if cur.state != circuitOpen || time.Since(cur.openedAt) < cooldown {
			return cur, false
		}
		next := circuitSnapshot{state: circuitHalfOpen}
		if b.circuit.CompareAndSwap(old, &next) {
			return next, true
		}
	}
}

// CircuitOpen reports whether b's circuit is currently Open, performing the
// lazy Open→Half-Open promotion described on currentCircuit. A half-open
// circuit is not open, so a backend whose cooldown has elapsed is selectable
// again (ADR-0011 decision 1).
//
// This is the Registry.Selectable read path. It returns no transition and has
// no logger reachable from it, so an Open→Half-Open promotion it performs is
// never logged (ADR-0013 decision 13) — see CircuitAllow, which reports
// CircuitHalfOpened only when it is itself the call that promoted.
func (b *Backend) CircuitOpen(cooldown time.Duration) bool {
	cur, _ := b.currentCircuit(cooldown)
	return cur.state == circuitOpen
}

// CircuitAllow reports whether b may dispatch a request now: Closed admits,
// Open denies, and Half-Open admits exactly one request — the trial — by
// CompareAndSwap-ing the trial flag. Two requests landing in the same instant a
// circuit becomes half-open therefore cannot both believe they are the trial
// (ADR-0011 decision 6). cooldown is supplied by internal/circuit.
//
// The transition is CircuitHalfOpened only when this call both promoted the
// circuit from Open and won the trial slot, so exactly one caller logs the
// promotion. If a Registry.Selectable scan already promoted the circuit, this
// call merely takes the trial and reports CircuitNoChange: the promotion has no
// logger path and is never logged (ADR-0013 decision 13). Every other outcome
// reports CircuitNoChange.
func (b *Backend) CircuitAllow(cooldown time.Duration) (bool, CircuitTransition) {
	// Promote first so an open-but-cooled circuit is treated as half-open; the
	// promotion rule then lives in exactly one place (currentCircuit).
	_, promoted := b.currentCircuit(cooldown)
	for {
		old := b.circuit.Load()
		cur := circuitSnapshot{}
		if old != nil {
			cur = *old
		}
		switch cur.state {
		case circuitOpen:
			// Still open: the cooldown has not elapsed (or the circuit was
			// reopened concurrently).
			return false, CircuitNoChange
		case circuitHalfOpen:
			if cur.trial {
				return false, CircuitNoChange
			}
			next := cur
			next.trial = true
			if b.circuit.CompareAndSwap(old, &next) {
				if promoted {
					return true, CircuitHalfOpened
				}
				return true, CircuitNoChange
			}
		default:
			return true, CircuitNoChange
		}
	}
}

// CircuitSuccess records a successful round trip. From Closed it resets the
// consecutive-failure count to zero (ADR-0011 decision 8); from Half-Open it
// closes the circuit, resolving the trial with a single success and no
// threshold (ADR-0011 decision 6). An outcome observed while Open is ignored:
// a request admitted before the circuit opened can still be in flight, and
// letting its stale success close the circuit would bypass the cooldown.
//
// It returns CircuitClosed only for the Half-Open→Closed resolution; every
// other case (already Closed, or ignored while Open) is CircuitNoChange.
//
// Known limitation (ADR-0012 consequences): a request admitted while Closed
// whose response arrives while the circuit is Half-Open cannot be told apart
// from the trial, because the frozen three-argument RoundTripObserver carries
// no per-request trial marker; it may therefore resolve the trial early.
func (b *Backend) CircuitSuccess() CircuitTransition {
	for {
		old := b.circuit.Load()
		if old != nil && old.state == circuitOpen {
			return CircuitNoChange
		}
		closedFromTrial := old != nil && old.state == circuitHalfOpen
		next := circuitSnapshot{state: circuitClosed}
		if b.circuit.CompareAndSwap(old, &next) {
			if closedFromTrial {
				return CircuitClosed
			}
			return CircuitNoChange
		}
	}
}

// CircuitFailure records a failed round trip (a 5xx response or an errorHandler
// transport failure). From Closed it increments the consecutive-failure count
// and opens the circuit at threshold; from Half-Open it reopens immediately —
// the trial failed — with a fresh opened-at timestamp and no inner threshold
// (ADR-0011 decisions 6 and 8). An outcome observed while Open is ignored, so a
// stale in-flight failure cannot move the opened-at timestamp. The same
// stale-while-Half-Open limitation noted on CircuitSuccess applies here.
//
// It returns CircuitOpened for the Closed→Open threshold crossing and
// CircuitReopened for the Half-Open→Open trial failure — two transitions that
// both leave the circuit Open but carry different log reasons — and
// CircuitNoChange otherwise.
func (b *Backend) CircuitFailure(threshold int) CircuitTransition {
	for {
		old := b.circuit.Load()
		cur := circuitSnapshot{}
		if old != nil {
			cur = *old
		}
		switch cur.state {
		case circuitOpen:
			return CircuitNoChange
		case circuitHalfOpen:
			next := circuitSnapshot{state: circuitOpen, openedAt: time.Now()}
			if b.circuit.CompareAndSwap(old, &next) {
				return CircuitReopened
			}
		default:
			if int(cur.failures)+1 >= threshold {
				next := circuitSnapshot{state: circuitOpen, openedAt: time.Now()}
				if b.circuit.CompareAndSwap(old, &next) {
					return CircuitOpened
				}
				continue
			}
			next := cur
			next.failures++
			if b.circuit.CompareAndSwap(old, &next) {
				return CircuitNoChange
			}
		}
	}
}
