package config

import (
	"testing"

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
			name:      "unimplemented algorithm p2c_ewma",
			yaml:      "listen: \":8080\"\nalgorithm: \"p2c_ewma\"\nbackends:\n" + backendYAML("backend-a", "http://127.0.0.1:9001"),
			wantErr:   true,
			errSubstr: "algorithm",
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
