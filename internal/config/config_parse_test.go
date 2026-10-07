package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParse covers Parse, the reader-based decode that Load wraps and the
// reload admin endpoint calls with a request body (ADR-0015
// decision 13). It must be exactly as strict as Load: unknown fields are
// rejected, ${VAR} references in backend URLs are expanded, and errors name the
// source the caller passed rather than a file path.
func TestParse(t *testing.T) {
	t.Setenv("PARSE_TEST_HOST", "10.0.0.7")

	tests := []struct {
		name      string
		body      string
		wantErr   bool
		errSubstr string
		check     func(t *testing.T, cfg *Config)
	}{
		{
			name: "valid body decodes",
			body: "listen: \":8080\"\nalgorithm: \"least_conn\"\nbackends:\n  - name: \"a\"\n    url: \"http://127.0.0.1:9001\"\n",
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, ":8080", cfg.Listen)
				assert.Equal(t, AlgorithmLeastConn, cfg.Algorithm)
				require.Len(t, cfg.Backends, 1)
				assert.Equal(t, "http://127.0.0.1:9001", cfg.Backends[0].URL)
			},
		},
		{
			name: "backend url references are expanded",
			body: "listen: \":8080\"\nbackends:\n  - name: \"a\"\n    url: \"http://${PARSE_TEST_HOST}:9001\"\n",
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "http://10.0.0.7:9001", cfg.Backends[0].URL)
			},
		},
		{
			name:      "unknown field is rejected and names the source",
			body:      "listen: \":8080\"\nlistn: \":8081\"\n",
			wantErr:   true,
			errSubstr: "config: parse test body",
		},
		{
			name:      "malformed yaml is rejected",
			body:      "listen: [\n",
			wantErr:   true,
			errSubstr: "config: parse test body",
		},
		{
			name:      "empty body is rejected",
			body:      "",
			wantErr:   true,
			errSubstr: "config: parse test body",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse(strings.NewReader(tc.body), "test body")
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSubstr)
				return
			}
			require.NoError(t, err)
			tc.check(t, cfg)
		})
	}
}

// TestLoadErrorStillNamesThePath pins that moving the decode into Parse did not
// change Load's error format: a file error still reads "config: load <path>".
func TestLoadErrorStillNamesThePath(t *testing.T) {
	path := writeTempConfig(t, "listn: \":8080\"\n")
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config: load "+path)
}
