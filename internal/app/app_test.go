package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// testConfig builds a config ready for Build: the given backends, round-robin
// selection, and every listener on :0 so a test never binds a fixed port. The
// Sprint 3 durations are supplied so Validate's defaulting has nothing to do
// and the test config is explicit about the cadence Build reads.
func testConfig(backends []config.BackendConfig) *config.Config {
	probeInterval := time.Second
	probeTimeout := time.Second
	cooldown := time.Minute
	metricsListen := ":0"
	healthListen := ":0"
	return &config.Config{
		Listen:    ":0",
		Algorithm: config.AlgorithmRoundRobin,
		Health: config.HealthConfig{
			ProbeInterval: &probeInterval,
			ProbeTimeout:  &probeTimeout,
		},
		Circuit:        config.CircuitConfig{Cooldown: &cooldown},
		Metrics:        config.MetricsConfig{Listen: &metricsListen},
		HealthEndpoint: config.HealthEndpointConfig{Listen: &healthListen},
		Backends:       backends,
	}
}

// discardLogger is the logger handed to Build in tests that do not assert on
// log output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// silenceDefault routes slog.Default at a discard handler for the test's
// duration. proxy.New captures slog.Default at construction and logs every
// request through it, so without this each driven request spams test output.
func silenceDefault(t *testing.T) {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(discardLogger())
	t.Cleanup(func() { slog.SetDefault(prev) })
}

// TestBuildRoutesToConfiguredBackends proves the application seam's handler is
// the real proxying handler: requests driven through it reach the configured
// backends and come back 200.
func TestBuildRoutesToConfiguredBackends(t *testing.T) {
	silenceDefault(t)

	serve := func(id string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, id)
		}))
	}
	a := serve("backend-a")
	defer a.Close()
	b := serve("backend-b")
	defer b.Close()

	cfg := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: a.URL},
		{Name: "backend-b", URL: b.URL},
	})
	require.NoError(t, cfg.Validate())

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		rec := httptest.NewRecorder()
		application.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		seen[rec.Body.String()] = true
	}
	require.True(t, seen["backend-a"], "backend-a must receive traffic")
	require.True(t, seen["backend-b"], "backend-b must receive traffic")
}

// TestBuildSeedsGaugeSeries proves Build materializes every backend's gauge
// series through the collector's own setter methods — active connections 0,
// healthy 1, circuit closed — so a freshly built system renders a complete
// dashboard on its first scrape (ADR-0013 decision 9).
func TestBuildSeedsGaugeSeries(t *testing.T) {
	silenceDefault(t)

	cfg := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
		{Name: "backend-b", URL: "http://127.0.0.1:9002"},
	})
	require.NoError(t, cfg.Validate())

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	const wantActive = `
# HELP lb_active_connections In-flight requests currently being served by a backend.
# TYPE lb_active_connections gauge
lb_active_connections{backend="backend-a"} 0
lb_active_connections{backend="backend-b"} 0
`
	require.NoError(t, testutil.GatherAndCompare(
		application.Collector().Registry(), strings.NewReader(wantActive), "lb_active_connections"))

	const wantHealthy = `
# HELP lb_backend_healthy Whether a backend is healthy (1) or unhealthy (0).
# TYPE lb_backend_healthy gauge
lb_backend_healthy{backend="backend-a"} 1
lb_backend_healthy{backend="backend-b"} 1
`
	require.NoError(t, testutil.GatherAndCompare(
		application.Collector().Registry(), strings.NewReader(wantHealthy), "lb_backend_healthy"))

	const wantCircuit = `
# HELP lb_circuit_state Circuit breaker state per backend as a label enum; exactly one of closed/open/half_open is 1.
# TYPE lb_circuit_state gauge
lb_circuit_state{backend="backend-a",state="closed"} 1
lb_circuit_state{backend="backend-a",state="half_open"} 0
lb_circuit_state{backend="backend-a",state="open"} 0
lb_circuit_state{backend="backend-b",state="closed"} 1
lb_circuit_state{backend="backend-b",state="half_open"} 0
lb_circuit_state{backend="backend-b",state="open"} 0
`
	require.NoError(t, testutil.GatherAndCompare(
		application.Collector().Registry(), strings.NewReader(wantCircuit), "lb_circuit_state"))
}

// TestBuildExposesRegistry proves the application value exposes the registry
// the wiring graph built, matching the configured backends.
func TestBuildExposesRegistry(t *testing.T) {
	silenceDefault(t)

	cfg := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
		{Name: "backend-b", URL: "http://127.0.0.1:9002"},
	})
	require.NoError(t, cfg.Validate())

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	all := application.Registry().All()
	require.Len(t, all, 2)
	require.Equal(t, "backend-a", all[0].Name)
	require.Equal(t, "backend-b", all[1].Name)
}

// TestBuildExposesLoadedConfig proves Build records the config it wired, so
// the reload operation (S4.T3) has the currently loaded config to diff
// against; in T2 only tests read it.
func TestBuildExposesLoadedConfig(t *testing.T) {
	silenceDefault(t)

	cfg := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
	})
	require.NoError(t, cfg.Validate())

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	assert.Same(t, cfg, application.LoadedConfig())
}

// TestRunStopsOnContextCancel proves Run serves until the context is cancelled
// and then returns, running the graceful shutdown of all three servers.
func TestRunStopsOnContextCancel(t *testing.T) {
	silenceDefault(t)

	cfg := testConfig([]config.BackendConfig{
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
	})
	require.NoError(t, cfg.Validate())

	application, err := Build(cfg, discardLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// The three seeding tests below pin seedMetrics directly (the same seeding
// Build performs) so a regression in one gauge family is attributable without
// going through Build. They moved here from cmd/l7LoadBalancer when the
// seeding left main.

func TestSeedMetricsBackendHealthy(t *testing.T) {
	reg, err := backend.NewRegistry([]config.BackendConfig{
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
		{Name: "backend-b", URL: "http://127.0.0.1:9002"},
	})
	require.NoError(t, err)

	c := metrics.NewCollector()
	seedMetrics(c, reg)

	const want = `
# HELP lb_backend_healthy Whether a backend is healthy (1) or unhealthy (0).
# TYPE lb_backend_healthy gauge
lb_backend_healthy{backend="backend-a"} 1
lb_backend_healthy{backend="backend-b"} 1
`
	require.NoError(t, testutil.GatherAndCompare(
		c.Registry(), strings.NewReader(want), "lb_backend_healthy"))
}

func TestSeedMetricsCircuitState(t *testing.T) {
	reg, err := backend.NewRegistry([]config.BackendConfig{
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
		{Name: "backend-b", URL: "http://127.0.0.1:9002"},
	})
	require.NoError(t, err)

	c := metrics.NewCollector()
	seedMetrics(c, reg)

	const want = `
# HELP lb_circuit_state Circuit breaker state per backend as a label enum; exactly one of closed/open/half_open is 1.
# TYPE lb_circuit_state gauge
lb_circuit_state{backend="backend-a",state="closed"} 1
lb_circuit_state{backend="backend-a",state="half_open"} 0
lb_circuit_state{backend="backend-a",state="open"} 0
lb_circuit_state{backend="backend-b",state="closed"} 1
lb_circuit_state{backend="backend-b",state="half_open"} 0
lb_circuit_state{backend="backend-b",state="open"} 0
`
	require.NoError(t, testutil.GatherAndCompare(
		c.Registry(), strings.NewReader(want), "lb_circuit_state"))
}

func TestSeedMetricsActiveConnections(t *testing.T) {
	reg, err := backend.NewRegistry([]config.BackendConfig{
		{Name: "backend-a", URL: "http://127.0.0.1:9001"},
		{Name: "backend-b", URL: "http://127.0.0.1:9002"},
	})
	require.NoError(t, err)

	c := metrics.NewCollector()
	seedMetrics(c, reg)

	const want = `
# HELP lb_active_connections In-flight requests currently being served by a backend.
# TYPE lb_active_connections gauge
lb_active_connections{backend="backend-a"} 0
lb_active_connections{backend="backend-b"} 0
`
	require.NoError(t, testutil.GatherAndCompare(
		c.Registry(), strings.NewReader(want), "lb_active_connections"))
}
