package balancer

import (
	"context"
	"net/http"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// RoundRobin selects backends in rotating order across the healthy set,
// using an atomic counter (no mutex) for rotation. Implemented in S1.T4.
type RoundRobin struct {
	reg *backend.Registry
}

// NewRoundRobin constructs a RoundRobin selector over reg. Implemented in
// S1.T4.
func NewRoundRobin(reg *backend.Registry) *RoundRobin {
	panic("not implemented: S1.T4")
}

// Select implements Selector. Returns ErrNoHealthyBackends when
// reg.Healthy() is empty. Implemented in S1.T4.
func (s *RoundRobin) Select(ctx context.Context, r *http.Request) (*backend.Backend, error) {
	panic("not implemented: S1.T4")
}

var _ Selector = (*RoundRobin)(nil)
