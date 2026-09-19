package balancer

import (
	"math/rand"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// naiveConsistentHash routes by client IP, so unlike selectName (which uses
// httptest's fixed default RemoteAddr) these tests must control RemoteAddr to
// exercise different clients and different ephemeral ports. The address-aware
// helpers selectForAddr / selectNameForAddr live in selector_test.go, the
// cross-cutting test file, so every selector test builds requests through one
// path.

// TestNaiveConsistentHashStableAffinity is the core session-affinity property:
// a given client address always maps to the same backend, across repeated
// calls and across two independently-built selectors over equivalent
// registries (so affinity depends on backend names, not pointer identity).
func TestNaiveConsistentHashStableAffinity(t *testing.T) {
	const repeats = 20

	tests := []struct {
		name       string
		remoteAddr string
	}{
		{name: "ipv4", remoteAddr: "203.0.113.7:54321"},
		{name: "same host, different port", remoteAddr: "203.0.113.7:9999"},
		{name: "ipv6", remoteAddr: "[2001:db8::1]:443"},
		{name: "other ipv4", remoteAddr: "198.51.100.23:1234"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel := newNaiveConsistentHash(newTestRegistry(t))
			again := newNaiveConsistentHash(newTestRegistry(t))

			want := selectNameForAddr(t, sel, tt.remoteAddr)
			for i := 0; i < repeats; i++ {
				assert.Equalf(t, want, selectNameForAddr(t, sel, tt.remoteAddr),
					"same client IP changed backend on selection %d", i+1)
			}
			assert.Equalf(t, want, selectNameForAddr(t, again, tt.remoteAddr),
				"a second, identically-built selector disagreed")
		})
	}
}

// TestNaiveConsistentHashStripsPort proves the port is not part of the hash
// key: one client, two ephemeral ports, one backend. Without stripping, each
// connection would hash differently and affinity would be lost.
func TestNaiveConsistentHashStripsPort(t *testing.T) {
	sel := newNaiveConsistentHash(newTestRegistry(t))

	first := selectNameForAddr(t, sel, "203.0.113.7:5000")
	second := selectNameForAddr(t, sel, "203.0.113.7:6000")

	assert.Equal(t, first, second,
		"the same client on a different source port must stick to one backend")
}

// TestNaiveConsistentHashRespectsHealthTransitions scripts a target backend
// going unhealthy and recovering mid-run. The walk skips the unhealthy target
// and resumes choosing it once it is healthy again — the same health
// discipline every other selector exhibits.
func TestNaiveConsistentHashRespectsHealthTransitions(t *testing.T) {
	const client = "203.0.113.7:54321"

	reg := newTestRegistry(t)
	sel := newNaiveConsistentHash(reg)

	target, err := selectForAddr(t, sel, client)
	require.NoError(t, err)
	require.NotNil(t, target)

	t.Run("healthy baseline chooses target", func(t *testing.T) {
		assert.Equal(t, target.Name, selectNameForAddr(t, sel, client))
	})

	t.Run("unhealthy mid-run stops choosing target", func(t *testing.T) {
		target.MarkUnhealthy()
		for i := 0; i < 5; i++ {
			next, err := selectForAddr(t, sel, client)
			require.NoError(t, err)
			require.NotNil(t, next)
			assert.NotEqualf(t, target.Name, next.Name,
				"unhealthy target chosen on selection %d", i+1)
		}
	})

	t.Run("recovered resumes choosing target", func(t *testing.T) {
		target.MarkHealthy()
		assert.Equal(t, target.Name, selectNameForAddr(t, sel, client),
			"target not chosen again after recovery")
	})
}

// TestNaiveConsistentHashNoHealthyBackends covers both ways the healthy set
// can be empty: a registry with no backends at all, and every backend
// unhealthy. Both must return ErrNoHealthyBackends, exactly like the Sprint 1
// selectors, never a panic or a zero value.
func TestNaiveConsistentHashNoHealthyBackends(t *testing.T) {
	tests := []struct {
		name string
		reg  func(t *testing.T) *backend.Registry
	}{
		{
			name: "registry with no backends",
			reg: func(t *testing.T) *backend.Registry {
				t.Helper()
				reg, err := backend.NewRegistry(nil)
				require.NoError(t, err)
				return reg
			},
		},
		{
			name: "all backends unhealthy",
			reg: func(t *testing.T) *backend.Registry {
				t.Helper()
				reg := newTestRegistry(t)
				for _, b := range reg.All() {
					b.MarkUnhealthy()
				}
				return reg
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel := newNaiveConsistentHash(tt.reg(t))

			b, err := selectForAddr(t, sel, "203.0.113.7:54321")
			assert.Nil(t, b)
			assert.ErrorIs(t, err, ErrNoHealthyBackends)
		})
	}
}

// TestNaiveConsistentHashConsultsKey is the teeth check the affinity tests
// lack: they prove a given key is stable, but a selector that ignored the key
// entirely (hashing a constant) would still pass them. Here a large sample of
// octet-diverse clients must reach every backend, which only holds if the key
// actually drives placement.
//
// The seed is chosen once and arbitrary: changing it re-derives the exact
// per-backend counts but not the invariant asserted — a key-blind selector
// starves backends under any seed. The floor is 10% (uniform expectation is
// 25%), well below the ring's observed spread, so this is not a distribution
// re-test; it is a "does placement depend on the input" check.
func TestNaiveConsistentHashConsultsKey(t *testing.T) {
	names := []string{"backend-a", "backend-b", "backend-c", "backend-d"}
	sel := newNaiveConsistentHash(ringRegistry(t, names...))

	const numClients = 400
	rng := rand.New(rand.NewSource(20260920))

	counts := make(map[string]int, len(names))
	for _, ip := range sampleKeys(rng, numClients) {
		counts[selectNameForAddr(t, sel, ip+":12345")]++
	}

	const floor = numClients / 10
	for _, name := range names {
		assert.GreaterOrEqualf(t, counts[name], floor,
			"backend %q got %d of %d selections; a key-blind selector would starve it",
			name, counts[name], numClients)
	}
}

// TestNaiveConsistentHashConcurrentSelect exercises one shared selector from
// many goroutines. The type holds no mutable state and the ring is immutable
// after construction, so this must be clean under -race — the same guarantee
// TestRingConcurrentCandidates substantiates one layer down.
func TestNaiveConsistentHashConcurrentSelect(t *testing.T) {
	sel := newNaiveConsistentHash(newTestRegistry(t))

	const numGoroutines = 100
	rng := rand.New(rand.NewSource(20260921))

	var wg sync.WaitGroup
	for _, ip := range sampleKeys(rng, numGoroutines) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := selectForAddr(t, sel, ip+":12345")
			assert.NoError(t, err)
			assert.NotNil(t, b)
		}()
	}
	wg.Wait()
}
