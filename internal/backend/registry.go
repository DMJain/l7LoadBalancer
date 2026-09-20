package backend

import (
	"fmt"
	"net/url"
	"slices"

	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// CircuitGate is the circuit breaker's view of a backend, defined here at the
// consumer because internal/backend must not import internal/circuit (which
// imports backend). *circuit.Breaker implements it.
//
// Open answers pool eligibility — may b be selected at all — and performs the
// same lazy Open→Half-Open promotion Allow does, so Selectable and admission
// never disagree about whether a circuit is open. Allow answers dispatch
// admission for one specific request; in half-open it is what hands out the
// single trial, so Selectable must never call it (that would consume the
// trial). See ADR-0012 decision 1.
type CircuitGate interface {
	Open(b *Backend) bool
	Allow(b *Backend) bool
}

// Registry holds the set of backends built from validated config and
// mediates concurrent read (selectors, proxy) and write (health checker,
// Sprint 3; config reload, Sprint 4) access.
//
// Sprint 1: immutable after construction. Sprint 4 adds hot-reload via
// atomic.Pointer swap.
//
// Concurrency: the backend slice is fixed at construction. circuit is written
// once by SetCircuitGate before the registry is served and only read
// afterwards, so the request path needs no lock — the same contract as
// Proxy.RegisterObserver. See docs/design/sprint-1-contracts.md and ADR-0012.
type Registry struct {
	backends []*Backend
	circuit  CircuitGate
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

// SetCircuitGate installs g as the registry's circuit gate. It must be called
// once before the registry begins serving; the field is read without a lock on
// the hot path. A nil gate (the default) means no circuit filtering, so a bare
// NewRegistry behaves exactly as it did before circuits existed.
func (r *Registry) SetCircuitGate(g CircuitGate) {
	r.circuit = g
}

// Selectable returns a fresh snapshot slice of backends eligible for routing
// right now — healthy and not circuit-open — preserving registry order. A
// half-open backend is included: only its circuit's Allow gate decides whether
// a given request becomes the trial (ADR-0011 decision 1, ADR-0012).
//
// The set is captured at call time; concurrent health or circuit transitions
// between this call and the next are expected and need no cross-call
// consistency (see S1.T4's modulo indexing). Renamed from Healthy in S3.T3:
// the old name would have quietly meant something narrower than what it
// filters on once circuit state existed.
func (r *Registry) Selectable() []*Backend {
	selectable := make([]*Backend, 0, len(r.backends))
	for _, b := range r.backends {
		if b.IsHealthy() && (r.circuit == nil || !r.circuit.Open(b)) {
			selectable = append(selectable, b)
		}
	}
	return selectable
}

// Allow reports whether b may dispatch a request now. It delegates to the
// installed circuit gate, performing the same lazy promotion Selectable does;
// with no gate installed, admission is unconditional. The proxy calls this
// after Select and before IncActive (ADR-0011 decision 7).
func (r *Registry) Allow(b *Backend) bool {
	if r.circuit == nil {
		return true
	}
	return r.circuit.Allow(b)
}
