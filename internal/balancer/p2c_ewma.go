package balancer

import (
	"context"
	"math/rand/v2"
	"net/http"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// PowerOfTwoChoicesEWMA routes each request to whichever of two randomly
// sampled healthy backends currently has the lower EWMA-tracked latency. It
// fills the gap LeastConnections and ConsistentHashBoundedLoads cannot see: a
// backend that is healthy and accepting connections but simply slow. Two
// random samples suffice to achieve exponentially better load balance than a
// single random choice (Mitzenmacher, 2001). See ADR-0010.
//
// Concurrency: the type holds no mutable state of its own — the only mutable
// state it reads lives on Backend behind atomics (IsHealthy, EWMALatency) — so
// one instance may serve concurrent requests. The two-backend draw uses
// math/rand/v2's package-level functions rather than a per-selector *rand.Rand,
// whose non-concurrency-safe state would need a mutex and undercut the
// hot-path lock-freedom.
type PowerOfTwoChoicesEWMA struct {
	reg *backend.Registry
}

// NewPowerOfTwoChoicesEWMA constructs the selector over reg.
func NewPowerOfTwoChoicesEWMA(reg *backend.Registry) *PowerOfTwoChoicesEWMA {
	return &PowerOfTwoChoicesEWMA{reg: reg}
}

// Select implements Selector. It snapshots the healthy set and returns
// ErrNoHealthyBackends when it is empty. With exactly one healthy backend it
// returns that backend directly — there is nothing to compare against, so a
// random draw would be theater, not policy. With two or more it draws two
// distinct indices and returns the one with the lower EWMALatency.
//
// A tie is broken arbitrarily: unlike LeastConnections, no deterministic
// tie-break is promised or needed, because a tie between EWMA values derived
// from two different backends' floating-point histories is not a
// reproducible-by-design scenario the way registry-order ties are.
func (s *PowerOfTwoChoicesEWMA) Select(ctx context.Context, r *http.Request) (*backend.Backend, error) {
	healthy := s.reg.Healthy()
	switch len(healthy) {
	case 0:
		return nil, ErrNoHealthyBackends
	case 1:
		return healthy[0], nil
	}

	i := rand.IntN(len(healthy))
	j := rand.IntN(len(healthy) - 1)
	if j >= i {
		j++
	}
	if healthy[i].EWMALatency() <= healthy[j].EWMALatency() {
		return healthy[i], nil
	}
	return healthy[j], nil
}

var _ Selector = (*PowerOfTwoChoicesEWMA)(nil)
