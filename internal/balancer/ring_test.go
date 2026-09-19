package balancer

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// These tests exercise the ring's own API directly: pure placement and
// candidate-walk properties, with no Selector, no Registry.Healthy()
// filtering, and no HTTP. Placement is a ring property, not a selector
// property, so testing it through Select would only add health/error-path
// noise. See ADR-0008.

// ringRegistry builds a real registry over the named backends, in order,
// to use as a fixture. Backends all start healthy but the ring must place
// them regardless of health, so health is never set here.
func ringRegistry(t *testing.T, names ...string) *backend.Registry {
	t.Helper()
	cfgs := make([]config.BackendConfig, len(names))
	for i, n := range names {
		cfgs[i] = config.BackendConfig{Name: n, URL: fmt.Sprintf("http://127.0.0.1:%d", 9001+i)}
	}
	reg, err := backend.NewRegistry(cfgs)
	require.NoError(t, err)
	return reg
}

// ringFirst returns the first backend the candidate walk yields for key —
// the ring's mapping of key to a backend.
func ringFirst(t *testing.T, r *ring, key string) *backend.Backend {
	t.Helper()
	for b := range r.candidates(key) {
		return b
	}
	require.FailNow(t, "ring yielded no candidates", "key %q", key)
	return nil
}

func randomClientIP(rng *rand.Rand) string {
	return fmt.Sprintf("%d.%d.%d.%d", rng.Intn(254)+1, rng.Intn(254)+1, rng.Intn(254)+1, rng.Intn(254)+1)
}

func sampleKeys(rng *rand.Rand, n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = randomClientIP(rng)
	}
	return keys
}

// TestRingStableMapping asserts the same key maps to the same backend
// across repeated calls, and that two rings built from the same backend set
// agree — placement depends on nothing but the key and the backend names.
func TestRingStableMapping(t *testing.T) {
	tests := []struct {
		name  string
		names []string
		keys  []string
	}{
		{
			name:  "four backends",
			names: []string{"backend-a", "backend-b", "backend-c", "backend-d"},
			keys:  []string{"203.0.113.7", "198.51.100.23", "10.0.0.1", "172.16.254.9", "8.8.8.8"},
		},
		{
			name:  "single backend",
			names: []string{"only-backend"},
			keys:  []string{"203.0.113.7", "10.0.0.1", "8.8.8.8"},
		},
		{
			name:  "five backends",
			names: []string{"backend-a", "backend-b", "backend-c", "backend-d", "backend-e"},
			keys:  []string{"203.0.113.7", "198.51.100.23", "10.0.0.1", "172.16.254.9", "8.8.8.8"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRing(ringRegistry(t, tt.names...).All())
			again := newRing(ringRegistry(t, tt.names...).All())

			for _, key := range tt.keys {
				first := ringFirst(t, r, key)
				assert.Same(t, first, ringFirst(t, r, key),
					"key %q remapped on a repeated call", key)
				assert.Equal(t, first.Name, ringFirst(t, again, key).Name,
					"key %q differs between two identically-built rings", key)
			}
		})
	}
}

// TestRingMinimalDisruption verifies the classic consistent-hashing
// property: changing membership by one backend remaps only roughly 1/n of
// keys, not a large fraction. Rebuilding the ring after a shared-prefix
// hash would remap almost everything, which this would catch.
func TestRingMinimalDisruption(t *testing.T) {
	const numKeys = 10000
	base := []string{"backend-a", "backend-b", "backend-c", "backend-d"}

	rng := rand.New(rand.NewSource(7))
	keys := sampleKeys(rng, numKeys)

	before := newRing(ringRegistry(t, base...).All())

	tests := []struct {
		name             string
		names            []string
		lo, hi           float64
		expectedFraction string
	}{
		{
			name:             "adding a fifth backend remaps about 1/5",
			names:            append(append([]string{}, base...), "backend-e"),
			lo:               0.15,
			hi:               0.25,
			expectedFraction: "1/(n+1)",
		},
		{
			name:             "removing the fourth backend remaps about 1/4",
			names:            base[:3],
			lo:               0.22,
			hi:               0.32,
			expectedFraction: "1/n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			after := newRing(ringRegistry(t, tt.names...).All())

			changed := 0
			for _, key := range keys {
				if ringFirst(t, before, key).Name != ringFirst(t, after, key).Name {
					changed++
				}
			}
			fraction := float64(changed) / numKeys
			assert.GreaterOrEqual(t, fraction, tt.lo,
				"only %.1f%% of keys remapped; expected roughly %s", fraction*100, tt.expectedFraction)
			assert.LessOrEqual(t, fraction, tt.hi,
				"%.1f%% of keys remapped; expected roughly %s", fraction*100, tt.expectedFraction)
		})
	}
}

// TestRingVnodeDistributionUniform asserts virtual-node placement spreads a
// large sample of random keys reasonably evenly across backends. A single
// ring position per backend would produce high variance here; 150 virtual
// nodes per backend smooths it out.
func TestRingVnodeDistributionUniform(t *testing.T) {
	names := []string{"backend-a", "backend-b", "backend-c", "backend-d"}
	r := newRing(ringRegistry(t, names...).All())

	rng := rand.New(rand.NewSource(20260919))
	const numKeys = 20000
	counts := make(map[string]int, len(names))
	for _, key := range sampleKeys(rng, numKeys) {
		counts[ringFirst(t, r, key).Name]++
	}

	expected := float64(numKeys) / float64(len(names))
	tolerance := expected * 0.10
	for _, name := range names {
		assert.InDeltaf(t, expected, counts[name], tolerance,
			"backend %q got %d of %d keys, want within ±10%% of %.0f",
			name, counts[name], numKeys, expected)
	}
}

// TestRingCandidatesYieldEachBackendOnce asserts the walk started at a
// key's ring position yields every distinct backend exactly once, regardless
// of how many virtual nodes share a ring neighbourhood, and that this holds
// across many keys (so the wrap-around path is exercised too).
func TestRingCandidatesYieldEachBackendOnce(t *testing.T) {
	names := []string{"backend-a", "backend-b", "backend-c", "backend-d"}
	r := newRing(ringRegistry(t, names...).All())

	rng := rand.New(rand.NewSource(99))
	for i, key := range sampleKeys(rng, 200) {
		seen := make(map[string]int, len(names))
		for b := range r.candidates(key) {
			seen[b.Name]++
		}
		require.Len(t, seen, len(names), "key %q (%d) yielded %d distinct backends", key, i, len(seen))
		for _, name := range names {
			assert.Equalf(t, 1, seen[name], "key %q (%d): backend %q yielded %d times", key, i, name, seen[name])
		}
	}
}

// TestRingCandidatesEmptyRegistry asserts an empty ring yields no
// candidates rather than panicking — the single-backend and zero-backend
// edge cases must not be able to crash a selector's walk.
func TestRingCandidatesEmptyRegistry(t *testing.T) {
	r := newRing(ringRegistry(t).All())
	for b := range r.candidates("203.0.113.7") {
		require.Failf(t, "empty ring yielded a backend", "got %q", b.Name)
	}
}

// TestRingConcurrentCandidates exercises one shared ring from many
// goroutines. The ring is immutable after construction, so this must be
// clean under -race: it substantiates the "safe to share" claim rather
// than only asserting it in a comment.
func TestRingConcurrentCandidates(t *testing.T) {
	names := []string{"backend-a", "backend-b", "backend-c", "backend-d"}
	r := newRing(ringRegistry(t, names...).All())

	const numGoroutines = 100
	keys := sampleKeys(rand.New(rand.NewSource(42)), numGoroutines)

	var wg sync.WaitGroup
	for _, key := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			yielded := 0
			for range r.candidates(key) {
				yielded++
			}
			assert.Equal(t, len(names), yielded)
		}()
	}
	wg.Wait()
}
