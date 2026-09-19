package balancer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// newTestRegistry builds a real three-backend registry (all healthy) to use
// as the fixture for selector tests. A real Registry is the natural, cheap
// fixture here — no mocks.
func newTestRegistry(t *testing.T) *backend.Registry {
	t.Helper()
	reg, err := backend.NewRegistry([]config.BackendConfig{
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
		{Name: "backend-b", URL: "http://127.0.0.1:9002"},
		{Name: "backend-c", URL: "http://127.0.0.1:9003"},
	})
	require.NoError(t, err)
	return reg
}

// markHealthy and markUnhealthy flip a named backend's state by intent, so a
// health-transition test reads the same way a production caller does after the
// SetHealthy split (ADR-0011 decision 2) rather than hiding it behind a bool.
func markHealthy(t *testing.T, reg *backend.Registry, name string) {
	t.Helper()
	backendNamed(t, reg, name).MarkHealthy()
}

func markUnhealthy(t *testing.T, reg *backend.Registry, name string) {
	t.Helper()
	backendNamed(t, reg, name).MarkUnhealthy()
}

func backendNamed(t *testing.T, reg *backend.Registry, name string) *backend.Backend {
	t.Helper()
	for _, b := range reg.All() {
		if b.Name == name {
			return b
		}
	}
	require.Failf(t, "backend not found", "no backend named %q in registry", name)
	return nil
}

// selectName selects through s using httptest's fixed default client address
// (192.0.2.1:1234); it is the address-agnostic shorthand for
// selectNameForAddr, which lives in selector_test.go.
func selectName(t *testing.T, s Selector) string {
	t.Helper()
	return selectNameForAddr(t, s, "192.0.2.1:1234")
}

func TestRoundRobinCyclicOrder(t *testing.T) {
	tests := []struct {
		name      string
		unhealthy []string
		calls     int
		want      []string
	}{
		{
			name:  "three healthy backends rotate in registry order",
			calls: 6,
			want:  []string{"backend-a", "backend-b", "backend-c", "backend-a", "backend-b", "backend-c"},
		},
		{
			name:      "unhealthy backend is skipped",
			unhealthy: []string{"backend-b"},
			calls:     4,
			want:      []string{"backend-a", "backend-c", "backend-a", "backend-c"},
		},
		{
			name:      "single healthy backend repeats",
			unhealthy: []string{"backend-b", "backend-c"},
			calls:     3,
			want:      []string{"backend-a", "backend-a", "backend-a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := newTestRegistry(t)
			for _, name := range tt.unhealthy {
				markUnhealthy(t, reg, name)
			}

			s := NewRoundRobin(reg)
			got := make([]string, 0, tt.calls)
			for i := 0; i < tt.calls; i++ {
				got = append(got, selectName(t, s))
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRoundRobinNoHealthyBackends(t *testing.T) {
	reg := newTestRegistry(t)
	for _, b := range reg.All() {
		b.MarkUnhealthy()
	}

	s := NewRoundRobin(reg)
	b, err := s.Select(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Nil(t, b)
	assert.ErrorIs(t, err, ErrNoHealthyBackends)
}

func TestRoundRobinConcurrentDistribution(t *testing.T) {
	const (
		numBackends = 3
		numRequests = 1000
	)

	reg := newTestRegistry(t)
	s := NewRoundRobin(reg)

	indexByName := make(map[string]int, numBackends)
	for i, b := range reg.All() {
		indexByName[b.Name] = i
	}

	var counts [numBackends]atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := s.Select(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil))
			if !assert.NoError(t, err) || !assert.NotNil(t, b) {
				return
			}
			counts[indexByName[b.Name]].Add(1)
		}()
	}
	wg.Wait()

	expectedShare := float64(numRequests) / float64(numBackends)
	tolerance := expectedShare * 0.05

	var total int64
	for i, b := range reg.All() {
		got := counts[i].Load()
		total += got
		assert.InDeltaf(t, expectedShare, got, tolerance,
			"backend %q got %d of %d selections, want within ±5%% of %.1f",
			b.Name, got, numRequests, expectedShare)
	}
	assert.Equal(t, int64(numRequests), total)
}
