package balancer

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// RoundRobin selects backends in rotating order across the selectable set
// (healthy and circuit-not-open), using an atomic counter (no mutex) for
// rotation.
//
// Concurrency: counter is written only by Select — one atomic increment
// per call — and never read externally. A single increment needs no mutual
// exclusion: racing increments each get a distinct value, so every call
// receives a distinct round-robin turn, and selection can never deadlock or
// block. See docs/design/sprint-1-contracts.md "Concurrency ownership
// table".
type RoundRobin struct {
	reg     *backend.Registry
	counter atomic.Uint64
}

// NewRoundRobin constructs a RoundRobin selector over reg.
func NewRoundRobin(reg *backend.Registry) *RoundRobin {
	return &RoundRobin{reg: reg}
}

// Select implements Selector. It snapshots the selectable set, returns
// ErrNoHealthyBackends when that set is empty, and otherwise indexes into
// the snapshot with the next counter value. Because Selectable() returns a
// fresh snapshot per call, indexing modulo the current length is correct
// even as the selectable set changes size between calls — no cross-call
// consistency is needed.
func (s *RoundRobin) Select(ctx context.Context, r *http.Request) (*backend.Backend, error) {
	selectable := s.reg.Selectable()
	if len(selectable) == 0 {
		return nil, ErrNoHealthyBackends
	}
	next := s.counter.Add(1) - 1
	return selectable[next%uint64(len(selectable))], nil
}

var _ Selector = (*RoundRobin)(nil)
