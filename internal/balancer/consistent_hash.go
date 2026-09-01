package balancer

import (
	"context"
	"net/http"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// ConsistentHashBoundedLoads selects a backend via consistent hashing with
// bounded loads (Mirrokni-Thorup-Zadimoghaddam, 2016): per-backend load
// counters, rehashing when the first-choice backend is over capacity.
// Sprint 2 deliverable — see MILESTONES.md.
type ConsistentHashBoundedLoads struct {
	reg *backend.Registry
}

// NewConsistentHashBoundedLoads constructs the selector over reg. Sprint 2.
func NewConsistentHashBoundedLoads(reg *backend.Registry) *ConsistentHashBoundedLoads {
	panic("not implemented: Sprint 2 (consistent-hash bounded-loads, see MILESTONES.md)")
}

// Select implements Selector. Sprint 2.
func (s *ConsistentHashBoundedLoads) Select(ctx context.Context, r *http.Request) (*backend.Backend, error) {
	panic("not implemented: Sprint 2 (consistent-hash bounded-loads, see MILESTONES.md)")
}

var _ Selector = (*ConsistentHashBoundedLoads)(nil)
