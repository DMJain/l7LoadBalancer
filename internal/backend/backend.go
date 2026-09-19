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

// Backend represents one upstream server the load balancer can route to.
//
// Concurrency: healthy is written by the health checker (active probes and
// passive outlier detection, both Sprint 3) and read by every selector and
// the proxy on the hot path. active is incremented/decremented by the
// proxy around each round trip (S1.T6) and read by LeastConnections and
// metrics (Sprint 3). latencyEWMA is written by the proxy on every round
// trip (S2.T3) and read by PowerOfTwoChoicesEWMA. All three fields are
// unexported atomics; callers MUST use
// IsHealthy/MarkHealthy/MarkUnhealthy/IncActive/DecActive/ActiveConns/
// RecordLatency/EWMALatency and never touch the fields directly — this keeps
// the field type free to change (e.g. atomic.Bool to a state enum in Sprint 3)
// without touching balancer or proxy code.
// See docs/design/sprint-1-contracts.md "Concurrency ownership table",
// ADR-0010, and ADR-0011.
type Backend struct {
	Name string
	URL  *url.URL

	healthy     atomic.Bool
	active      atomic.Int64
	latencyEWMA atomic.Int64
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
