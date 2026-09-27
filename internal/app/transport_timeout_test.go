package app

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
)

// TestBuildTransportSizesTotalPoolFromPerHost pins the transport construction
// note in the contract: the total MaxIdleConns must be per-host × backend count,
// or the stdlib default of 100 silently caps the per-host knob. The remaining
// fields are asserted here too, so a mis-wired transport is caught without
// relying on timing.
func TestBuildTransportSizesTotalPoolFromPerHost(t *testing.T) {
	perHost := 7
	dial := 3 * time.Second
	responseHeader := 4 * time.Second
	idle := 5 * time.Second

	cfg := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
		{Name: "backend-b", URL: "http://127.0.0.1:9002"},
		{Name: "backend-c", URL: "http://127.0.0.1:9003"},
		{Name: "backend-d", URL: "http://127.0.0.1:9004"},
	})
	cfg.Transport.DialTimeout = &dial
	cfg.Transport.ResponseHeaderTimeout = &responseHeader
	cfg.Transport.MaxIdleConnsPerHost = &perHost
	cfg.Transport.IdleConnTimeout = &idle
	require.NoError(t, cfg.Validate())

	tr := buildTransport(cfg)

	require.NotNil(t, tr.DialContext, "dial must run through a configured DialContext")
	assert.Equal(t, responseHeader, tr.ResponseHeaderTimeout)
	assert.Equal(t, perHost, tr.MaxIdleConnsPerHost)
	assert.Equal(t, idle, tr.IdleConnTimeout)
	assert.Equal(t, perHost*len(cfg.Backends), tr.MaxIdleConns,
		"total idle pool must be sized from per-host × backend count, not the stdlib default")
}

// TestTransportDialTimeoutFailsFast proves the proxy's configured dial timeout
// is wired: a request to a non-accepting address fails near the bound instead of
// hanging on the stdlib default. 192.0.2.0/24 is TEST-NET-1, reserved and
// unroutable, so the dial cannot be answered.
func TestTransportDialTimeoutFailsFast(t *testing.T) {
	silenceDefault(t)

	dialTimeout := 200 * time.Millisecond
	cfg := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: "http://192.0.2.1:9"},
	})
	cfg.Transport.DialTimeout = &dialTimeout
	require.NoError(t, cfg.Validate())

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	start := time.Now()
	rec := httptest.NewRecorder()
	application.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	elapsed := time.Since(start)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Less(t, elapsed, 2*time.Second,
		"a non-accepting address must fail at the %s dial timeout, not the stdlib default", dialTimeout)
}

// TestTransportResponseHeaderTimeoutFailsFast proves the proxy's configured
// response-header timeout is wired: a backend that accepts a connection but
// never sends headers is recorded as a failure near the bound instead of
// hanging the request forever.
func TestTransportResponseHeaderTimeoutFailsFast(t *testing.T) {
	silenceDefault(t)

	gate := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-gate
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() {
		close(gate)
		backend.Close()
	})

	responseHeaderTimeout := 200 * time.Millisecond
	cfg := testConfig([]config.BackendConfig{{Name: "backend-a", URL: backend.URL}})
	cfg.Transport.ResponseHeaderTimeout = &responseHeaderTimeout
	require.NoError(t, cfg.Validate())

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	start := time.Now()
	rec := httptest.NewRecorder()
	application.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	elapsed := time.Since(start)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.GreaterOrEqual(t, elapsed, responseHeaderTimeout/2,
		"the request must wait for the response-header bound, not fail instantly")
	assert.Less(t, elapsed, 2*time.Second,
		"a backend that never sends headers must fail at the %s response-header timeout", responseHeaderTimeout)
}

// TestReloadRejectsTransportChange proves a reload that changes the transport
// section is rejected whole and names "transport" (S4.T8).
func TestReloadRejectsTransportChange(t *testing.T) {
	silenceDefault(t)

	backend := serveFixed(t, "backend-a")
	cfg := testConfig([]config.BackendConfig{{Name: "backend-a", URL: backend.URL}})
	require.NoError(t, cfg.Validate())

	capture := &reloadLogCapture{}
	application, err := Build(cfg, slog.New(capture))
	require.NoError(t, err)

	cfg2 := testConfig([]config.BackendConfig{{Name: "backend-a", URL: backend.URL}})
	longer := 2 * time.Second
	cfg2.Transport.DialTimeout = &longer
	require.NoError(t, cfg2.Validate())

	err = application.Reload(context.Background(), cfg2)
	require.Error(t, err, "a transport change must reject the reload")
	assert.Same(t, cfg, application.LoadedConfig(), "the loaded config must be untouched")

	failed := capture.withEvent(logger.EventConfigReloadFailed)
	require.Len(t, failed, 1, "exactly one config reload failed line")
	assert.True(t, recordHasStringValue(failed[0], "fields", "transport"),
		"the failed line must name the changed transport section")
}
