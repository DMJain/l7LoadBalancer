package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTempConfig writes contents to a config.yaml inside a per-test temp dir
// and returns its path.
func writeTempConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

type loadCase struct {
	name      string
	contents  string
	missing   bool
	wantErr   bool
	errSubstr string
	errIs     error
	check     func(t *testing.T, cfg *Config)
}

func TestLoad(t *testing.T) {
	cases := []loadCase{
		{
			name:      "missing file wraps os.ErrNotExist",
			missing:   true,
			wantErr:   true,
			errSubstr: "config: load",
			errIs:     os.ErrNotExist,
		},
		{
			name:      "invalid YAML syntax returns a decode error",
			contents:  "listen: \":8080\"\nbackends:\n  - name: \"a\"\n   url: \"http://127.0.0.1:9001\"\n",
			wantErr:   true,
			errSubstr: "config: load",
		},
		{
			name:      "unknown field is rejected",
			contents:  "listn: \":8080\"\n",
			wantErr:   true,
			errSubstr: "listn",
		},
		{
			name: "successful load populates fields without defaulting or validating",
			contents: `listen: ":9090"
algorithm: "least_conn"
backends:
  - name: "a"
    url: "http://127.0.0.1:9001"
  - name: "b"
    url: "http://127.0.0.1:9002"
`,
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				assert.Equal(t, ":9090", cfg.Listen)
				assert.Equal(t, "least_conn", cfg.Algorithm)
				require.Len(t, cfg.Backends, 2)
				assert.Equal(t, BackendConfig{Name: "a", URL: "http://127.0.0.1:9001"}, cfg.Backends[0])
				assert.Equal(t, BackendConfig{Name: "b", URL: "http://127.0.0.1:9002"}, cfg.Backends[1])
			},
		},
		{
			name:     "omitted algorithm is left empty by Load",
			contents: "listen: \":8080\"\n",
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				assert.Empty(t, cfg.Algorithm)
			},
		},
		{
			name:     "empty backends are left empty by Load",
			contents: "listen: \":8080\"\nbackends: []\n",
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				assert.Empty(t, cfg.Backends)
			},
		},
		{
			name: "Sprint 3 duration fields decode from YAML strings without defaulting",
			contents: `listen: ":8080"
health:
  probe_interval: "1500ms"
  probe_timeout: "250ms"
circuit:
  cooldown: "10s"
backends:
  - name: "a"
    url: "http://127.0.0.1:9001"
`,
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
			name:     "omitted Sprint 3 durations are left nil by Load",
			contents: "listen: \":8080\"\n",
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				assert.Nil(t, cfg.Health.ProbeInterval)
				assert.Nil(t, cfg.Health.ProbeTimeout)
				assert.Nil(t, cfg.Circuit.Cooldown)
			},
		},
		{
			name:      "unknown field inside health section is rejected",
			contents:  "listen: \":8080\"\nhealth:\n  probe_intervall: \"1s\"\n",
			wantErr:   true,
			errSubstr: "probe_intervall",
		},
		{
			name:      "unknown field inside circuit section is rejected",
			contents:  "listen: \":8080\"\ncircuit:\n  cooldwn: \"1s\"\n",
			wantErr:   true,
			errSubstr: "cooldwn",
		},
		{
			name:     "metrics listen decodes from YAML without defaulting",
			contents: "listen: \":8080\"\nmetrics:\n  listen: \":19191\"\n",
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				require.NotNil(t, cfg.Metrics.Listen)
				assert.Equal(t, ":19191", *cfg.Metrics.Listen)
			},
		},
		{
			name:     "omitted metrics listen is left nil by Load",
			contents: "listen: \":8080\"\n",
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				assert.Nil(t, cfg.Metrics.Listen)
			},
		},
		{
			name:      "unknown field inside metrics section is rejected",
			contents:  "listen: \":8080\"\nmetrics:\n  listn: \":9090\"\n",
			wantErr:   true,
			errSubstr: "listn",
		},
		{
			name:     "health_endpoint listen decodes from YAML without defaulting",
			contents: "listen: \":8080\"\nhealth_endpoint:\n  listen: \":18081\"\n",
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				require.NotNil(t, cfg.HealthEndpoint.Listen)
				assert.Equal(t, ":18081", *cfg.HealthEndpoint.Listen)
			},
		},
		{
			name:     "omitted health_endpoint listen is left nil by Load",
			contents: "listen: \":8080\"\n",
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				assert.Nil(t, cfg.HealthEndpoint.Listen)
			},
		},
		{
			name:      "unknown field inside health_endpoint section is rejected",
			contents:  "listen: \":8080\"\nhealth_endpoint:\n  listn: \":8081\"\n",
			wantErr:   true,
			errSubstr: "listn",
		},
		{
			name:     "reload drain_window decodes from YAML without defaulting",
			contents: "listen: \":8080\"\nreload:\n  drain_window: \"15s\"\n",
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				require.NotNil(t, cfg.Reload.DrainWindow)
				assert.Equal(t, 15*time.Second, *cfg.Reload.DrainWindow)
			},
		},
		{
			name:     "omitted reload drain_window is left nil by Load",
			contents: "listen: \":8080\"\n",
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				assert.Nil(t, cfg.Reload.DrainWindow)
			},
		},
		{
			name:      "unknown field inside reload section is rejected",
			contents:  "listen: \":8080\"\nreload:\n  drain_windw: \"15s\"\n",
			wantErr:   true,
			errSubstr: "drain_windw",
		},
		{
			name:      "malformed reload drain_window is rejected at load",
			contents:  "listen: \":8080\"\nreload:\n  drain_window: \"abc\"\n",
			wantErr:   true,
			errSubstr: "cannot unmarshal",
		},
		{
			name:     "server read_timeout decodes from YAML without defaulting",
			contents: "listen: \":8080\"\nserver:\n  read_timeout: \"90s\"\n",
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				require.NotNil(t, cfg.Server.ReadTimeout)
				assert.Equal(t, 90*time.Second, *cfg.Server.ReadTimeout)
			},
		},
		{
			name:     "omitted server read_timeout is left nil by Load",
			contents: "listen: \":8080\"\n",
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				assert.Nil(t, cfg.Server.ReadTimeout)
			},
		},
		{
			name:      "unknown field inside server section is rejected",
			contents:  "listen: \":8080\"\nserver:\n  read_timeoutt: \"1s\"\n",
			wantErr:   true,
			errSubstr: "read_timeoutt",
		},
		{
			name:      "malformed server read_timeout is rejected at load",
			contents:  "listen: \":8080\"\nserver:\n  read_timeout: \"abc\"\n",
			wantErr:   true,
			errSubstr: "cannot unmarshal",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "missing.yaml")
			if !tc.missing {
				path = writeTempConfig(t, tc.contents)
			}

			cfg, err := Load(path)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSubstr)
				if tc.errIs != nil {
					assert.ErrorIs(t, err, tc.errIs)
				}
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

// TestExampleConfig proves the shipped configs/example.yaml round-trips
// through the real Load + Validate pipeline, so the example can never drift
// out of sync with the schema or validation rules. The path is relative to
// this test file's package directory (internal/config).
func TestExampleConfig(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "configs", "example.yaml"))
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())
}
