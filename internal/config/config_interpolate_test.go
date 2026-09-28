package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadEnvInterpolation covers S4.T16: ${VAR} expansion in backend URLs at
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
			env:     map[string]string{"T16_HOST": "127.0.0.1", "T16_PORT": "9001"},
			url:     "http://${T16_HOST}:${T16_PORT}",
			wantURL: "http://127.0.0.1:9001",
		},
		{
			name:    "multiple variables across credentials and host",
			env:     map[string]string{"T16_USER": "alice", "T16_PASS": "s3cr3t", "T16_HOST": "example.com", "T16_PORT": "8080"},
			url:     "http://${T16_USER}:${T16_PASS}@${T16_HOST}:${T16_PORT}",
			wantURL: "http://alice:s3cr3t@example.com:8080",
		},
		{
			name:    "variable adjacent to literal text",
			env:     map[string]string{"T16_SUFFIX": "internal", "T16_PORT": "8080"},
			url:     "http://api-${T16_SUFFIX}:${T16_PORT}/v1",
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
  listen: "${T16_METRICS_LISTEN}"
backends:
  - name: "backend-a"
    url: "http://127.0.0.1:9001"
`,
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				require.NotNil(t, cfg.Metrics.Listen)
				assert.Equal(t, "${T16_METRICS_LISTEN}", *cfg.Metrics.Listen)
			},
		},
		{
			name:      "unset variable fails naming backend and variable",
			url:       "http://${T16_UNSET}:9001",
			wantErr:   true,
			errSubstr: []string{`"backend-a"`, "T16_UNSET"},
		},
		{
			name:      "empty variable fails naming backend and variable",
			env:       map[string]string{"T16_EMPTY": ""},
			url:       "http://${T16_EMPTY}:9001",
			wantErr:   true,
			errSubstr: []string{`"backend-a"`, "T16_EMPTY"},
		},
		{
			name:      "unterminated reference fails naming backend",
			url:       "http://127.0.0.1:${T16_PORT",
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
			url:       "http://127.0.0.1:${T16-PORT}",
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
    url: "http://${T16_MISSING}:9002"
`,
			wantErr:   true,
			errSubstr: []string{`"backend-b"`, "T16_MISSING"},
			notInErr:  []string{`"backend-a"`},
		},
		{
			name:      "failure error never contains an already-expanded value",
			env:       map[string]string{"T16_PASS": "sup3r-s3cr3t"},
			url:       "http://${T16_PASS}@host:9001/${T16-BAD}",
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
// literal typo (S4.T16).
func TestLoadEnvInterpolationResolvedURLIsValidated(t *testing.T) {
	t.Setenv("T16_GOOD", "127.0.0.1")
	t.Setenv("T16_BAD", "not-a-url")

	good, err := Load(writeTempConfig(t, singleBackendYAML("http://${T16_GOOD}:9001")))
	require.NoError(t, err)
	require.NoError(t, good.Validate())

	bad, err := Load(writeTempConfig(t, singleBackendYAML("${T16_BAD}")))
	require.NoError(t, err)
	require.Error(t, bad.Validate())
}

// TestLoadEnvInterpolationValidationErrorIsRedacted covers S4.T17: the
// Load-then-Validate pipeline main runs over an interpolated URL fails naming
// only the backend and the url field — neither the expanded value nor the
// resolved URL appears, so a secret in the environment cannot reach the logs
// through main's validation-error path. Load's own expansion errors already
// never embed a value (S4.T16); Validate is the secret-bearing path. The
// environment value here appears verbatim in url.Parse's pre-redaction message
// ("invalid port \":80a\" after host"), so the test fails if the raw URL is
// wrapped back in.
func TestLoadEnvInterpolationValidationErrorIsRedacted(t *testing.T) {
	const secret = "80a"
	t.Setenv("T17_BAD_PORT", secret)

	cfg, err := loadAndValidate(t, singleBackendYAML("http://127.0.0.1:${T17_BAD_PORT}"))
	require.Error(t, err)
	require.Len(t, cfg.Backends, 1)
	assert.Contains(t, err.Error(), "backend-a")
	assert.NotContains(t, err.Error(), secret)
	assert.NotContains(t, err.Error(), cfg.Backends[0].URL)
}

// TestLoadEnvInterpolationReloadIsRemovePlusAdd proves the reload semantics: a
// changed expansion is one removed plus one added backend, because identity is
// (name, URL) (ADR-0015, S4.T16).
func TestLoadEnvInterpolationReloadIsRemovePlusAdd(t *testing.T) {
	contents := singleBackendYAML("http://${T16_HOST}:9001")

	t.Setenv("T16_HOST", "host-old")
	oldCfg, err := Load(writeTempConfig(t, contents))
	require.NoError(t, err)

	t.Setenv("T16_HOST", "host-new")
	newCfg, err := Load(writeTempConfig(t, contents))
	require.NoError(t, err)

	diff := DiffBackends(oldCfg, newCfg)
	require.Len(t, diff.Removed, 1)
	require.Len(t, diff.Added, 1)
	assert.Empty(t, diff.Unchanged)
	assert.Equal(t, BackendConfig{Name: "backend-a", URL: "http://host-old:9001"}, diff.Removed[0])
	assert.Equal(t, BackendConfig{Name: "backend-a", URL: "http://host-new:9001"}, diff.Added[0])
}
