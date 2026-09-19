package balancer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// seedLatency records a latency on the named backend, pre-seeding the EWMA a
// selector compares against without needing a real HTTP round trip.
func seedLatency(t *testing.T, reg *backend.Registry, name string, d time.Duration) {
	t.Helper()
	for _, b := range reg.All() {
		if b.Name == name {
			b.RecordLatency(d)
			return
		}
	}
	require.Failf(t, "backend not found", "no backend named %q in registry", name)
}

func TestPowerOfTwoChoicesEWMANoHealthyBackends(t *testing.T) {
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
			s := NewPowerOfTwoChoicesEWMA(tt.reg(t))
			b, err := s.Select(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil))

			assert.Nil(t, b)
			assert.ErrorIs(t, err, ErrNoHealthyBackends)
		})
	}
}

func TestPowerOfTwoChoicesEWMASoleHealthyBackendReturned(t *testing.T) {
	reg := newTestRegistry(t)
	markUnhealthy(t, reg, "backend-b")
	markUnhealthy(t, reg, "backend-c")

	s := NewPowerOfTwoChoicesEWMA(reg)
	for i := 0; i < 100; i++ {
		assert.Equal(t, "backend-a", selectName(t, s),
			"select %d: the sole healthy backend must be returned with no draw", i+1)
	}
}

func TestPowerOfTwoChoicesEWMAPrefersFasterOfTwo(t *testing.T) {
	reg := ringRegistry(t, "backend-a", "backend-b")
	seedLatency(t, reg, "backend-a", 10*time.Millisecond)
	seedLatency(t, reg, "backend-b", 500*time.Millisecond)

	s := NewPowerOfTwoChoicesEWMA(reg)
	for i := 0; i < 200; i++ {
		assert.Equal(t, "backend-a", selectName(t, s),
			"select %d: with two healthy backends both are drawn, so the faster one must win", i+1)
	}
}

// TestPowerOfTwoChoicesEWMAShiftsLoadToFasterBackend is the load-skew property
// MILESTONES.md's Sprint 2 exit criteria name: after a sustained latency gap
// is recorded, P2C routes measurably more traffic to the faster backend. With
// four healthy backends P2C samples two, so the fast backend is drawn half the
// time and wins every comparison it appears in (~50% of requests overall),
// while each slow backend receives roughly a sixth.
func TestPowerOfTwoChoicesEWMAShiftsLoadToFasterBackend(t *testing.T) {
	const (
		fastName = "backend-a"
		samples  = 4000
	)

	reg := ringRegistry(t, "backend-a", "backend-b", "backend-c", "backend-d")
	for _, b := range reg.All() {
		if b.Name == fastName {
			seedLatency(t, reg, b.Name, 10*time.Millisecond)
			continue
		}
		seedLatency(t, reg, b.Name, 500*time.Millisecond)
	}

	s := NewPowerOfTwoChoicesEWMA(reg)
	counts := make(map[string]int, len(reg.All()))
	for i := 0; i < samples; i++ {
		counts[selectName(t, s)]++
	}

	fast := counts[fastName]
	var slowMax int
	for name, c := range counts {
		if name == fastName {
			continue
		}
		if c > slowMax {
			slowMax = c
		}
	}

	assert.InDelta(t, samples/2, fast, samples/10,
		"fast backend took %d of %d, want ~50%% (counts=%v)", fast, samples, counts)
	t.Logf("S2.T3 load-skew: fast=%d/%d (%.1f%%), per-slow=%v", fast, samples,
		100*float64(fast)/float64(samples), counts)
	assert.Greater(t, fast, slowMax*2,
		"P2C must measurably favor the fast backend: fast=%d slowest-of-slow=%d (counts=%v)",
		fast, slowMax, counts)
}

func TestPowerOfTwoChoicesEWMARespectsHealthTransitions(t *testing.T) {
	const target = "backend-a"

	reg := newTestRegistry(t)
	seedLatency(t, reg, target, 10*time.Millisecond)
	seedLatency(t, reg, "backend-b", 500*time.Millisecond)
	seedLatency(t, reg, "backend-c", 500*time.Millisecond)
	s := NewPowerOfTwoChoicesEWMA(reg)

	t.Run("healthy baseline chooses target", func(t *testing.T) {
		chosen := selectNames(t, s, 100)
		assert.Contains(t, chosen, target, "target must be selectable while healthy")
	})

	t.Run("unhealthy mid-run stops choosing target", func(t *testing.T) {
		markUnhealthy(t, reg, target)
		chosen := selectNames(t, s, 100)
		assert.NotContains(t, chosen, target, "target must not be chosen while unhealthy")
	})

	t.Run("recovered resumes choosing target", func(t *testing.T) {
		markHealthy(t, reg, target)
		chosen := selectNames(t, s, 100)
		assert.Contains(t, chosen, target, "target must be chosen again after recovery")
	})
}

func TestPowerOfTwoChoicesEWMAConcurrentSelect(t *testing.T) {
	reg := newTestRegistry(t)
	seedLatency(t, reg, "backend-a", 10*time.Millisecond)
	seedLatency(t, reg, "backend-b", 50*time.Millisecond)
	seedLatency(t, reg, "backend-c", 90*time.Millisecond)
	s := NewPowerOfTwoChoicesEWMA(reg)

	const goroutines = 100
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := s.Select(context.Background(), requestForAddr("192.0.2.1:1234"))
			if !assert.NoError(t, err) || !assert.NotNil(t, b) {
				return
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, b := range reg.All() {
				b.RecordLatency(time.Millisecond)
			}
		}()
	}
	wg.Wait()
}
