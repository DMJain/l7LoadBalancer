package backend

import (
	"fmt"
	"net/url"
	"slices"
	"sync/atomic"

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

// registrySnapshot is the registry's whole backend set as one immutable value:
// a monotonically increasing version plus the ordered backend slice. Bundling
// the two means a reader gets a version and a backend set that cannot disagree,
// which is what lets the consistent-hash selector build a ring from the exact
// set its cached version names (ADR-0015 decisions 5 and 9). A snapshot is
// never mutated after it is published; a new one is built and swapped in whole.
type registrySnapshot struct {
	version  uint64
	backends []*Backend
}

// Registry holds the set of backends built from validated config and mediates
// concurrent read (selectors, proxy) and write (health checker, Sprint 3;
// config reload, Sprint 4) access.
//
// Concurrency: the backend set is one immutable snapshot behind an
// atomic.Pointer, replaced whole by Apply. All readers — All, Selectable,
// Version, Snapshot — load the pointer once and never lock, so a swap is
// invisible to a reader that already loaded the old snapshot. Apply is the
// single writer, called by one goroutine at a time (the reload loop). The
// circuit gate is written once by SetCircuitGate before the registry is served
// and only read afterwards, the same contract as Proxy.RegisterObserver. See
// docs/design/sprint-1-contracts.md and ADR-0012, ADR-0015.
type Registry struct {
	snap    atomic.Pointer[registrySnapshot]
	circuit CircuitGate
}

// NewRegistry builds a Registry from validated backend config. It parses each
// BackendConfig.URL into a *url.URL and starts every backend healthy: at
// process startup there is no previously-serving fleet to protect and the
// process must come up serving, so it trusts the operator's file and lets the
// active checker correct it (ADR-0015 decision 10). The initial snapshot's
// version is 1; every later Apply increments it.
//
// The registry preserves config order, which LeastConnections relies on for
// its deterministic tie-break and the file order dictates after a reload. It
// holds no name-indexed map: Apply builds a per-call name map from the current
// snapshot, which is off the request path.
func NewRegistry(cfgs []config.BackendConfig) (*Registry, error) {
	backends := make([]*Backend, 0, len(cfgs))
	for _, cfg := range cfgs {
		b, err := newBackend(cfg)
		if err != nil {
			return nil, err
		}
		backends = append(backends, b)
	}
	r := &Registry{}
	r.snap.Store(&registrySnapshot{version: 1, backends: backends})
	return r, nil
}

// newBackend parses one validated backend config into a fresh, healthy Backend
// (see NewRegistry for why healthy is the construction default). It is shared
// by NewRegistry and Apply, so a reload-added backend is built exactly like a
// startup backend.
func newBackend(cfg config.BackendConfig) (*Backend, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("backend: parse url %q for %q: %w", cfg.URL, cfg.Name, err)
	}
	b := &Backend{Name: cfg.Name, URL: u}
	b.MarkHealthy()
	return b, nil
}

// All returns a fresh snapshot slice of every backend in the current snapshot,
// regardless of health, preserving registry order. The caller may mutate the
// returned slice without affecting the registry; the backends themselves are
// shared.
func (r *Registry) All() []*Backend {
	return slices.Clone(r.snap.Load().backends)
}

// Version returns the current snapshot's version. It is a monotonically
// increasing counter, bumped by every Apply, that the consistent-hash selector
// keys its ring cache on. Callers that need a version and its backend set to
// agree must use Snapshot, not Version followed by All.
func (r *Registry) Version() uint64 {
	return r.snap.Load().version
}

// Snapshot returns the current snapshot's version and a fresh slice of every
// backend in it, both from a single atomic load, so the two cannot disagree.
// It is the read the consistent-hash selector uses to decide whether its
// cached ring is stale (ADR-0015 decision 9).
func (r *Registry) Snapshot() (uint64, []*Backend) {
	snap := r.snap.Load()
	return snap.version, slices.Clone(snap.backends)
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
	snap := r.snap.Load()
	selectable := make([]*Backend, 0, len(snap.backends))
	for _, b := range snap.backends {
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

// Apply atomically replaces the registry's backend set with the one the reload
// diff describes, while traffic flows, and returns the freshly-constructed
// added instances and the retired removed ones. It is the single writer; every
// reader loads the snapshot pointer once and never locks, so a swap is atomic
// to readers (ADR-0015 decisions 5–6).
//
// diff classifies every backend by identity (name, URL): an identity in
// diff.Added gets a fresh instance; one in diff.Unchanged keeps its existing
// instance and all its state. newBackends supplies the new file's order — the
// diff's separate Added/Unchanged slices cannot express how the two interleave
// — and Apply walks it in order, so the new file dictates round-robin rotation
// and LeastConnections' tie-break (ADR-0015 decision 3). It is a programming
// error for a newBackends entry to be in neither set, and Apply reports it
// rather than guess.
//
// Each removed backend is marked removed before the snapshot is swapped, so a
// request selected just before the swap already sees the flag when it completes
// (ADR-0015 decision 7). Removed backends then leave All and Selectable at the
// swap. A re-added identity is always fresh: it is in diff.Added, so it never
// matches an unchanged entry and never inherits the old instance's state
// (ADR-0015 decision 6).
//
// The errors are an unparseable URL and an inconsistent diff (a newBackends
// entry in neither Added nor Unchanged, or an unchanged entry absent from the
// current snapshot); config.Validate rules the first out for production
// callers, and DiffBackends rules the rest out. They are returned rather than
// panicked or silently mis-applied. On error nothing is marked or swapped: the
// fresh instances built so far are discarded.
func (r *Registry) Apply(diff config.BackendDiff, newBackends []config.BackendConfig) (added, removed []*Backend, err error) {
	cur := r.snap.Load()

	byName := make(map[string]*Backend, len(cur.backends))
	for _, b := range cur.backends {
		byName[b.Name] = b
	}
	addedIdentities := identities(diff.Added)
	unchangedIdentities := identities(diff.Unchanged)

	next := make([]*Backend, 0, len(newBackends))
	for _, cfg := range newBackends {
		id := config.BackendIdentity(cfg)
		switch {
		case inSet(addedIdentities, id):
			b, err := newBackend(cfg)
			if err != nil {
				return nil, nil, err
			}
			next = append(next, b)
			added = append(added, b)
		case inSet(unchangedIdentities, id):
			b := byName[cfg.Name]
			if b == nil {
				return nil, nil, fmt.Errorf("backend: apply: unchanged backend %q not in current snapshot", cfg.Name)
			}
			next = append(next, b)
		default:
			return nil, nil, fmt.Errorf("backend: apply: backend %q (%s) is neither added nor unchanged in the diff", cfg.Name, cfg.URL)
		}
	}

	for _, cfg := range diff.Removed {
		b := byName[cfg.Name]
		if b == nil {
			continue
		}
		b.markRemoved()
		removed = append(removed, b)
	}

	r.snap.Store(&registrySnapshot{version: cur.version + 1, backends: next})
	return added, removed, nil
}

// identities builds the identity set of a backend config slice.
func identities(cfgs []config.BackendConfig) map[string]struct{} {
	ids := make(map[string]struct{}, len(cfgs))
	for _, cfg := range cfgs {
		ids[config.BackendIdentity(cfg)] = struct{}{}
	}
	return ids
}

// inSet reports whether id is in ids.
func inSet(ids map[string]struct{}, id string) bool {
	_, ok := ids[id]
	return ok
}
