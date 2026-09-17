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

func TestLoad(t *testing.T) {
	t.Run("missing file wraps os.ErrNotExist", func(t *testing.T) {
		_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
		require.Error(t, err)
		assert.ErrorIs(t, err, os.ErrNotExist)
		assert.Contains(t, err.Error(), "config: load")
	})

	t.Run("invalid YAML syntax returns a decode error", func(t *testing.T) {
		path := writeTempConfig(t, "listen: \":8080\"\nbackends:\n  - name: \"a\"\n   url: \"http://127.0.0.1:9001\"\n")
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config: load")
	})

	t.Run("unknown field is rejected", func(t *testing.T) {
		path := writeTempConfig(t, "listn: \":8080\"\n")
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "listn")
	})

	t.Run("successful load populates fields without defaulting or validating", func(t *testing.T) {
		path := writeTempConfig(t, `listen: ":9090"
algorithm: "least_conn"
backends:
  - name: "a"
    url: "http://127.0.0.1:9001"
  - name: "b"
    url: "http://127.0.0.1:9002"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Equal(t, ":9090", cfg.Listen)
		assert.Equal(t, "least_conn", cfg.Algorithm)
		require.Len(t, cfg.Backends, 2)
		assert.Equal(t, BackendConfig{Name: "a", URL: "http://127.0.0.1:9001"}, cfg.Backends[0])
		assert.Equal(t, BackendConfig{Name: "b", URL: "http://127.0.0.1:9002"}, cfg.Backends[1])
	})

	t.Run("omitted algorithm is left empty by Load", func(t *testing.T) {
		path := writeTempConfig(t, "listen: \":8080\"\n")
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Empty(t, cfg.Algorithm)
	})

	t.Run("empty backends are left empty by Load", func(t *testing.T) {
		path := writeTempConfig(t, "listen: \":8080\"\nbackends: []\n")
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Empty(t, cfg.Backends)
	})
}
