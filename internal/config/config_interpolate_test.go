package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadEnvInterpolation covers ${VAR} expansion in backend URLs at
// Load time. It uses t.Setenv and therefore never calls t.Parallel, because it
// mutates the process environment.
func TestLoadEnvInterpolation(t *testing.T) {
	cases := []struct {
		name      string
		env       map[string]string
		url       string
		yaml      string
		wantURL   string
		wantErr   bool
		errSubstr []string
		notInErr  []string
		check     func(t *testing.T, cfg *Config)
	}{
		{
			name:    "host and port positions",
			env:     map[string]string{"INTERP_HOST": "127.0.0.1", "INTERP_PORT": "9001"},
			url:     "http://${INTERP_HOST}:${INTERP_PORT}",
			wantURL: "http://127.0.0.1:9001",
		},
		{
			name:    "multiple variables across credentials and host",
			env:     map[string]string{"INTERP_USER": "alice", "INTERP_PASS": "s3cr3t", "INTERP_HOST": "example.com", "INTERP_PORT": "8080"},
			url:     "http://${INTERP_USER}:${INTERP_PASS}@${INTERP_HOST}:${INTERP_PORT}",
			wantURL: "http://alice:s3cr3t@example.com:8080",
		},
		{
			name:    "variable adjacent to literal text",
			env:     map[string]string{"INTERP_SUFFIX": "internal", "INTERP_PORT": "8080"},
			url:     "http://api-${INTERP_SUFFIX}:${INTERP_PORT}/v1",
			wantURL: "http://api-internal:8080/v1",
		},
		{
			name:    "no reference passes through unchanged",
			url:     "http://127.0.0.1:9001",
			wantURL: "http://127.0.0.1:9001",
		},
		{
			name: "non-backend field passes through untouched",
			yaml: `listen: ":8080"
metrics:
  listen: "${METRICS_LISTEN_VAR}"
backends:
  - name: "backend-a"
    url: "http://127.0.0.1:9001"
`,
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				require.NotNil(t, cfg.Metrics.Listen)
				assert.Equal(t, "${METRICS_LISTEN_VAR}", *cfg.Metrics.Listen)
			},
		},
		{
			name:      "unset variable fails naming backend and variable",
			url:       "http://${UNSET_HOST_VAR}:9001",
			wantErr:   true,
			errSubstr: []string{`"backend-a"`, "UNSET_HOST_VAR"},
		},
		{
			name:      "empty variable fails naming backend and variable",
			env:       map[string]string{"EMPTY_VAR": ""},
			url:       "http://${EMPTY_VAR}:9001",
			wantErr:   true,
			errSubstr: []string{`"backend-a"`, "EMPTY_VAR"},
		},
		{
			name:      "unterminated reference fails naming backend",
			url:       "http://127.0.0.1:${INTERP_PORT",
			wantErr:   true,
			errSubstr: []string{`"backend-a"`},
		},
		{
			name:      "empty reference fails naming backend",
			url:       "http://127.0.0.1:${}",
			wantErr:   true,
			errSubstr: []string{`"backend-a"`},
		},
		{
			name:      "invalid name character fails naming backend",
			url:       "http://127.0.0.1:${INTERP-PORT}",
			wantErr:   true,
			errSubstr: []string{`"backend-a"`},
		},
		{
			name: "failure names the offending backend among several",
			yaml: `listen: ":8080"
backends:
  - name: "backend-a"
    url: "http://127.0.0.1:9001"
  - name: "backend-b"
    url: "http://${MISSING_VAR}:9002"
`,
			wantErr:   true,
			errSubstr: []string{`"backend-b"`, "MISSING_VAR"},
			notInErr:  []string{`"backend-a"`},
		},
		{
			name:      "failure error never contains an already-expanded value",
			env:       map[string]string{"INTERP_PASS": "sup3r-s3cr3t"},
			url:       "http://${INTERP_PASS}@host:9001/${INTERP-BAD}",
			wantErr:   true,
			errSubstr: []string{`"backend-a"`},
			notInErr:  []string{"sup3r-s3cr3t"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			contents := tc.yaml
			if contents == "" {
				contents = singleBackendYAML(tc.url)
			}
			cfg, err := Load(writeTempConfig(t, contents))
			if tc.wantErr {
				require.Error(t, err)
				for _, sub := range tc.errSubstr {
					assert.Contains(t, err.Error(), sub)
				}
				for _, bad := range tc.notInErr {
					assert.NotContains(t, err.Error(), bad)
				}
				return
			}

			require.NoError(t, err)
			if tc.check != nil {
				tc.check(t, cfg)
				return
			}
			require.Len(t, cfg.Backends, 1)
			assert.Equal(t, tc.wantURL, cfg.Backends[0].URL)
		})
	}
}

// TestLoadEnvInterpolationResolvedURLIsValidated proves the resolved URL is
// what Validate checks, so a typo in an expanded value fails exactly like a
// literal typo.
func TestLoadEnvInterpolationResolvedURLIsValidated(t *testing.T) {
	t.Setenv("GOOD_URL_VAR", "127.0.0.1")
	t.Setenv("BAD_URL_VAR", "not-a-url")

	good, err := Load(writeTempConfig(t, singleBackendYAML("http://${GOOD_URL_VAR}:9001")))
	require.NoError(t, err)
	require.NoError(t, good.Validate())

	bad, err := Load(writeTempConfig(t, singleBackendYAML("${BAD_URL_VAR}")))
	require.NoError(t, err)
	require.Error(t, bad.Validate())
}

// TestLoadEnvInterpolationValidationErrorIsRedacted covers the
// Load-then-Validate pipeline main runs over an interpolated URL fails naming
// only the backend and the url field — neither the expanded value nor the
// resolved URL appears, so a secret in the environment cannot reach the logs
// through main's validation-error path. Load's own expansion errors already
// never embed a value; Validate is the secret-bearing path. The
// environment value here appears verbatim in url.Parse's pre-redaction message
// ("invalid port \":80a\" after host"), so the test fails if the raw URL is
// wrapped back in.
func TestLoadEnvInterpolationValidationErrorIsRedacted(t *testing.T) {
	const secret = "80a"
	t.Setenv("BAD_PORT_VAR", secret)

	cfg, err := loadAndValidate(t, singleBackendYAML("http://127.0.0.1:${BAD_PORT_VAR}"))
	require.Error(t, err)
	require.Len(t, cfg.Backends, 1)
	assert.Contains(t, err.Error(), "backend-a")
	assert.NotContains(t, err.Error(), secret)
	assert.NotContains(t, err.Error(), cfg.Backends[0].URL)
}

// TestLoadEnvInterpolationReloadIsRemovePlusAdd proves the reload semantics: a
// changed expansion is one removed plus one added backend, because identity is
// (name, URL) (ADR-0015).
func TestLoadEnvInterpolationReloadIsRemovePlusAdd(t *testing.T) {
	contents := singleBackendYAML("http://${INTERP_HOST}:9001")

	t.Setenv("INTERP_HOST", "host-old")
	oldCfg, err := Load(writeTempConfig(t, contents))
	require.NoError(t, err)

	t.Setenv("INTERP_HOST", "host-new")
	newCfg, err := Load(writeTempConfig(t, contents))
	require.NoError(t, err)

	diff := DiffBackends(oldCfg, newCfg)
	require.Len(t, diff.Removed, 1)
	require.Len(t, diff.Added, 1)
	assert.Empty(t, diff.Unchanged)
	assert.Equal(t, BackendConfig{Name: "backend-a", URL: "http://host-old:9001"}, diff.Removed[0])
	assert.Equal(t, BackendConfig{Name: "backend-a", URL: "http://host-new:9001"}, diff.Added[0])
}
