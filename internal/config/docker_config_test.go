package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDockerConfig proves the shipped configs/docker.yaml — the default baked
// into the container image at /etc/l7lb/config.yaml (S3.T10) — round-trips
// through the real Load + Validate pipeline, so the image's default config can
// never drift out of sync with the schema or validation rules. Unlike
// example.yaml (the host-process default, which points at 127.0.0.1:9001-3),
// the container config must resolve its backends by compose service name on
// the default bridge network, so the host assertion has teeth: a container
// probing 127.0.0.1 would only ever find itself.
func TestDockerConfig(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "configs", "docker.yaml"))
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	assert.Equal(t, ":8080", cfg.Listen)
	require.Len(t, cfg.Backends, 3)

	want := map[string]string{
		"backend-a": "http://backend-a:8080",
		"backend-b": "http://backend-b:8080",
		"backend-c": "http://backend-c:8080",
	}
	for _, b := range cfg.Backends {
		assert.Equalf(t, want[b.Name], b.URL, "backend %q must use its compose service name", b.Name)
		assert.NotContainsf(t, b.URL, "127.0.0.1", "backend %q must not point at the container itself", b.Name)
	}

	assert.Equal(t, ":9090", *cfg.Metrics.Listen)
	assert.Equal(t, ":8081", *cfg.HealthEndpoint.Listen)
}
