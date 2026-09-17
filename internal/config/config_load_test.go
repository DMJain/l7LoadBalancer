package config

import (
	"os"
	"path/filepath"
	"testing"

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
