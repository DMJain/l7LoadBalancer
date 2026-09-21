package chaos_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/balancer"
	"github.com/DMJain/l7LoadBalancer/internal/circuit"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/health"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
	"github.com/DMJain/l7LoadBalancer/internal/proxy"
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

// assemble duplicates cmd/l7LoadBalancer's Sprint 3 wiring: collector, seeded
// registry, breaker installed as both the registry gate and an observer,
// selector, proxy with every observer registered, and the active checker
// started on a context cancelled at test cleanup. It is the local duplicate the
// bundle's spec accepts; Sprint 4's programmatic Run(ctx, cfg) seam will either
// converge with it or render it dead code.
func assemble(t *testing.T, cfg *config.Config) *assembly {
	t.Helper()
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

	collector := metrics.NewCollector()

	reg, err := backend.NewRegistry(cfg.Backends)
	require.NoError(t, err)
	seedMetrics(collector, reg)

	breaker := circuit.New(*cfg.Circuit.Cooldown, log, collector)
	reg.SetCircuitGate(breaker)

	sel, err := balancer.NewFromConfig(cfg, reg)
	require.NoError(t, err)

	p := proxy.New(reg, sel)
	p.SetMetrics(collector)
	p.RegisterObserver(proxy.NewLatencyObserver())
	p.RegisterObserver(health.NewOutlierDetector(log, collector))
	p.RegisterObserver(breaker)

	checker := health.New(reg, *cfg.Health.ProbeInterval, *cfg.Health.ProbeTimeout, log, collector)
	ctx, cancel := context.WithCancel(context.Background())
	checker.Start(ctx)
	t.Cleanup(cancel)

	return &assembly{handler: p, collector: collector, logs: logs, reg: reg}
}

// seedMetrics mirrors cmd/l7LoadBalancer's startup seeding (unexported there),
// so every backend's gauge series exists before any failure — the baseline the
// chaos assertions start from.
func seedMetrics(c *metrics.Collector, reg *backend.Registry) {
	for _, b := range reg.All() {
		c.SetActiveConnections(b.Name, 0)
		c.SetBackendHealthy(b.Name, true)
		c.SetCircuitState(b.Name, metrics.CircuitStateClosed)
	}
}
