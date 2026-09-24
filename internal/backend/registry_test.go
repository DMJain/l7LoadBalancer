package backend

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/config"
)

func testConfigs() []config.BackendConfig {
	return []config.BackendConfig{
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
		{Name: "backend-b", URL: "http://127.0.0.1:9002"},
		{Name: "backend-c", URL: "http://127.0.0.1:9003"},
	}
}

// backendByName is a test helper that returns the live *Backend for name from
// the registry's current snapshot, so callers can mutate the pointer directly.
func backendByName(t *testing.T, reg *Registry, name string) *Backend {
	t.Helper()
	for _, b := range reg.All() {
		if b.Name == name {
			return b
		}
	}
	require.Failf(t, "backend not found", "no backend named %q in registry", name)
	return nil
}

func names(bs []*Backend) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Name
	}
	return out
}

func TestNewRegistry(t *testing.T) {
	tests := []struct {
		name      string
		cfgs      []config.BackendConfig
		wantNames []string
		wantURLs  []string
	}{
		{
			name:      "three backends preserve config order",
			cfgs:      testConfigs(),
			wantNames: []string{"backend-a", "backend-b", "backend-c"},
			wantURLs:  []string{"http://127.0.0.1:9001", "http://127.0.0.1:9002", "http://127.0.0.1:9003"},
		},
		{
			name:      "single backend",
			cfgs:      testConfigs()[:1],
			wantNames: []string{"backend-a"},
			wantURLs:  []string{"http://127.0.0.1:9001"},
		},
		{
			name:      "backend with path prefix preserved",
			cfgs:      []config.BackendConfig{{Name: "backend-a", URL: "http://127.0.0.1:9001/api"}},
			wantNames: []string{"backend-a"},
			wantURLs:  []string{"http://127.0.0.1:9001/api"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, err := NewRegistry(tt.cfgs)
			require.NoError(t, err)

			all := reg.All()
			require.Len(t, all, len(tt.wantNames))
			for i, b := range all {
				assert.Equal(t, tt.wantNames[i], b.Name)
				require.NotNil(t, b.URL)
				assert.Equal(t, tt.wantURLs[i], b.URL.String())
			}
		})
	}
}

func TestNewRegistryStartsAllHealthy(t *testing.T) {
	reg, err := NewRegistry(testConfigs())
	require.NoError(t, err)

	for _, b := range reg.All() {
		assert.True(t, b.IsHealthy(), "backend %q must start healthy", b.Name)
	}
}

func TestNewRegistryInvalidURL(t *testing.T) {
	_, err := NewRegistry([]config.BackendConfig{{Name: "backend-a", URL: "http://[::1"}})
	require.Error(t, err)
}

func TestRegistryAllIncludesUnhealthy(t *testing.T) {
	tests := []struct {
		name      string
		unhealthy []string
	}{
		{name: "none unhealthy"},
		{name: "one unhealthy", unhealthy: []string{"backend-b"}},
		{name: "all unhealthy", unhealthy: []string{"backend-a", "backend-b", "backend-c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, err := NewRegistry(testConfigs())
			require.NoError(t, err)
			for _, name := range tt.unhealthy {
				backendByName(t, reg, name).MarkUnhealthy()
			}

			assert.Equal(t, []string{"backend-a", "backend-b", "backend-c"}, names(reg.All()))
		})
	}
}

// fakeGate is a minimal CircuitGate for testing the registry's gate delegation
// without importing internal/circuit, which would be an import cycle from this
// package. A backend present in open reports as circuit-open; every other
// backend is admitted.
type fakeGate struct {
	open map[*Backend]bool
}

func (g fakeGate) Open(b *Backend) bool  { return g.open[b] }
func (g fakeGate) Allow(b *Backend) bool { return !g.open[b] }

func TestRegistrySelectableFilters(t *testing.T) {
	tests := []struct {
		name      string
		unhealthy []string
		wantNames []string
	}{
		{
			name:      "all healthy by default",
			wantNames: []string{"backend-a", "backend-b", "backend-c"},
		},
		{
			name:      "unhealthy backend excluded, order preserved",
			unhealthy: []string{"backend-b"},
			wantNames: []string{"backend-a", "backend-c"},
		},
		{
			name:      "all unhealthy yields empty snapshot",
			unhealthy: []string{"backend-a", "backend-b", "backend-c"},
			wantNames: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, err := NewRegistry(testConfigs())
			require.NoError(t, err)
			for _, name := range tt.unhealthy {
				backendByName(t, reg, name).MarkUnhealthy()
			}

			assert.Equal(t, tt.wantNames, names(reg.Selectable()))
		})
	}
}

func TestRegistrySelectableHonorsCircuitGate(t *testing.T) {
	reg, err := NewRegistry(testConfigs())
	require.NoError(t, err)

	gate := fakeGate{open: make(map[*Backend]bool)}
	reg.SetCircuitGate(gate)

	b := backendByName(t, reg, "backend-b")
	gate.open[b] = true

	assert.Equal(t, []string{"backend-a", "backend-c"}, names(reg.Selectable()),
		"a circuit-open backend must be excluded from Selectable")
	assert.False(t, reg.Allow(b), "a circuit-open backend must be denied")
	assert.True(t, reg.Allow(backendByName(t, reg, "backend-a")),
		"a backend with no open circuit must be admitted")
}

func TestRegistryAllowWithoutGateAdmits(t *testing.T) {
	reg, err := NewRegistry(testConfigs())
	require.NoError(t, err)

	assert.True(t, reg.Allow(reg.All()[0]),
		"with no circuit gate set, admission must be unconditional")
}

func TestRegistrySnapshotsAreFresh(t *testing.T) {
	reg, err := NewRegistry(testConfigs())
	require.NoError(t, err)

	all := reg.All()
	require.Len(t, all, 3)
	all[0] = nil

	assert.Equal(t, []string{"backend-a", "backend-b", "backend-c"}, names(reg.All()))

	healthy := reg.Selectable()
	require.Len(t, healthy, 3)
	healthy[0] = nil

	assert.Equal(t, []string{"backend-a", "backend-b", "backend-c"}, names(reg.Selectable()))
}

func TestRegistryVersionAndSnapshot(t *testing.T) {
	reg, err := NewRegistry(testConfigs())
	require.NoError(t, err)

	v := reg.Version()
	require.NotZero(t, v, "the initial snapshot must carry a nonzero version")

	gotV, gotAll := reg.Snapshot()
	assert.Equal(t, v, gotV, "Snapshot must report the version Version does")
	assert.Equal(t, []string{"backend-a", "backend-b", "backend-c"}, names(gotAll))
}

func TestRegistryApplyKeepsUnchangedInstancesAndState(t *testing.T) {
	reg, err := NewRegistry(testConfigs())
	require.NoError(t, err)

	a := backendByName(t, reg, "backend-a")
	b := backendByName(t, reg, "backend-b")
	a.MarkUnhealthy()
	a.IncActive()
	a.RecordLatency(50 * time.Millisecond)
	openCircuit(t, b, 3)

	v0 := reg.Version()

	const addedURL = "http://127.0.0.1:9004"
	newBackends := []config.BackendConfig{
		{Name: "backend-b", URL: "http://127.0.0.1:9002"},
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
		{Name: "backend-d", URL: addedURL},
	}
	diff := config.BackendDiff{
		Added: []config.BackendConfig{{Name: "backend-d", URL: addedURL}},
		Removed: []config.BackendConfig{
			{Name: "backend-c", URL: "http://127.0.0.1:9003"},
		},
		Unchanged: []config.BackendConfig{newBackends[0], newBackends[1]},
	}

	added, removed, err := reg.Apply(diff, newBackends)
	require.NoError(t, err)

	require.Len(t, added, 1)
	assert.Equal(t, "backend-d", added[0].Name)
	assert.True(t, added[0].IsHealthy(), "an added backend is constructed healthy")
	assert.Equal(t, int64(0), added[0].ActiveConns(), "an added backend starts idle")
	require.Len(t, removed, 1)
	assert.Equal(t, "backend-c", removed[0].Name)
	assert.True(t, removed[0].IsRemoved())

	// Unchanged identities keep their instance and every piece of state.
	assert.Same(t, a, backendByName(t, reg, "backend-a"))
	assert.Same(t, b, backendByName(t, reg, "backend-b"))
	assert.False(t, a.IsHealthy(), "unchanged health state must survive")
	assert.Equal(t, int64(1), a.ActiveConns(), "unchanged active connections must survive")
	assert.Equal(t, 50*time.Millisecond, a.EWMALatency(), "unchanged EWMA must survive")
	assert.True(t, b.CircuitOpen(circuitTestCooldown), "unchanged circuit state must survive")

	// The snapshot follows the new file's order, and the removed backend is gone.
	assert.Equal(t, []string{"backend-b", "backend-a", "backend-d"}, names(reg.All()))
	assert.NotContains(t, names(reg.All()), "backend-c")
	assert.NotContains(t, names(reg.Selectable()), "backend-c")
	assert.Greater(t, reg.Version(), v0, "version must increase monotonically")
}

func TestRegistryApplyReAddedIdentityIsFresh(t *testing.T) {
	reg, err := NewRegistry(testConfigs()[:1])
	require.NoError(t, err)

	original := backendByName(t, reg, "backend-a")
	identity := config.BackendConfig{Name: "backend-a", URL: "http://127.0.0.1:9001"}

	_, removed, err := reg.Apply(config.BackendDiff{Removed: []config.BackendConfig{identity}}, nil)
	require.NoError(t, err)
	require.Len(t, removed, 1)
	require.Same(t, original, removed[0])

	added, _, err := reg.Apply(
		config.BackendDiff{Added: []config.BackendConfig{identity}},
		[]config.BackendConfig{identity},
	)
	require.NoError(t, err)
	require.Len(t, added, 1)
	assert.NotSame(t, original, added[0], "a re-added identity must be a fresh instance")
	assert.False(t, added[0].IsRemoved(), "the fresh instance must not inherit the removed flag")
	assert.True(t, added[0].IsHealthy())
}

func TestRegistryConcurrentApplyAndRead(t *testing.T) {
	reg, err := NewRegistry(testConfigs())
	require.NoError(t, err)

	withD := append(append([]config.BackendConfig{}, testConfigs()...),
		config.BackendConfig{Name: "backend-d", URL: "http://127.0.0.1:9004"})
	withoutD := testConfigs()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = reg.All()
				_ = reg.Selectable()
				_ = reg.Version()
				_, _ = reg.Snapshot()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		for i := 0; i < 50; i++ {
			if _, _, err := reg.Apply(
				config.BackendDiff{Added: []config.BackendConfig{withD[len(withD)-1]}},
				withD,
			); err != nil {
				t.Errorf("apply add: %v", err)
			}
			if _, _, err := reg.Apply(
				config.BackendDiff{Removed: []config.BackendConfig{withD[len(withD)-1]}},
				withoutD,
			); err != nil {
				t.Errorf("apply remove: %v", err)
			}
		}
	}()

	wg.Wait()
}

func TestRegistryConcurrentMutation(t *testing.T) {
	reg, err := NewRegistry(testConfigs())
	require.NoError(t, err)

	var wg sync.WaitGroup
	for _, b := range reg.All() {
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(b *Backend) {
				defer wg.Done()
				b.IncActive()
				_ = b.IsHealthy()
				b.MarkUnhealthy()
				b.MarkHealthy()
				b.DecActive()
			}(b)
		}
	}
	wg.Wait()

	for _, b := range reg.All() {
		assert.Equal(t, int64(0), b.ActiveConns())
		assert.True(t, b.IsHealthy())
	}
}
