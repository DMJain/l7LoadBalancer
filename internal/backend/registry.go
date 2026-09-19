package backend

import (
	"fmt"
	"net/url"
	"slices"

	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// Registry holds the set of backends built from validated config and
// mediates concurrent read (selectors, proxy) and write (health checker,
// Sprint 3; config reload, Sprint 4) access.
//
// Sprint 1: immutable after construction. Sprint 4 adds hot-reload via
// atomic.Pointer swap. See docs/design/sprint-1-contracts.md.
type Registry struct {
	backends []*Backend
}

// NewRegistry builds a Registry from validated backend config. It parses
// each BackendConfig.URL into a *url.URL and starts every backend healthy:
// there is no health checker yet to set it any other way, and Sprint 1's
// exit criteria requires all configured backends reachable from the start.
// Sprint 3's health checker takes over health state from here.
//
// The registry holds an ordered slice (no name-indexed map) — preserving
// config order, which LeastConnections relies on for its deterministic
// tie-break. Nothing through Sprint 3 needs O(1) lookup by name; Sprint 4's
// reload-diffing will add it if required.
func NewRegistry(cfgs []config.BackendConfig) (*Registry, error) {
	backends := make([]*Backend, 0, len(cfgs))
	for _, cfg := range cfgs {
		u, err := url.Parse(cfg.URL)
		if err != nil {
			return nil, fmt.Errorf("backend: parse url %q for %q: %w", cfg.URL, cfg.Name, err)
		}
		b := &Backend{Name: cfg.Name, URL: u}
		b.MarkHealthy()
		backends = append(backends, b)
	}
	return &Registry{backends: backends}, nil
}

// All returns a fresh snapshot slice of every backend, regardless of
// health, preserving registry order. The caller may mutate the returned
// slice without affecting the registry; the backends themselves are shared.
func (r *Registry) All() []*Backend {
	return slices.Clone(r.backends)
}

// Healthy returns a fresh snapshot slice of only the currently healthy
// backends, preserving registry order. The set is captured at call time;
// concurrent health transitions between this call and the next are expected
// and need no cross-call consistency (see S1.T4's modulo indexing).
func (r *Registry) Healthy() []*Backend {
	healthy := make([]*Backend, 0, len(r.backends))
	for _, b := range r.backends {
		if b.IsHealthy() {
			healthy = append(healthy, b)
		}
	}
	return healthy
}
