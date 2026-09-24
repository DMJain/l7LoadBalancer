package balancer

import (
	"context"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// The bounded-loads selector routes by client IP like naiveConsistentHash, so
// these tests control RemoteAddr. candidateOrderForAddr exposes the ring walk
// the selector uses internally (same backend names -> same ring), so a test
// can make a deliberate choice about which candidate to push over capacity.

// candidateOrderForAddr returns the backend names the ring yields for addr's
// hash key, in walk order. The selector builds its own identical ring from the
// same backend names, so this is the order its Select walk visits.
func candidateOrderForAddr(t *testing.T, reg *backend.Registry, addr string) []string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = addr

	names := make([]string, 0, len(reg.All()))
	for b := range newRing(reg.All()).candidates(requestHashKey(r)) {
		names = append(names, b.Name)
	}
	require.NotEmpty(t, names, "ring yielded no candidates")
	return names
}

// TestConsistentHashBoundedLoadsSkipsOverCapacity is the capacity check's core
// behavior: the ring's first-choice backend is pushed far over capacity, so
// Select must walk past it and pick the next candidate.
func TestConsistentHashBoundedLoadsSkipsOverCapacity(t *testing.T) {
	const addr = "203.0.113.7:54321"

	reg := ringRegistry(t, "backend-a", "backend-b", "backend-c", "backend-d")
	sel := NewConsistentHashBoundedLoads(reg)

	order := candidateOrderForAddr(t, reg, addr)
	overCapacity := order[0]

	// avg = 100/4 = 25, capacity = ceil(25 * 1.25) = 32; 100 is far above it.
	seedActive(t, reg, overCapacity, 100)

	chosen := selectNameForAddr(t, sel, addr)
	assert.NotEqualf(t, overCapacity, chosen,
		"backend %q is far over capacity and must not be selected", overCapacity)
	assert.Equal(t, order[1], chosen,
		"the next candidate in the walk should be selected")
}

// TestConsistentHashBoundedLoadsCapacityBoundary pins the `<=` admission rule
// the hot-key evidence depends on: a candidate exactly at its capacity is
// admitted, one over is skipped. With two backends and one idle, load L gives
// avg = L/2 and capacity = ceil(L * 0.625): L=2 admits (cap 2), L=3 skips
// (cap 2).
func TestConsistentHashBoundedLoadsCapacityBoundary(t *testing.T) {
	const addr = "203.0.113.7:54321"

	tests := []struct {
		name       string
		active     int
		wantFirst  bool
		wantReason string
	}{
		{name: "exactly at capacity is admitted", active: 2, wantFirst: true,
			wantReason: "avg=1, capacity=ceil(1.25)=2, 2 <= 2"},
		{name: "one over capacity is skipped", active: 3, wantFirst: false,
			wantReason: "avg=1.5, capacity=ceil(1.875)=2, 3 > 2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := ringRegistry(t, "backend-a", "backend-b")
			sel := NewConsistentHashBoundedLoads(reg)

			first := candidateOrderForAddr(t, reg, addr)[0]
			seedActive(t, reg, first, tt.active)

			chosen := selectNameForAddr(t, sel, addr)
			if tt.wantFirst {
				assert.Equalf(t, first, chosen, "expected admission: %s", tt.wantReason)
			} else {
				assert.NotEqualf(t, first, chosen, "expected skip: %s", tt.wantReason)
			}
		})
	}
}

// TestConsistentHashBoundedLoadsIdleSystemAdmits proves the capacity floor:
// on an idle system avg_active is zero, so capacity would be zero without the
// max(1, ...) floor, and every backend's first request would be rejected.
func TestConsistentHashBoundedLoadsIdleSystemAdmits(t *testing.T) {
	reg := ringRegistry(t, "backend-a", "backend-b", "backend-c", "backend-d")
	sel := NewConsistentHashBoundedLoads(reg)

	for _, addr := range []string{
		"203.0.113.7:54321",
		"198.51.100.23:1234",
		"[2001:db8::1]:443",
		"10.0.0.1:80",
	} {
		b, err := selectForAddr(t, sel, addr)
		require.NoErrorf(t, err, "idle system rejected a first request from %s", addr)
		require.NotNil(t, b)
	}
}

// TestConsistentHashBoundedLoadsStableAffinity confirms the session-affinity
// property survives the capacity layer while no backend is under load.
func TestConsistentHashBoundedLoadsStableAffinity(t *testing.T) {
	const addr = "203.0.113.7:54321"

	sel := NewConsistentHashBoundedLoads(newTestRegistry(t))
	want := selectNameForAddr(t, sel, addr)
	for i := 0; i < 20; i++ {
		assert.Equalf(t, want, selectNameForAddr(t, sel, addr),
			"same client IP changed backend on selection %d", i+1)
	}
}

// TestConsistentHashBoundedLoadsRespectsHealthTransitions is the same scripted
// health sequence naiveConsistentHash and the Sprint 1 selectors cover:
// bounded-loads applies the identical health check alongside its capacity
// check, so an unhealthy backend is skipped regardless of its capacity
// headroom, and resumes once healthy.
func TestConsistentHashBoundedLoadsRespectsHealthTransitions(t *testing.T) {
	const client = "203.0.113.7:54321"

	reg := ringRegistry(t, "backend-a", "backend-b", "backend-c", "backend-d")
	sel := NewConsistentHashBoundedLoads(reg)

	target := selectNameForAddr(t, sel, client)

	t.Run("healthy baseline chooses target", func(t *testing.T) {
		assert.Equal(t, target, selectNameForAddr(t, sel, client))
	})

	t.Run("unhealthy mid-run stops choosing target", func(t *testing.T) {
		markUnhealthy(t, reg, target)
		for i := 0; i < 5; i++ {
			next, err := selectForAddr(t, sel, client)
			require.NoError(t, err)
			require.NotNil(t, next)
			assert.NotEqualf(t, target, next.Name, "unhealthy target chosen on selection %d", i+1)
		}
	})

	t.Run("recovered resumes choosing target", func(t *testing.T) {
		markHealthy(t, reg, target)
		assert.Equal(t, target, selectNameForAddr(t, sel, client),
			"target not chosen again after recovery")
	})
}

// TestConsistentHashBoundedLoadsNoHealthyBackends covers both ways the healthy
// set can be empty: a registry with no backends and every backend unhealthy.
// The capacity check must never turn this into a distinct "over capacity"
// error — the same ErrNoHealthyBackends every other selector returns.
func TestConsistentHashBoundedLoadsNoHealthyBackends(t *testing.T) {
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
			sel := NewConsistentHashBoundedLoads(tt.reg(t))

			b, err := selectForAddr(t, sel, "203.0.113.7:54321")
			assert.Nil(t, b)
			assert.ErrorIs(t, err, ErrNoHealthyBackends)
		})
	}
}

// TestConsistentHashBoundedLoadsDoesNotMutateActiveConns pins the separation of
// concerns LeastConnections already documents: Select reads ActiveConns, the
// proxy mutates it. Bookkeeping leaking into the selector would corrupt the
// average it computes.
func TestConsistentHashBoundedLoadsDoesNotMutateActiveConns(t *testing.T) {
	reg := ringRegistry(t, "backend-a", "backend-b", "backend-c", "backend-d")
	seedActive(t, reg, "backend-a", 2)
	seedActive(t, reg, "backend-b", 1)
	seedActive(t, reg, "backend-c", 3)

	sel := NewConsistentHashBoundedLoads(reg)
	for _, addr := range []string{"203.0.113.7:1", "198.51.100.23:2", "10.0.0.1:3"} {
		_, err := selectForAddr(t, sel, addr)
		require.NoError(t, err)
	}

	want := map[string]int64{"backend-a": 2, "backend-b": 1, "backend-c": 3, "backend-d": 0}
	for _, b := range reg.All() {
		assert.Equalf(t, want[b.Name], b.ActiveConns(),
			"Select must not mutate ActiveConns for %q", b.Name)
	}
}

// TestConsistentHashBoundedLoadsConcurrentSelect exercises one shared selector
// from many goroutines while another churns ActiveConns, so the average and
// capacity reads are exercised under -race.
func TestConsistentHashBoundedLoadsConcurrentSelect(t *testing.T) {
	reg := ringRegistry(t, "backend-a", "backend-b", "backend-c", "backend-d")
	sel := NewConsistentHashBoundedLoads(reg)

	const numGoroutines = 100
	keys := sampleKeys(rand.New(rand.NewSource(20260922)), numGoroutines)

	var wg sync.WaitGroup
	for _, key := range keys {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			b, err := selectForAddr(t, sel, key+":12345")
			assert.NoError(t, err)
			assert.NotNil(t, b)
		}(key)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			for _, b := range reg.All() {
				b.IncActive()
				b.DecActive()
			}
		}
	}()

	wg.Wait()
}

// TestConsistentHashBoundedLoadsConsultKey is the teeth check the affinity
// tests lack: a selector that ignored the key and always took the first ring
// position would pass them. A large octet-diverse client sample must reach
// every backend.
func TestConsistentHashBoundedLoadsConsultKey(t *testing.T) {
	names := []string{"backend-a", "backend-b", "backend-c", "backend-d"}
	reg := ringRegistry(t, names...)
	sel := NewConsistentHashBoundedLoads(reg)

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

// TestConsistentHashBoundedLoadsSelectContext verifies the selector honors the
// Selector interface at the call seam rather than only through test helpers.
func TestConsistentHashBoundedLoadsSelectContext(t *testing.T) {
	reg := ringRegistry(t, "backend-a", "backend-b")
	sel := NewConsistentHashBoundedLoads(reg)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.7:54321"
	b, err := sel.Select(context.Background(), r)

	require.NoError(t, err)
	require.NotNil(t, b)
}

// TestConsistentHashBoundedLoadsRebuildsRingOnVersionChange pins ADR-0015
// decision 9: the selector caches its ring against the snapshot version and
// rebuilds from the new backend set when the version moves, so a removed
// backend owns no ring position after the swap and an added one takes its
// share.
func TestConsistentHashBoundedLoadsRebuildsRingOnVersionChange(t *testing.T) {
	reg := ringRegistry(t, "backend-a", "backend-b")
	sel := NewConsistentHashBoundedLoads(reg)

	const (
		aURL = "http://127.0.0.1:9001"
		bURL = "http://127.0.0.1:9002"
		cURL = "http://127.0.0.1:9003"
	)
	newBackends := []config.BackendConfig{
		{Name: "backend-b", URL: bURL},
		{Name: "backend-c", URL: cURL},
	}
	diff := config.BackendDiff{
		Added:     []config.BackendConfig{{Name: "backend-c", URL: cURL}},
		Removed:   []config.BackendConfig{{Name: "backend-a", URL: aURL}},
		Unchanged: []config.BackendConfig{{Name: "backend-b", URL: bURL}},
	}
	_, _, err := reg.Apply(diff, newBackends)
	require.NoError(t, err)

	rng := rand.New(rand.NewSource(20260925))
	for _, ip := range sampleKeys(rng, 200) {
		got := selectNameForAddr(t, sel, ip+":12345")
		assert.Contains(t, []string{"backend-b", "backend-c"}, got,
			"a removed backend must own no ring position after the swap")
	}
}

// TestConsistentHashBoundedLoadsKeepsAffinityAcrossReorder pins story 41: when
// a reload changes only the file order, the ring is rebuilt (the version moved)
// but membership is identical, so every key keeps its owner — session affinity
// survives the swap.
func TestConsistentHashBoundedLoadsKeepsAffinityAcrossReorder(t *testing.T) {
	reg := ringRegistry(t, "backend-a", "backend-b", "backend-c", "backend-d")
	sel := NewConsistentHashBoundedLoads(reg)

	rng := rand.New(rand.NewSource(20260925))
	keys := sampleKeys(rng, 100)

	before := make(map[string]string, len(keys))
	for _, ip := range keys {
		before[ip] = selectNameForAddr(t, sel, ip+":12345")
	}

	reordered := []config.BackendConfig{
		{Name: "backend-d", URL: "http://127.0.0.1:9004"},
		{Name: "backend-c", URL: "http://127.0.0.1:9003"},
		{Name: "backend-b", URL: "http://127.0.0.1:9002"},
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
	}
	_, _, err := reg.Apply(config.BackendDiff{Unchanged: reordered}, reordered)
	require.NoError(t, err)

	for _, ip := range keys {
		assert.Equalf(t, before[ip], selectNameForAddr(t, sel, ip+":12345"),
			"a reorder-only reload must not move key %s", ip)
	}
}

// TestConsistentHashBoundedLoadsConcurrentSelectAcrossSwap drives the selector
// from many goroutines while another goroutine swaps the backend set the ring
// is built from, so the version-check/rebuild path is exercised under -race.
func TestConsistentHashBoundedLoadsConcurrentSelectAcrossSwap(t *testing.T) {
	reg := ringRegistry(t, "backend-a", "backend-b", "backend-c", "backend-d")
	sel := NewConsistentHashBoundedLoads(reg)

	const (
		aURL = "http://127.0.0.1:9001"
		bURL = "http://127.0.0.1:9002"
		cURL = "http://127.0.0.1:9003"
		dURL = "http://127.0.0.1:9004"
		eURL = "http://127.0.0.1:9005"
	)
	withD := []config.BackendConfig{
		{Name: "backend-a", URL: aURL},
		{Name: "backend-b", URL: bURL},
		{Name: "backend-c", URL: cURL},
		{Name: "backend-d", URL: dURL},
	}
	withE := []config.BackendConfig{
		{Name: "backend-a", URL: aURL},
		{Name: "backend-b", URL: bURL},
		{Name: "backend-c", URL: cURL},
		{Name: "backend-e", URL: eURL},
	}
	addE := config.BackendDiff{
		Added:     []config.BackendConfig{{Name: "backend-e", URL: eURL}},
		Removed:   []config.BackendConfig{{Name: "backend-d", URL: dURL}},
		Unchanged: withD[:3],
	}
	addD := config.BackendDiff{
		Added:     []config.BackendConfig{{Name: "backend-d", URL: dURL}},
		Removed:   []config.BackendConfig{{Name: "backend-e", URL: eURL}},
		Unchanged: withE[:3],
	}

	rng := rand.New(rand.NewSource(20260925))
	keys := sampleKeys(rng, 100)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for _, key := range keys {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				b, err := selectForAddr(t, sel, key+":12345")
				assert.NoError(t, err)
				assert.NotNil(t, b)
			}
		}(key)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		for i := 0; i < 50; i++ {
			if _, _, err := reg.Apply(addE, withE); err != nil {
				t.Errorf("apply add e: %v", err)
			}
			if _, _, err := reg.Apply(addD, withD); err != nil {
				t.Errorf("apply add d: %v", err)
			}
		}
	}()

	wg.Wait()
}
