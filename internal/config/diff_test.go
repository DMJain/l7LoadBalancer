package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func bc(name, url string) BackendConfig {
	return BackendConfig{Name: name, URL: url}
}

// backendListYAML renders a config whose backend list is exactly backends, in
// order, at the fixed listen address.
func backendListYAML(backends ...BackendConfig) string {
	yaml := "listen: \":8080\"\nbackends:\n"
	for _, b := range backends {
		yaml += backendYAML(b.Name, b.URL)
	}
	return yaml
}

// oneBackendConfigYAML renders a single-backend config from raw top-level YAML
// lines (which must include listen) placed before the backends list.
func oneBackendConfigYAML(top string) string {
	return top + "backends:\n" + backendYAML("backend-a", "http://127.0.0.1:9001")
}

func mustConfig(t *testing.T, yaml string) *Config {
	t.Helper()
	cfg, err := loadAndValidate(t, yaml)
	require.NoError(t, err)
	return cfg
}

func TestDiffBackends(t *testing.T) {
	a1 := bc("backend-a", "http://127.0.0.1:9001")
	a2 := bc("backend-a", "http://127.0.0.1:9003")
	b := bc("backend-b", "http://127.0.0.1:9002")
	c := bc("backend-c", "http://127.0.0.1:9004")
	c2 := bc("backend-c", "http://127.0.0.1:9006")
	d := bc("backend-d", "http://127.0.0.1:9005")

	cases := []struct {
		name          string
		old, new      []BackendConfig
		wantAdded     []BackendConfig
		wantRemoved   []BackendConfig
		wantUnchanged []BackendConfig
	}{
		{
			name:          "identical",
			old:           []BackendConfig{a1, b},
			new:           []BackendConfig{a1, b},
			wantUnchanged: []BackendConfig{a1, b},
		},
		{
			name:          "pure add appends in new order",
			old:           []BackendConfig{a1},
			new:           []BackendConfig{a1, b},
			wantAdded:     []BackendConfig{b},
			wantUnchanged: []BackendConfig{a1},
		},
		{
			name:          "pure remove keeps old order",
			old:           []BackendConfig{a1, b},
			new:           []BackendConfig{a1},
			wantRemoved:   []BackendConfig{b},
			wantUnchanged: []BackendConfig{a1},
		},
		{
			name:          "url change under one name is one removed plus one added",
			old:           []BackendConfig{a1, b},
			new:           []BackendConfig{a2, b},
			wantAdded:     []BackendConfig{a2},
			wantRemoved:   []BackendConfig{a1},
			wantUnchanged: []BackendConfig{b},
		},
		{
			name:          "reorder only is all unchanged in new order",
			old:           []BackendConfig{a1, b},
			new:           []BackendConfig{b, a1},
			wantUnchanged: []BackendConfig{b, a1},
		},
		{
			name:          "mixed add remove and url change",
			old:           []BackendConfig{a1, b, c},
			new:           []BackendConfig{c2, b, d},
			wantAdded:     []BackendConfig{c2, d},
			wantRemoved:   []BackendConfig{a1, c},
			wantUnchanged: []BackendConfig{b},
		},
		{
			name:          "unchanged and added each keep new order",
			old:           []BackendConfig{a1, c},
			new:           []BackendConfig{c, b, a1},
			wantAdded:     []BackendConfig{b},
			wantUnchanged: []BackendConfig{c, a1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldCfg := mustConfig(t, backendListYAML(tc.old...))
			newCfg := mustConfig(t, backendListYAML(tc.new...))

			got := DiffBackends(oldCfg, newCfg)

			assert.Equal(t, tc.wantAdded, got.Added, "added")
			assert.Equal(t, tc.wantRemoved, got.Removed, "removed")
			assert.Equal(t, tc.wantUnchanged, got.Unchanged, "unchanged")
		})
	}
}

func TestNonBackendChanges(t *testing.T) {
	base := "listen: \":8080\"\n"

	cases := []struct {
		name string
		old  string
		new  string
		want []string
	}{
		{
			name: "identical",
			old:  oneBackendConfigYAML(base),
			new:  oneBackendConfigYAML(base),
		},
		{
			name: "listen differs",
			old:  oneBackendConfigYAML(base),
			new:  oneBackendConfigYAML("listen: \":8081\"\n"),
			want: []string{"listen"},
		},
		{
			name: "algorithm differs",
			old:  oneBackendConfigYAML(base),
			new:  oneBackendConfigYAML(base + "algorithm: \"least_conn\"\n"),
			want: []string{"algorithm"},
		},
		{
			name: "health probe_interval differs",
			old:  oneBackendConfigYAML(base),
			new:  oneBackendConfigYAML(base + "health:\n  probe_interval: \"1s\"\n"),
			want: []string{"health"},
		},
		{
			name: "health probe_timeout differs",
			old:  oneBackendConfigYAML(base),
			new:  oneBackendConfigYAML(base + "health:\n  probe_timeout: \"1s\"\n"),
			want: []string{"health"},
		},
		{
			name: "circuit cooldown differs",
			old:  oneBackendConfigYAML(base),
			new:  oneBackendConfigYAML(base + "circuit:\n  cooldown: \"10s\"\n"),
			want: []string{"circuit"},
		},
		{
			name: "metrics listen differs",
			old:  oneBackendConfigYAML(base),
			new:  oneBackendConfigYAML(base + "metrics:\n  listen: \":19191\"\n"),
			want: []string{"metrics"},
		},
		{
			name: "health_endpoint listen differs",
			old:  oneBackendConfigYAML(base),
			new:  oneBackendConfigYAML(base + "health_endpoint:\n  listen: \":18081\"\n"),
			want: []string{"health_endpoint"},
		},
		{
			name: "omitted fields equal explicit defaults",
			old:  oneBackendConfigYAML(base),
			new: oneBackendConfigYAML(base +
				"algorithm: \"round_robin\"\n" +
				"health:\n  probe_interval: \"5s\"\n  probe_timeout: \"2s\"\n" +
				"circuit:\n  cooldown: \"30s\"\n" +
				"metrics:\n  listen: \":9090\"\n" +
				"health_endpoint:\n  listen: \":8081\"\n"),
		},
		{
			name: "multiple fields come back in fixed order",
			old:  oneBackendConfigYAML(base),
			new: oneBackendConfigYAML("listen: \":8081\"\n" +
				"algorithm: \"least_conn\"\n" +
				"metrics:\n  listen: \":19191\"\n"),
			want: []string{"listen", "algorithm", "metrics"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldCfg := mustConfig(t, tc.old)
			newCfg := mustConfig(t, tc.new)

			assert.Equal(t, tc.want, NonBackendChanges(oldCfg, newCfg))
		})
	}
}
