package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// TestBuildTransportHTTP2Settings pins the scheme-driven HTTP/2 transport
// construction (S5.T3-main): ForceAttemptHTTP2 follows force_http2, and
// tls_skip_verify installs a TLSClientConfig whose InsecureSkipVerify is set.
// The explicit ForceAttemptHTTP2 is the point of the test: Go silently drops
// automatic HTTP/2 the moment TLSClientConfig is customized, so leaving it
// unset would fall back to HTTP/1.1 with no error.
func TestBuildTransportHTTP2Settings(t *testing.T) {
	backend := config.BackendConfig{Name: "backend-a", URL: "https://127.0.0.1:9001"}

	t.Run("defaults negotiate http/2 and verify certificates", func(t *testing.T) {
		cfg := testConfig([]config.BackendConfig{backend})
		require.NoError(t, cfg.Validate())

		tr := buildTransport(cfg)

		assert.True(t, tr.ForceAttemptHTTP2, "force_http2 defaults to true")
		assert.Nil(t, tr.TLSClientConfig, "certificate verification must stay on by default")
	})

	t.Run("tls_skip_verify keeps http/2 despite a custom TLSClientConfig", func(t *testing.T) {
		cfg := testConfig([]config.BackendConfig{backend})
		cfg.Transport.TLSSkipVerify = true
		require.NoError(t, cfg.Validate())

		tr := buildTransport(cfg)

		require.NotNil(t, tr.TLSClientConfig, "tls_skip_verify must install a TLSClientConfig")
		assert.True(t, tr.TLSClientConfig.InsecureSkipVerify)
		assert.True(t, tr.ForceAttemptHTTP2,
			"a custom TLSClientConfig disables automatic HTTP/2 unless ForceAttemptHTTP2 is explicitly true")
	})

	t.Run("force_http2 false disables http/2 to backends", func(t *testing.T) {
		forceOff := false
		cfg := testConfig([]config.BackendConfig{backend})
		cfg.Transport.ForceHTTP2 = &forceOff
		require.NoError(t, cfg.Validate())

		tr := buildTransport(cfg)

		assert.False(t, tr.ForceAttemptHTTP2)
	})
}

// h2Backend starts a TLS httptest backend that negotiates HTTP/2 and reports
// the protocol it saw for each request. EnableHTTP2 must be set before
// StartTLS: httptest.NewTLSServer alone serves only HTTP/1.1.
func h2Backend(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()
	seen := make(chan string, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Proto
		w.WriteHeader(http.StatusOK)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, func() string { return <-seen }
}

// TestTransportNegotiatesHTTP2ToHTTPSBackend proves the end-to-end promise of
// the ticket: an https:// backend URL reaches the backend over HTTP/2 via ALPN
// when tls_skip_verify permits the self-signed test certificate.
func TestTransportNegotiatesHTTP2ToHTTPSBackend(t *testing.T) {
	silenceDefault(t)

	backend, sawProto := h2Backend(t)

	cfg := testConfig([]config.BackendConfig{{Name: "backend-a", URL: backend.URL}})
	cfg.Transport.TLSSkipVerify = true
	require.NoError(t, cfg.Validate())

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	application.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, "HTTP/2.0", sawProto(), "an https:// backend must be reached over HTTP/2 via ALPN")
}

// TestTransportKeepsHTTP11ToHTTPBackend proves scheme selection stays per-URL:
// an http:// backend still receives HTTP/1.1 even though ForceAttemptHTTP2 is
// on by default, because force_http2 only affects TLS connections.
func TestTransportKeepsHTTP11ToHTTPBackend(t *testing.T) {
	silenceDefault(t)

	seen := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Proto
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	cfg := testConfig([]config.BackendConfig{{Name: "backend-a", URL: backend.URL}})
	require.NoError(t, cfg.Validate())

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	application.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, "HTTP/1.1", <-seen, "force_http2 must not attempt http/2 on an http:// backend")
}
