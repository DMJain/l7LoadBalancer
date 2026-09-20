package circuit

import (
	"time"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// circuitFailuresBeforeOpen is the consecutive-failure-to-open threshold. An
// unexported Go constant, not a config field (ADR-0011 decision 10), matching
// the active checker's three consecutive probe failures: the smallest count
// that reads as a trend rather than one bad request. See ADR-0012 decision 6.
const circuitFailuresBeforeOpen = 3

// Breaker is the per-backend circuit breaker's policy. The state itself lives
// on *backend.Backend as one CAS-guarded snapshot (ADR-0011 decision 5); this
// type holds only the tunables and routes calls to the right Backend method.
// It imports internal/backend and never the reverse, so the package graph
// stays acyclic — and because it depends only on backend, it can satisfy
// proxy.RoundTripObserver structurally without importing proxy (ADR-0012).
//
// Breaker implements backend.CircuitGate (Open/Allow) as well as the observer
// contract: main installs it as the registry's gate and registers it as a
// proxy round-trip observer.
type Breaker struct {
	cooldown time.Duration
}

// New builds a Breaker with the configured cooldown: how long a tripped circuit
// stays Open before a read may lazily promote it to Half-Open. The cooldown is
// read from Config by main (ADR-0011 decision 10), so it is passed in rather
// than imported here.
func New(cooldown time.Duration) *Breaker {
	return &Breaker{cooldown: cooldown}
}

// Open reports whether b's circuit is open, performing the lazy Open→Half-Open
// promotion. It is the gate's pool-eligibility question and never takes the
// half-open trial (ADR-0012 decision 1).
func (br *Breaker) Open(b *backend.Backend) bool {
	return b.CircuitOpen(br.cooldown)
}

// Allow reports whether b may dispatch a request now, taking the half-open
// trial when that is the circuit's current state. It is the proxy's
// per-request admission gate.
func (br *Breaker) Allow(b *backend.Backend) bool {
	return b.CircuitAllow(br.cooldown)
}

// ObserveRoundTrip folds one round trip's outcome into b's circuit. The failure
// signal is the observer contract's success flag: false is the union of a 5xx
// response and an errorHandler transport failure, the same signal passive
// outlier detection watches (ADR-0011 decisions 8 and 12). It is called
// unconditionally for every round trip; recording is unconditional, gating is
// conditional (ADR-0011 decision 9).
func (br *Breaker) ObserveRoundTrip(b *backend.Backend, _ time.Duration, success bool) {
	if success {
		b.CircuitSuccess()
		return
	}
	b.CircuitFailure(circuitFailuresBeforeOpen)
}

var (
	_ backend.CircuitGate = (*Breaker)(nil)

	// Breaker satisfies proxy.RoundTripObserver structurally. The assertion
	// cannot be written here without importing internal/proxy, which would add
	// an edge the package graph does not want; main's registration of it is the
	// compile-time check.
	_ interface {
		ObserveRoundTrip(*backend.Backend, time.Duration, bool)
	} = (*Breaker)(nil)
)
