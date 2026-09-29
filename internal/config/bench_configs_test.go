package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBenchConfigs proves the eight shipped benchmark configs (S5.T4-harness,
// issue 06) round-trip through the real Load + Validate pipeline, so a config
// the benchmark harness mounts can never drift out of sync with the schema or
// validation rules. Every file is self-contained by design (spec §31): literal
// backend URLs, no templating, readable in isolation. The table is explicit
// (not a directory walk) so a missing or renamed file fails loudly.
//
// The protocol directory is a client-facing listener-mode distinction only:
// http11/ configs are plain HTTP, h2/ configs select TLS+ALPN. In h2 mode the
// backends are https:// so the LB↔backend leg also negotiates HTTP/2 (S5.T3),
// with tls_skip_verify for the shared self-signed SAN cert.
func TestBenchConfigs(t *testing.T) {
	algorithmByToken := map[string]string{
		"roundrobin":      AlgorithmRoundRobin,
		"leastconn":       AlgorithmLeastConn,
		"consistent-hash": AlgorithmConsistentHash,
		"p2c-ewma":        AlgorithmP2CEWMA,
	}

	tests := []struct {
		proto string
		token string
	}{
		{"http11", "roundrobin"},
		{"http11", "leastconn"},
		{"http11", "consistent-hash"},
		{"http11", "p2c-ewma"},
		{"h2", "roundrobin"},
		{"h2", "leastconn"},
		{"h2", "consistent-hash"},
		{"h2", "p2c-ewma"},
	}

	for _, tt := range tests {
		t.Run(tt.proto+"/"+tt.token, func(t *testing.T) {
			path := filepath.Join("..", "..", "bench", "configs", tt.proto, tt.token+".yaml")
			cfg, err := Load(path)
			require.NoErrorf(t, err, "%s must load", path)
			require.NoErrorf(t, cfg.Validate(), "%s must validate", path)

			assert.Equal(t, ":8080", cfg.Listen)
			assert.Equal(t, algorithmByToken[tt.token], cfg.Algorithm, "algorithm must match the filename")
			require.Len(t, cfg.Backends, 4)

			if tt.proto == "h2" {
				require.NotNil(t, cfg.TLS, "h2 configs select TLS mode by presence")
				assert.Equal(t, "/certs/server.crt", cfg.TLS.CertFile)
				assert.Equal(t, "/certs/server.key", cfg.TLS.KeyFile)
				assert.False(t, cfg.H2C, "TLS and h2c are mutually exclusive")
				assert.True(t, cfg.Transport.TLSSkipVerify, "shared self-signed backend cert needs skip-verify")
				for _, b := range cfg.Backends {
					assert.Regexpf(t, `^https://backend[1-4]:8080$`, b.URL, "h2 backend %q must speak https", b.Name)
				}
				return
			}

			assert.Nil(t, cfg.TLS, "http11 configs are plain HTTP")
			assert.False(t, cfg.H2C)
			assert.False(t, cfg.Transport.TLSSkipVerify)
			for _, b := range cfg.Backends {
				assert.Regexpf(t, `^http://backend[1-4]:8080$`, b.URL, "http11 backend %q must speak plain http", b.Name)
			}
		})
	}
}
