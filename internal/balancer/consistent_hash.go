package balancer

import (
	"context"
	"math"
	"net/http"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// capacityEpsilon is ε in the bounded-loads capacity formula: a candidate is
// admitted only while its ActiveConns is within (1 + ε) of the mean across
// selectable backends. 0.25 is the value cited in
// Mirrokni-Thorup-Zadimoghaddam (2016) and is a Go constant, not a config
// field — no operator has asked to tune it. See ADR-0009.
const capacityEpsilon = 0.25

// ConsistentHashBoundedLoads is the operator-facing consistent_hash selector:
// session-sticky routing by client IP with a per-backend load ceiling. It
// hashes the request's client address onto the shared immutable ring, then
// walks candidates from that position, admitting the first that is both
// healthy and within capacity (ActiveConns <= capacity). Capacity is
// max(1, ceil(avg_active * (1 + ε))) where avg_active is the mean ActiveConns
// over currently-selectable backends (healthy and circuit-not-open).
//
// It gives the affinity of naive consistent hashing without its failure mode:
// a hot key whose ring position lands on one backend is rehashed to the next
// candidate once that backend reaches its share of the load, so no backend is
// overwhelmed just because it owns a popular key. See CONTEXT.md ("Load",
// "Capacity") and ADR-0009.
//
// When no selectable backend exists it returns ErrNoHealthyBackends, the
// single condition every selector in this package uses — never a distinct
// "over capacity" error, because the capacity floor and the selectable-set
// average guarantee a healthy candidate is always admissible.
//
// Concurrency: the ring is immutable after construction and the only mutable
// state it reads lives on Backend behind atomics (IsHealthy, ActiveConns).
// The type holds no state of its own, so one instance may serve concurrent
// requests.
type ConsistentHashBoundedLoads struct {
	reg  *backend.Registry
	ring *ring
}

// NewConsistentHashBoundedLoads constructs the selector over reg. The ring is
// built once from every backend reg holds (Registry.All()), regardless of
// health; a health flip never rebuilds it.
func NewConsistentHashBoundedLoads(reg *backend.Registry) *ConsistentHashBoundedLoads {
	return &ConsistentHashBoundedLoads{reg: reg, ring: newRing(reg.All())}
}

// Select implements Selector. It computes the current capacity across
// selectable backends once per call, then walks the ring from the request's
// hash key and returns the first candidate that is healthy and not over
// capacity. It never mutates ActiveConns — like LeastConnections, it only
// reads it; connection bookkeeping is the proxy's job.
//
// The walk is the ring's full candidate iterator: it yields each distinct
// backend once and terminates, so the traversal is inherently capped at one
// full pass. If — counter to the bounded-loads invariant — every healthy
// candidate were nevertheless over capacity, it returns the least-loaded
// healthy candidate seen rather than looping or returning an error. That
// branch is defensive and unreachable with a non-empty selectable set: the
// mean of the selectable set is always <= ceil(mean * 1.25), so at least the
// least-loaded healthy backend is admissible, and the walk always reaches it.
func (s *ConsistentHashBoundedLoads) Select(ctx context.Context, r *http.Request) (*backend.Backend, error) {
	selectable := s.reg.Selectable()
	if len(selectable) == 0 {
		return nil, ErrNoHealthyBackends
	}
	capacity := capacityFor(selectable)

	var (
		leastLoaded       *backend.Backend
		leastLoadedActive int64
	)
	for b := range s.ring.candidates(requestHashKey(r)) {
		if s.admits(b, capacity) {
			return b, nil
		}
		if b.IsHealthy() && (leastLoaded == nil || b.ActiveConns() < leastLoadedActive) {
			leastLoaded, leastLoadedActive = b, b.ActiveConns()
		}
	}

	// Defensive: unreachable with a non-empty healthy set (capacityFor's mean
	// is itself within capacity, so the least-loaded healthy backend is always
	// admissible and the walk reaches it). It exists so a violated invariant
	// degrades to a bounded choice rather than a nil return.
	if leastLoaded != nil {
		return leastLoaded, nil
	}
	return nil, ErrNoHealthyBackends
}

// admits reports whether b may be selected at the given capacity: it must be
// healthy and within capacity. Select and the offline reproducer's
// fallback check share this predicate, so the evidence cannot drift from the
// behavior it measures.
func (s *ConsistentHashBoundedLoads) admits(b *backend.Backend, capacity int64) bool {
	return b.IsHealthy() && b.ActiveConns() <= capacity
}

// capacityFor returns the admission ceiling for one Select call:
// max(1, ceil(avg_active * (1 + capacityEpsilon))), averaged over the
// selectable set. The floor of 1 keeps an idle system (avg_active = 0) from
// rejecting every backend's first request against a zero-sized cap.
func capacityFor(selectable []*backend.Backend) int64 {
	var total int64
	for _, b := range selectable {
		total += b.ActiveConns()
	}
	avg := float64(total) / float64(len(selectable))
	capacity := int64(math.Ceil(avg * (1 + capacityEpsilon)))
	if capacity < 1 {
		return 1
	}
	return capacity
}

var _ Selector = (*ConsistentHashBoundedLoads)(nil)
