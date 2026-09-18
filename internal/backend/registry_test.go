package backend

import (
	"sync"
	"testing"

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
				backendByName(t, reg, name).SetHealthy(false)
			}

			assert.Equal(t, []string{"backend-a", "backend-b", "backend-c"}, names(reg.All()))
		})
	}
}

func TestRegistryHealthyFilters(t *testing.T) {
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
				backendByName(t, reg, name).SetHealthy(false)
			}

			assert.Equal(t, tt.wantNames, names(reg.Healthy()))
		})
	}
}

func TestRegistrySnapshotsAreFresh(t *testing.T) {
	reg, err := NewRegistry(testConfigs())
	require.NoError(t, err)

	all := reg.All()
	require.Len(t, all, 3)
	all[0] = nil

	assert.Equal(t, []string{"backend-a", "backend-b", "backend-c"}, names(reg.All()))

	healthy := reg.Healthy()
	require.Len(t, healthy, 3)
	healthy[0] = nil

	assert.Equal(t, []string{"backend-a", "backend-b", "backend-c"}, names(reg.Healthy()))
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
				b.SetHealthy(false)
				b.SetHealthy(true)
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
