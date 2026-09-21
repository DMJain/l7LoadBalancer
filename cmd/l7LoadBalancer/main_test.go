package main

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// TestSeedMetricsBackendHealthy proves main's startup seeding materializes
// every backend's lb_backend_healthy series at 1 through the collector's own
// setter — the same method real health transitions use — so a freshly started,
// never-degraded system renders a complete healthy dashboard on its first
// scrape instead of a blank panel (ADR-0013 decision 9).
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

// TestSeedMetricsActiveConnections proves main's startup seeding materializes
// every backend's lb_active_connections series at 0 through the collector's own
// setter, so a freshly started, never-trafficked system renders a complete
// dashboard on its first scrape instead of a blank panel. Prometheus Vec
// metrics materialize no series until first written (ADR-0013 decision 9).
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
