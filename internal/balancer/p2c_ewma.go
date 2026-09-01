package balancer

import (
	"context"
	"net/http"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// PowerOfTwoChoicesEWMA picks two random healthy backends and selects the
// one with the lower EWMA-tracked latency, updated atomically on each
// response. Sprint 2 deliverable — see MILESTONES.md.
type PowerOfTwoChoicesEWMA struct {
	reg *backend.Registry
}

// NewPowerOfTwoChoicesEWMA constructs the selector over reg. Sprint 2.
func NewPowerOfTwoChoicesEWMA(reg *backend.Registry) *PowerOfTwoChoicesEWMA {
	panic("not implemented: Sprint 2 (P2C+EWMA, see MILESTONES.md)")
}

// Select implements Selector. Sprint 2.
func (s *PowerOfTwoChoicesEWMA) Select(ctx context.Context, r *http.Request) (*backend.Backend, error) {
	panic("not implemented: Sprint 2 (P2C+EWMA, see MILESTONES.md)")
}

var _ Selector = (*PowerOfTwoChoicesEWMA)(nil)
