package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func backendYAML(name, url string) string {
	return "  - name: \"" + name + "\"\n    url: \"" + url + "\"\n"
}

func singleBackendYAML(url string) string {
	return "listen: \":8080\"\nbackends:\n" + backendYAML("backend-a", url)
}

func threeBackendYAML() string {
	return "listen: \":8080\"\nbackends:\n" +
		backendYAML("backend-a", "http://127.0.0.1:9001") +
		backendYAML("backend-b", "http://127.0.0.1:9002") +
		backendYAML("backend-c", "http://127.0.0.1:9003")
}

// configWithSprint3YAML builds a minimal otherwise-valid config with the
// supplied health and circuit YAML blocks (each may be empty) spliced in
// before the backends section.
func configWithSprint3YAML(health, circuit string) string {
	return "listen: \":8080\"\n" + health + circuit +
		"backends:\n" + backendYAML("backend-a", "http://127.0.0.1:9001")
}

// configWithMetricsYAML builds a minimal otherwise-valid config with the
// supplied metrics YAML block (which may be empty) spliced in before the
// backends section.
func configWithMetricsYAML(metrics string) string {
	return "listen: \":8080\"\n" + metrics +
		"backends:\n" + backendYAML("backend-a", "http://127.0.0.1:9001")
}

// loadAndValidate is the end-to-end seam under test: YAML -> Load -> Validate.
func loadAndValidate(t *testing.T, contents string) (*Config, error) {
	t.Helper()
	cfg, err := Load(writeTempConfig(t, contents))
	require.NoError(t, err, "Load must succeed for syntactically valid YAML")
	return cfg, cfg.Validate()
}

type validateCase struct {
	name      string
	yaml      string
	wantErr   bool
	errSubstr string
	check     func(t *testing.T, cfg *Config)
}

func TestValidate(t *testing.T) {
	cases := []validateCase{
		{
			name:      "missing listen",
			yaml:      "listen: \"\"\nbackends:\n" + backendYAML("backend-a", "http://127.0.0.1:9001"),
			wantErr:   true,
			errSubstr: "listen",
		},
		{
			name:      "listen without port",
			yaml:      "listen: \"foobar\"\nbackends:\n" + backendYAML("backend-a", "http://127.0.0.1:9001"),
			wantErr:   true,
			errSubstr: "listen",
		},
		{
			name:      "listen port out of uint16 range",
			yaml:      "listen: \":99999\"\nbackends:\n" + backendYAML("backend-a", "http://127.0.0.1:9001"),
			wantErr:   true,
			errSubstr: "listen",
		},
		{
			name:      "zero backends",
			yaml:      "listen: \":8080\"\nbackends: []\n",
			wantErr:   true,
			errSubstr: "backend",
		},
		{
			name:      "backend URL without scheme",
			yaml:      singleBackendYAML("localhost:9001"),
			wantErr:   true,
			errSubstr: "scheme",
		},
		{
			name:      "backend URL with non-http scheme",
			yaml:      singleBackendYAML("ftp://127.0.0.1:9001"),
			wantErr:   true,
			errSubstr: "scheme",
		},
		{
			name:      "backend URL with uppercase HTTP scheme",
			yaml:      singleBackendYAML("HTTP://backend:9001"),
			wantErr:   true,
			errSubstr: "scheme",
		},
		{
			name:      "backend URL with uppercase HTTPS scheme",
			yaml:      singleBackendYAML("HTTPS://backend:9001"),
			wantErr:   true,
			errSubstr: "scheme",
		},
		{
			name:      "backend URL without host",
			yaml:      singleBackendYAML("http://"),
			wantErr:   true,
			errSubstr: "host",
		},
		{
			name:      "backend URL with query string",
			yaml:      singleBackendYAML("http://127.0.0.1:9001?debug=1"),
			wantErr:   true,
			errSubstr: "query",
		},
		{
			name:      "backend URL with fragment",
			yaml:      singleBackendYAML("http://127.0.0.1:9001#foo"),
			wantErr:   true,
			errSubstr: "fragment",
		},
		{
			name:      "duplicate backend names",
			yaml:      "listen: \":8080\"\nbackends:\n" + backendYAML("a", "http://127.0.0.1:9001") + backendYAML("a", "http://127.0.0.1:9002"),
			wantErr:   true,
			errSubstr: "duplicate",
		},
		{
			name:      "empty backend name",
			yaml:      "listen: \":8080\"\nbackends:\n" + backendYAML("", "http://127.0.0.1:9001"),
			wantErr:   true,
			errSubstr: "name",
		},
		{
			name:      "backend name with invalid characters",
			yaml:      "listen: \":8080\"\nbackends:\n" + backendYAML("backend.primary", "http://127.0.0.1:9001"),
			wantErr:   true,
			errSubstr: "name",
		},
		{
			name:      "unknown algorithm",
			yaml:      "listen: \":8080\"\nalgorithm: \"random\"\nbackends:\n" + backendYAML("backend-a", "http://127.0.0.1:9001"),
			wantErr:   true,
			errSubstr: "algorithm",
		},
		{
			name: "p2c_ewma is accepted",
			yaml: "listen: \":8080\"\nalgorithm: \"p2c_ewma\"\nbackends:\n" + backendYAML("backend-a", "http://127.0.0.1:9001"),
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				assert.Equal(t, AlgorithmP2CEWMA, cfg.Algorithm)
			},
		},
		{
			name: "consistent_hash is accepted",
			yaml: "listen: \":8080\"\nalgorithm: \"consistent_hash\"\nbackends:\n" + backendYAML("backend-a", "http://127.0.0.1:9001"),
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				assert.Equal(t, AlgorithmConsistentHash, cfg.Algorithm)
			},
		},
		{
			name:      "algorithm is case-sensitive",
			yaml:      "listen: \":8080\"\nalgorithm: \"Round_Robin\"\nbackends:\n" + backendYAML("backend-a", "http://127.0.0.1:9001"),
			wantErr:   true,
			errSubstr: "algorithm",
		},
		{
			name: "algorithm omitted defaults to round_robin",
			yaml: threeBackendYAML(),
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				assert.Equal(t, AlgorithmRoundRobin, cfg.Algorithm)
			},
		},
		{
			name: "empty algorithm defaults to round_robin",
			yaml: "listen: \":8080\"\nalgorithm: \"\"\nbackends:\n" + backendYAML("backend-a", "http://127.0.0.1:9001"),
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				assert.Equal(t, AlgorithmRoundRobin, cfg.Algorithm)
			},
		},
		{
			name: "least_conn is accepted",
			yaml: "listen: \":8080\"\nalgorithm: \"least_conn\"\nbackends:\n" + backendYAML("backend-a", "http://127.0.0.1:9001"),
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				assert.Equal(t, AlgorithmLeastConn, cfg.Algorithm)
			},
		},
		{
			name: "https scheme and backend path are allowed",
			yaml: "listen: \":8080\"\nbackends:\n" + backendYAML("backend-a", "https://internal:9001/v2"),
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				require.Len(t, cfg.Backends, 1)
				assert.Equal(t, "https://internal:9001/v2", cfg.Backends[0].URL)
			},
		},
		{
			name: "happy-path round-trip populates every field",
			yaml: threeBackendYAML(),
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				assert.Equal(t, ":8080", cfg.Listen)
				assert.Equal(t, AlgorithmRoundRobin, cfg.Algorithm)
				require.Len(t, cfg.Backends, 3)
				assert.Equal(t, []BackendConfig{
					{Name: "backend-a", URL: "http://127.0.0.1:9001"},
					{Name: "backend-b", URL: "http://127.0.0.1:9002"},
					{Name: "backend-c", URL: "http://127.0.0.1:9003"},
				}, cfg.Backends)
			},
		},
		{
			name: "Sprint 3 durations omitted fall back to documented defaults",
			yaml: threeBackendYAML(),
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				require.NotNil(t, cfg.Health.ProbeInterval)
				require.NotNil(t, cfg.Health.ProbeTimeout)
				require.NotNil(t, cfg.Circuit.Cooldown)
				assert.Equal(t, DefaultProbeInterval, *cfg.Health.ProbeInterval)
				assert.Equal(t, DefaultProbeTimeout, *cfg.Health.ProbeTimeout)
				assert.Equal(t, DefaultCircuitCooldown, *cfg.Circuit.Cooldown)
			},
		},
		{
			name: "Sprint 3 durations set explicitly are kept",
			yaml: configWithSprint3YAML(
				"health:\n  probe_interval: \"1500ms\"\n  probe_timeout: \"250ms\"\n",
				"circuit:\n  cooldown: \"10s\"\n",
			),
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				require.NotNil(t, cfg.Health.ProbeInterval)
				require.NotNil(t, cfg.Health.ProbeTimeout)
				require.NotNil(t, cfg.Circuit.Cooldown)
				assert.Equal(t, 1500*time.Millisecond, *cfg.Health.ProbeInterval)
				assert.Equal(t, 250*time.Millisecond, *cfg.Health.ProbeTimeout)
				assert.Equal(t, 10*time.Second, *cfg.Circuit.Cooldown)
			},
		},
		{
			name:      "Sprint 3 health probe interval of zero is rejected",
			yaml:      configWithSprint3YAML("health:\n  probe_interval: \"0s\"\n", ""),
			wantErr:   true,
			errSubstr: "probe_interval",
		},
		{
			name:      "Sprint 3 health probe interval negative is rejected",
			yaml:      configWithSprint3YAML("health:\n  probe_interval: \"-1s\"\n", ""),
			wantErr:   true,
			errSubstr: "probe_interval",
		},
		{
			name:      "Sprint 3 health probe timeout of zero is rejected",
			yaml:      configWithSprint3YAML("health:\n  probe_timeout: \"0s\"\n", ""),
			wantErr:   true,
			errSubstr: "probe_timeout",
		},
		{
			name:      "Sprint 3 health probe timeout negative is rejected",
			yaml:      configWithSprint3YAML("health:\n  probe_timeout: \"-500ms\"\n", ""),
			wantErr:   true,
			errSubstr: "probe_timeout",
		},
		{
			name:      "Sprint 3 circuit cooldown of zero is rejected",
			yaml:      configWithSprint3YAML("", "circuit:\n  cooldown: \"0s\"\n"),
			wantErr:   true,
			errSubstr: "cooldown",
		},
		{
			name:      "Sprint 3 circuit cooldown negative is rejected",
			yaml:      configWithSprint3YAML("", "circuit:\n  cooldown: \"-30s\"\n"),
			wantErr:   true,
			errSubstr: "cooldown",
		},
		{
			name: "metrics listen omitted defaults to :9090",
			yaml: threeBackendYAML(),
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				require.NotNil(t, cfg.Metrics.Listen)
				assert.Equal(t, DefaultMetricsListen, *cfg.Metrics.Listen)
			},
		},
		{
			name: "metrics listen set explicitly is kept",
			yaml: configWithMetricsYAML("metrics:\n  listen: \":19191\"\n"),
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				require.NotNil(t, cfg.Metrics.Listen)
				assert.Equal(t, ":19191", *cfg.Metrics.Listen)
			},
		},
		{
			name:      "metrics listen without port is rejected",
			yaml:      configWithMetricsYAML("metrics:\n  listen: \"foobar\"\n"),
			wantErr:   true,
			errSubstr: "metrics",
		},
		{
			name:      "metrics listen with empty value is rejected",
			yaml:      configWithMetricsYAML("metrics:\n  listen: \"\"\n"),
			wantErr:   true,
			errSubstr: "metrics",
		},
		{
			name:      "metrics listen with out-of-range port is rejected",
			yaml:      configWithMetricsYAML("metrics:\n  listen: \":99999\"\n"),
			wantErr:   true,
			errSubstr: "metrics",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadAndValidate(t, tc.yaml)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSubstr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, cfg)
			if tc.check != nil {
				tc.check(t, cfg)
			}
		})
	}
}
