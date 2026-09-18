package balancer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
)

// seedActive raises the named backend's active-connection count to n, so a
// test can pre-seed the state LeastConnections reads. Mutation here stands
// in for what the proxy will do in S1.T6.
func seedActive(t *testing.T, reg *backend.Registry, name string, n int) {
	t.Helper()
	for _, b := range reg.All() {
		if b.Name == name {
			for i := 0; i < n; i++ {
				b.IncActive()
			}
			return
		}
	}
	require.Failf(t, "backend not found", "no backend named %q in registry", name)
}

func TestLeastConnectionsPicksMinimum(t *testing.T) {
	tests := []struct {
		name      string
		active    map[string]int
		unhealthy []string
		want      string
	}{
		{
			name:   "picks backend with fewest active connections",
			active: map[string]int{"backend-a": 3, "backend-b": 1, "backend-c": 5},
			want:   "backend-b",
		},
		{
			name:   "idle backend preferred over busy ones",
			active: map[string]int{"backend-a": 2, "backend-b": 0, "backend-c": 1},
			want:   "backend-b",
		},
		{
			name:   "equal counts tie-break to first in registry order",
			active: map[string]int{"backend-a": 4, "backend-b": 4, "backend-c": 4},
			want:   "backend-a",
		},
		{
			name:   "tie only among trailing backends picks earlier of the tied",
			active: map[string]int{"backend-a": 7, "backend-b": 2, "backend-c": 2},
			want:   "backend-b",
		},
		{
			name:      "unhealthy backend ignored even with fewest connections",
			active:    map[string]int{"backend-a": 5, "backend-b": 0, "backend-c": 2},
			unhealthy: []string{"backend-b"},
			want:      "backend-c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := newTestRegistry(t)
			for _, name := range tt.unhealthy {
				setHealthy(t, reg, name, false)
			}
			for name, n := range tt.active {
				seedActive(t, reg, name, n)
			}

			s := NewLeastConnections(reg)
			assert.Equal(t, tt.want, selectName(t, s))
		})
	}
}

func TestLeastConnectionsTieBreakIsDeterministic(t *testing.T) {
	reg := newTestRegistry(t)
	for _, b := range reg.All() {
		b.IncActive()
	}

	s := NewLeastConnections(reg)
	for i := 0; i < 10; i++ {
		assert.Equal(t, "backend-a", selectName(t, s),
			"select %d should always land on the first tied backend", i+1)
	}
}

func TestLeastConnectionsDoesNotMutateActiveConns(t *testing.T) {
	reg := newTestRegistry(t)
	seedActive(t, reg, "backend-a", 2)
	seedActive(t, reg, "backend-b", 1)
	seedActive(t, reg, "backend-c", 3)

	s := NewLeastConnections(reg)
	for i := 0; i < 5; i++ {
		_ = selectName(t, s)
	}

	want := map[string]int64{"backend-a": 2, "backend-b": 1, "backend-c": 3}
	for _, b := range reg.All() {
		assert.Equalf(t, want[b.Name], b.ActiveConns(),
			"Select must not mutate ActiveConns for %q", b.Name)
	}
}

func TestLeastConnectionsNoHealthyBackends(t *testing.T) {
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
					b.SetHealthy(false)
				}
				return reg
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewLeastConnections(tt.reg(t))
			b, err := s.Select(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil))

			assert.Nil(t, b)
			assert.ErrorIs(t, err, ErrNoHealthyBackends)
		})
	}
}
