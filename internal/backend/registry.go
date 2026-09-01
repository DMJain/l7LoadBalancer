package backend

import "github.com/DMJain/l7LoadBalancer/internal/config"

// Registry holds the set of backends built from validated config and
// mediates concurrent read (selectors, proxy) and write (health checker,
// Sprint 3; config reload, Sprint 4) access.
//
// Sprint 1: immutable after construction. Sprint 4 adds hot-reload via
// atomic.Pointer swap. See docs/design/sprint-1-contracts.md.
type Registry struct {
	// fields decided in S1.T3
}

// NewRegistry builds a Registry from validated backend config. Implemented
// in S1.T3.
func NewRegistry(cfgs []config.BackendConfig) (*Registry, error) {
	panic("not implemented: S1.T3")
}

// All returns a fresh snapshot slice of every backend, regardless of
// health.
//
// returns fresh snapshot; see S1.T3
func (r *Registry) All() []*Backend {
	return nil
}

// Healthy returns a fresh snapshot slice of only the currently healthy
// backends.
//
// returns fresh snapshot; see S1.T3
func (r *Registry) Healthy() []*Backend {
	return nil
}
