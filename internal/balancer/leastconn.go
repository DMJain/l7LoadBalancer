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
type LeastConnections struct {
	reg *backend.Registry
}

// NewLeastConnections constructs a LeastConnections selector over reg.
func NewLeastConnections(reg *backend.Registry) *LeastConnections {
	return &LeastConnections{reg: reg}
}

// Select implements Selector. It linear-scans a snapshot of the healthy set,
// returning the backend with the lowest ActiveConns, and ErrNoHealthyBackends
// when that set is empty. Replacement happens only on a strictly lower count,
// so the first backend in registry order wins ties — deterministic and
// reproducible. ActiveConns is read, never mutated: connection bookkeeping is
// the proxy's job (S1.T6).
func (s *LeastConnections) Select(ctx context.Context, r *http.Request) (*backend.Backend, error) {
	healthy := s.reg.Healthy()
	if len(healthy) == 0 {
		return nil, ErrNoHealthyBackends
	}

	best := healthy[0]
	bestActive := best.ActiveConns()
	for _, b := range healthy[1:] {
		if active := b.ActiveConns(); active < bestActive {
			best = b
			bestActive = active
		}
	}
	return best, nil
}

var _ Selector = (*LeastConnections)(nil)
