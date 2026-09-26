package app

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// activeSeriesPresent reports whether the collector exposes an
// lb_active_connections series for backend.
func activeSeriesPresent(t *testing.T, c *metrics.Collector, backend string) bool {
	t.Helper()
	families, err := c.Registry().Gather()
	require.NoError(t, err)
	for _, mf := range families {
		if mf.GetName() != "lb_active_connections" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "backend" && lp.GetValue() == backend {
					return true
				}
			}
		}
	}
	return false
}

// TestDrainBackendShutdownExitsWithoutCleanup proves a drain goroutine returns
// promptly when the process context is cancelled: it neither deletes the
// series nor logs a drained line, because the process is ending and the
// servers' graceful shutdown covers in-flight requests (ADR-0016 decision 8).
func TestDrainBackendShutdownExitsWithoutCleanup(t *testing.T) {
	silenceDefault(t)

	srv := serveFixed(t, "backend-a")
	cfg := testConfig([]config.BackendConfig{{Name: "backend-a", URL: srv.URL}})
	require.NoError(t, cfg.Validate())

	capture := &reloadLogCapture{}
	application, err := Build(cfg, slog.New(capture))
	require.NoError(t, err)

	b := application.Registry().All()[0]
	b.IncActive()
	t.Cleanup(b.DecActive)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		application.drainBackend(ctx, b, time.Hour)
	}()

	// The drain is parked on the non-zero active count; cancelling the process
	// context must return it promptly and without cleanup.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not exit on process shutdown")
	}

	assert.Empty(t, capture.withEvent(logger.EventBackendDrained), "shutdown must not log a drain")
	require.True(t, activeSeriesPresent(t, application.Collector(), "backend-a"),
		"shutdown must not delete the series")
}
