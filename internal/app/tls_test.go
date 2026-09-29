package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// freeAddr reserves a loopback address and releases it, returning the address
// string for a server to bind. Run only takes a config Listen address (there is
// no way to hand it a listener), so the test needs a concrete free port.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// writeTestCert generates a self-signed server certificate for 127.0.0.1 and
// localhost into a per-test temp dir and returns the cert and key file paths.
// Tests generate their own cert rather than relying on scripts/generate-cert.sh
// so the suite stays hermetic (no openssl dependency).
func writeTestCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	dir := t.TempDir()
	certFile = filepath.Join(dir, "server.crt")
	keyFile = filepath.Join(dir, "server.key")

	certPEM, err := os.Create(certFile)
	require.NoError(t, err)
	require.NoError(t, pem.Encode(certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: der}))
	require.NoError(t, certPEM.Close())

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM, err := os.Create(keyFile)
	require.NoError(t, err)
	require.NoError(t, pem.Encode(keyPEM, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	require.NoError(t, keyPEM.Close())

	return certFile, keyFile
}

// tlsTestConfig builds a validated config whose client listener is TLS mode,
// with the given backend and idle timeout.
func tlsTestConfig(t *testing.T, backendURL string, idle *time.Duration) *config.Config {
	t.Helper()
	certFile, keyFile := writeTestCert(t)
	cfg := testConfig([]config.BackendConfig{{Name: "backend-a", URL: backendURL}})
	cfg.TLS = &config.TLSConfig{CertFile: certFile, KeyFile: keyFile}
	if idle != nil {
		cfg.Server.IdleTimeout = idle
	}
	require.NoError(t, cfg.Validate())
	return cfg
}

// TestBuildTLSModeServerConstruction proves Build loads the configured cert
// pair into the client server's TLSConfig when a tls: block is present, and
// that HTTP/2 mode disables ReadTimeout and takes IdleTimeout from config. The
// metrics and health listeners stay plain HTTP (S5.T1).
func TestBuildTLSModeServerConstruction(t *testing.T) {
	silenceDefault(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	idle := 45 * time.Second
	cfg := tlsTestConfig(t, backend.URL, &idle)

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	require.NotNil(t, application.srv.TLSConfig, "TLS mode must set the client server's TLSConfig")
	assert.Len(t, application.srv.TLSConfig.Certificates, 1)
	assert.Zero(t, application.srv.ReadTimeout, "ReadTimeout is architecturally wrong for HTTP/2 and must be disabled")
	assert.Equal(t, idle, application.srv.IdleTimeout, "HTTP/2 modes rely on IdleTimeout for cleanup")

	assert.Nil(t, application.metricsSrv.TLSConfig, "metrics endpoint stays plain HTTP")
	assert.Nil(t, application.healthSrv.TLSConfig, "health endpoint stays plain HTTP")
}

// TestBuildPlainModeServerConstruction pins the plain-HTTP path: no TLSConfig,
// ReadTimeout from config, and IdleTimeout from config (the knob is never
// accepted-but-ignored).
func TestBuildPlainModeServerConstruction(t *testing.T) {
	silenceDefault(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	read := 30 * time.Second
	idle := 75 * time.Second
	cfg := testConfig([]config.BackendConfig{{Name: "backend-a", URL: backend.URL}})
	cfg.Server.ReadTimeout = &read
	cfg.Server.IdleTimeout = &idle
	require.NoError(t, cfg.Validate())

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	assert.Nil(t, application.srv.TLSConfig)
	assert.Equal(t, read, application.srv.ReadTimeout)
	assert.Equal(t, idle, application.srv.IdleTimeout)
}

// TestBuildTLSMissingCertFails proves the cert pair is loaded at build time, so
// a bad path is a startup error rather than a silent plaintext listener.
func TestBuildTLSMissingCertFails(t *testing.T) {
	silenceDefault(t)

	cfg := testConfig([]config.BackendConfig{{Name: "backend-a", URL: "http://127.0.0.1:9001"}})
	cfg.TLS = &config.TLSConfig{
		CertFile: filepath.Join(t.TempDir(), "absent.crt"),
		KeyFile:  filepath.Join(t.TempDir(), "absent.key"),
	}
	require.NoError(t, cfg.Validate())

	_, err := Build(cfg, discardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls")
}

// TestRunTLSListenerNegotiatesH2AndServesHTTP2 proves App.Run's TLS branch
// (ListenAndServeTLS) accepts an ALPN handshake that negotiates h2 and serves a
// full request as HTTP/2.0 (S5.T1). It runs the real app on a free loopback
// port, so the http.Server's automatic HTTP/2 configuration is exercised end to
// end rather than by poking the server directly.
func TestRunTLSListenerNegotiatesH2AndServesHTTP2(t *testing.T) {
	silenceDefault(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "backend-a")
	}))
	t.Cleanup(backend.Close)

	cfg := tlsTestConfig(t, backend.URL, nil)
	cfg.Listen = freeAddr(t)
	require.NoError(t, cfg.Validate())

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatal("Run did not return after cancellation")
		}
	})

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, // test-only: generated cert
		ForceAttemptHTTP2: true,
	}}

	// Run starts its servers asynchronously; poll until the TLS listener answers.
	deadline := time.Now().Add(5 * time.Second)
	var resp *http.Response
	for {
		resp, err = client.Get("https://" + cfg.Listen + "/")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("TLS listener never came up: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer resp.Body.Close()
	assert.Equal(t, "HTTP/2.0", resp.Proto, "requests through the TLS listener must be served as HTTP/2")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "backend-a", string(body))

	conn, err := tls.Dial("tcp", cfg.Listen, &tls.Config{
		InsecureSkipVerify: true, // test-only: the generated cert is self-signed
		NextProtos:         []string{"h2", "http/1.1"},
	})
	require.NoError(t, err)
	defer conn.Close()
	assert.Equal(t, "h2", conn.ConnectionState().NegotiatedProtocol,
		"the TLS listener must advertise and negotiate HTTP/2 via ALPN")
}
