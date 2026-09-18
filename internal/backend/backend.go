package backend

import (
	"net/url"
	"sync/atomic"
)

// Backend represents one upstream server the load balancer can route to.
//
// Concurrency: healthy is written by the health checker (active probes and
// passive outlier detection, both Sprint 3) and read by every selector and
// the proxy on the hot path. active is incremented/decremented by the
// proxy around each round trip (S1.T6) and read by LeastConnections and
// metrics (Sprint 3). Both fields are unexported atomics; callers MUST use
// IsHealthy/IncActive/DecActive/ActiveConns and never touch the fields
// directly — this keeps the field type free to change (e.g. atomic.Bool
// to a state enum in Sprint 3) without touching balancer or proxy code.
// See docs/design/sprint-1-contracts.md "Concurrency ownership table".
type Backend struct {
	Name string
	URL  *url.URL

	healthy atomic.Bool
	active  atomic.Int64
}

// IsHealthy reports whether the backend is currently eligible for
// selection. Implemented in S1.T3.
func (b *Backend) IsHealthy() bool {
	panic("not implemented: S1.T3")
}

// SetHealthy sets whether the backend is currently eligible for selection.
// Owned by the health-check subsystem by convention (ADR-0006): Sprint 3's
// per-backend health-check goroutines call it directly on the *Backend they
// hold. Sprint 1 has no production caller; S1.T8's cross-selector tests use
// it to drive health transitions. Implemented in S1.T3.
func (b *Backend) SetHealthy(healthy bool) {
	panic("not implemented: S1.T3")
}

// IncActive increments the active connection count. Called by the proxy
// before dispatching a request. Implemented in S1.T3.
func (b *Backend) IncActive() {
	panic("not implemented: S1.T3")
}

// DecActive decrements the active connection count. Called by the proxy
// once the response completes (success, error, or timeout). Implemented
// in S1.T3.
func (b *Backend) DecActive() {
	panic("not implemented: S1.T3")
}

// ActiveConns returns the current active connection count. Implemented in
// S1.T3.
func (b *Backend) ActiveConns() int64 {
	panic("not implemented: S1.T3")
}
