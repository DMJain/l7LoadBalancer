package app

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/config"
)

// TestSlowLorisBodyDisconnectedAtReadTimeout proves the client-facing server's
// configured ReadTimeout bounds a full request read including the body: a
// client that declares a large body, sends a couple of bytes, then stalls is
// cut off near the bound instead of pinning the server goroutine. The backend
// drains the request body before answering, so no response can arrive until the
// full body does — without the bound the connection would stay open until the
// test's own deadline (S4.T7).
func TestSlowLorisBodyDisconnectedAtReadTimeout(t *testing.T) {
	silenceDefault(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	readTimeout := 200 * time.Millisecond
	cfg := testConfig([]config.BackendConfig{{Name: "backend-a", URL: backend.URL}})
	cfg.Server.ReadTimeout = &readTimeout
	require.NoError(t, cfg.Validate())

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = application.srv.Serve(ln) }()
	t.Cleanup(func() { _ = application.srv.Close() })

	conn, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	// Headers declare a 10 MB body; only two bytes ever follow.
	_, err = io.WriteString(conn, "POST / HTTP/1.1\r\nHost: l7lb\r\nContent-Length: 10485760\r\n\r\nab")
	require.NoError(t, err)

	start := time.Now()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	body, readErr := io.ReadAll(conn)
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 2*time.Second,
		"the slow client must be cut off near the %s read timeout, not after the test's own deadline", readTimeout)
	if len(body) == 0 {
		require.ErrorIs(t, readErr, io.EOF, "a stalled body must end in a close")
	} else {
		assert.Contains(t, string(body), "HTTP/1.1 502")
	}
}
