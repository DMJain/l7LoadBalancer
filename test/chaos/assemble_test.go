package chaos_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/app"
	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// Chaos-test timing: the config-driven fast path. ADR-0011 decision 10 keeps
// cadence (probe interval/timeout) and cooldown in config while the
// consecutive thresholds, the passive window, and the failure-to-open
// threshold stay compile-time constants — so the whole speed-up lives in these
// three knobs and no production constant is bent for the test.
const (
	chaosProbeInterval = 20 * time.Millisecond
	chaosProbeTimeout  = 100 * time.Millisecond
	chaosCooldown      = 200 * time.Millisecond
)

// Timed assertions never sleep for an exact duration; they poll until the
// observable flips. The deadline is far above the arithmetic expectation
// (sub-second) so the race detector's overhead on a slow executor cannot make
// them flake.
const (
	eventuallyDeadline = 3 * time.Second
	eventuallyTick     = 10 * time.Millisecond
)

// assembly is the handle assemble hands back: the proxy under test, the
// collector whose gauges the tests read, the captured transition logs, and the
// registry for backend lookup.
type assembly struct {
	handler   http.Handler
	collector *metrics.Collector
	logs      *captureHandler
	reg       *backend.Registry
}

// backendConfigs maps flippable backends to their config entries, preserving
// order. Shared by every chaos config builder.
func backendConfigs(fbs []*flippableBackend) []config.BackendConfig {
	backends := make([]config.BackendConfig, len(fbs))
	for i, fb := range fbs {
		backends[i] = config.BackendConfig{Name: fb.id, URL: fb.URL()}
	}
	return backends
}

// chaosConfig builds the round-robin config the chaos tests assemble: the
// flippable backends' URLs, the fast-path timing, and no listen binding (the
// tests serve the proxy handler directly).
func chaosConfig(fbs []*flippableBackend) *config.Config {
	probeInterval := chaosProbeInterval
	probeTimeout := chaosProbeTimeout
	cooldown := chaosCooldown
	return &config.Config{
		Listen:    ":0",
		Algorithm: config.AlgorithmRoundRobin,
		Health: config.HealthConfig{
			ProbeInterval: &probeInterval,
			ProbeTimeout:  &probeTimeout,
		},
		Circuit:  config.CircuitConfig{Cooldown: &cooldown},
		Backends: backendConfigs(fbs),
	}
}

// assemble builds the system through internal/app's Build seam — production
// and test wiring now converge, closing the Sprint 3 retro's assemble debt —
// and starts it with Run on a context cancelled at test cleanup. The client,
// metrics, and health-endpoint listeners all bind :0 (the tests drive the
// handler directly). The returned handle keeps the same shape the chaos
// assertions read before the swap.
func assemble(t *testing.T, cfg *config.Config) *assembly {
	t.Helper()

	// Run serves all three servers; the tests never want a fixed port.
	zero := ":0"
	if cfg.Metrics.Listen == nil {
		cfg.Metrics.Listen = &zero
	}
	if cfg.HealthEndpoint.Listen == nil {
		cfg.HealthEndpoint.Listen = &zero
	}
	require.NoError(t, cfg.Validate())

	logs := &captureHandler{}
	log := slog.New(logs)

	// proxy.New captures slog.Default() at construction (its frozen two-arg
	// signature takes no logger), so route the default at the capture handler
	// for the duration of the test: request lines are then retained instead of
	// spamming the test output, and nothing else can observe them.
	prevDefault := slog.Default()
	slog.SetDefault(log)
	t.Cleanup(func() { slog.SetDefault(prevDefault) })

	application, err := app.Build(cfg, log)
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = application.Run(runCtx)
	}()
	t.Cleanup(func() {
		cancel()
		<-runDone
	})

	return &assembly{
		handler:   application.Handler(),
		collector: application.Collector(),
		logs:      logs,
		reg:       application.Registry(),
	}
}
