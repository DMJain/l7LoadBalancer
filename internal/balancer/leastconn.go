package balancer

import (
	"context"
	"net/http"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// LeastConnections selects the healthy backend with the lowest current
// ActiveConns, reading it atomically. It does not itself mutate
// ActiveConns — mutation is the proxy's job (S1.T6). Ties are broken
// deterministically: the first tied backend in registry order.
// Implemented in S1.T5.
type LeastConnections struct {
	reg *backend.Registry
}

// NewLeastConnections constructs a LeastConnections selector over reg.
// Implemented in S1.T5.
func NewLeastConnections(reg *backend.Registry) *LeastConnections {
	panic("not implemented: S1.T5")
}

// Select implements Selector. Returns ErrNoHealthyBackends when
// reg.Healthy() is empty. Implemented in S1.T5.
func (s *LeastConnections) Select(ctx context.Context, r *http.Request) (*backend.Backend, error) {
	panic("not implemented: S1.T5")
}

var _ Selector = (*LeastConnections)(nil)
