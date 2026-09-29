package app

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"

	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// h2cTestConfig builds a validated config whose client listener is h2c mode,
// with the given backend and idle timeout (nil leaves the config default).
func h2cTestConfig(t *testing.T, backendURL string, idle *time.Duration) *config.Config {
	t.Helper()
	cfg := testConfig([]config.BackendConfig{{Name: "backend-a", URL: backendURL}})
	cfg.H2C = true
	if idle != nil {
		cfg.Server.IdleTimeout = idle
	}
	require.NoError(t, cfg.Validate())
	return cfg
}

// startTestApp builds and runs the app on a free loopback port, returning the
// listen address. Run is torn down on cleanup.
func startTestApp(t *testing.T, cfg *config.Config) string {
	t.Helper()
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
	return cfg.Listen
}

// TestBuildH2CModeServerConstruction pins the h2c path: no TLSConfig, a
// disabled ReadTimeout (HTTP/2 is HTTP/2 regardless of encryption), and an
// IdleTimeout taken from config. Metrics and health listeners stay plain HTTP.
func TestBuildH2CModeServerConstruction(t *testing.T) {
	silenceDefault(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	idle := 45 * time.Second
	cfg := h2cTestConfig(t, backend.URL, &idle)

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	assert.Nil(t, application.srv.TLSConfig, "h2c is cleartext, not TLS")
	assert.Zero(t, application.srv.ReadTimeout, "ReadTimeout is architecturally wrong for HTTP/2 and must be disabled")
	assert.Equal(t, idle, application.srv.IdleTimeout, "HTTP/2 modes rely on IdleTimeout for cleanup")

	assert.Nil(t, application.metricsSrv.TLSConfig, "metrics endpoint stays plain HTTP")
	assert.Nil(t, application.healthSrv.TLSConfig, "health endpoint stays plain HTTP")

	// The h2c handler passes HTTP/1.1 requests through to the proxy unchanged.
	rec := httptest.NewRecorder()
	application.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, rec.Code, "HTTP/1.1 requests must still be served in h2c mode")
}

// TestRunH2CListenerServesHTTP2PriorKnowledge proves App.Run's h2c handler
// accepts a prior-knowledge cleartext HTTP/2 connection (the client speaks the
// HTTP/2 preface immediately) and serves a full request as HTTP/2.0 (S5.T2).
// The http2.Transport with AllowHTTP + a plaintext DialTLSContext sends the
// preface (prior knowledge) without the HTTP/1.1 Upgrade handshake.
func TestRunH2CListenerServesHTTP2PriorKnowledge(t *testing.T) {
	silenceDefault(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "backend-a")
	}))
	t.Cleanup(backend.Close)

	cfg := h2cTestConfig(t, backend.URL, nil)
	addr := startTestApp(t, cfg)

	// Run starts its servers asynchronously; poll until the listener answers.
	deadline := time.Now().Add(5 * time.Second)
	plain := &http.Client{Timeout: time.Second}
	for {
		resp, err := plain.Get("http://" + addr + "/")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("h2c listener never came up: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	client := &http.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}

	resp, err := client.Get("http://" + addr + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "HTTP/2.0", resp.Proto, "prior-knowledge h2c requests must be served as HTTP/2")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "backend-a", string(body))
}

// TestRunH2CListenerUpgrade proves the h2c handler also supports the HTTP/1.1
// Upgrade mechanism (RFC 7540 §3.2): a plaintext HTTP/1.1 request carrying
// Upgrade: h2c and HTTP2-Settings is answered with 101 Switching Protocols.
// The stdlib's own unencrypted-HTTP/2 server only detects the prior-knowledge
// preface, which is why x/net/http2/h2c is used (S5.T2).
func TestRunH2CListenerUpgrade(t *testing.T) {
	silenceDefault(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	cfg := h2cTestConfig(t, backend.URL, nil)
	addr := startTestApp(t, cfg)

	deadline := time.Now().Add(5 * time.Second)
	plain := &http.Client{Timeout: time.Second}
	for {
		resp, err := plain.Get("http://" + addr + "/")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("h2c listener never came up: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close()

	req := "GET / HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Connection: Upgrade, HTTP2-Settings\r\n" +
		"Upgrade: h2c\r\n" +
		"HTTP2-Settings: AAEAAEAAAAA\r\n" +
		"\r\n"
	_, err = io.WriteString(conn, req)
	require.NoError(t, err)

	line, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err)
	assert.Contains(t, line, "101 Switching Protocols", "an h2c Upgrade request must be accepted")
}
