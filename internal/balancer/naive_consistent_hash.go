package balancer

import (
	"context"
	"net/http"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// naiveConsistentHash is a health-aware but load-blind Selector: it hashes the
// request's client address to a ring position and returns the first healthy
// backend walking the ring from there. It gives session affinity, but because
// it has no notion of backend load, a hot key can still concentrate traffic on
// whichever backend owns that key's position — the failure mode
// ConsistentHashBoundedLoads exists to bound.
//
// It is deliberately never reachable through configuration: it appears in
// neither NewFromConfig's switch nor config's implementedAlgorithms, and the
// type is unexported. It exists only as the real, directly-testable comparator
// for the bounded-loads decision — so that decision is measured against a
// working naive implementation rather than argued from a citation. ADR-0008
// records the ring it shares and the comparator's role in the Sprint 2 design;
// the bounded-loads evidence lives with ConsistentHashBoundedLoads.
//
// It is intentionally not named ConsistentHash. A reader who meets the
// consistent_hash config identifier would take a type of that name to be what
// it maps to, and it maps to ConsistentHashBoundedLoads. Keeping this type's
// name outside that vocabulary keeps its unwired status obvious.
//
// Concurrency: the ring is immutable after construction and this type holds no
// mutable state of its own, so one instance may serve concurrent requests.
type naiveConsistentHash struct {
	ring *ring
}

// newNaiveConsistentHash builds the selector over every backend reg holds.
// Like every consistent-hash selector, it builds its ring once from
// Registry.All() regardless of health; a health flip never rebuilds the ring.
func newNaiveConsistentHash(reg *backend.Registry) *naiveConsistentHash {
	return &naiveConsistentHash{ring: newRing(reg.All())}
}

// Select implements Selector. It walks the ring from the client's hash key,
// returning the first healthy candidate, and ErrNoHealthyBackends when the
// walk exhausts every backend without finding one. The ring is placement-only;
// the health check here is this selector's entire eligibility rule.
func (s *naiveConsistentHash) Select(ctx context.Context, r *http.Request) (*backend.Backend, error) {
	for b := range s.ring.candidates(requestHashKey(r)) {
		if b.IsHealthy() {
			return b, nil
		}
	}
	return nil, ErrNoHealthyBackends
}

var _ Selector = (*naiveConsistentHash)(nil)
