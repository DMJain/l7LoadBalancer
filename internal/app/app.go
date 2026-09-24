// Package app owns the load balancer's whole wiring graph: the metrics
// collector, the backend registry and its seeded series, the circuit breaker
// as registry gate and observer, the selector, the proxy with every round-trip
// observer registered, the active health checker, and the client, metrics, and
// health-endpoint servers.
//
// It exists so production, the chaos harness, and integration tests build the
// system the same way: Build assembles the graph, and Run serves it until the
// context is cancelled. main is left with flags, file I/O, and signals
// (S4.T0, spec D3–D4). Nothing reloads the graph yet — S4.T3 adds that.
//
// The package sits above proxy, health, circuit, metrics, balancer, backend,
// and config, is imported only by main and tests, and keeps the dependency
// graph acyclic.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/balancer"
	"github.com/DMJain/l7LoadBalancer/internal/circuit"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/health"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
	"github.com/DMJain/l7LoadBalancer/internal/proxy"
)

// shutdownTimeout bounds the graceful shutdown of all three servers, matching
// the timeout main used before the wiring moved here.
const shutdownTimeout = 10 * time.Second

// readHeaderTimeout is the client-, metrics-, and health-server read-header
// bound main applied to each server.
const readHeaderTimeout = 5 * time.Second

// App is the assembled load-balancing system. Its subsystems are wired at
// build time and never mutated here; Run only starts and stops the servers.
//
// loadedCfg is the one field the reload operation (S4.T3) replaces: it holds
// the config the running system was last built or reloaded from, so each reload
// diffs against the currently loaded config rather than the startup config and
// successive reloads compose. It is an atomic pointer so a reader never sees a
// half-replaced config (ADR-0015 decision 12).
type App struct {
	log        *slog.Logger
	cfg        *config.Config
	collector  *metrics.Collector
	reg        *backend.Registry
	checker    *health.Checker
	srv        *http.Server
	metricsSrv *http.Server
	healthSrv  *http.Server
	loadedCfg  atomic.Pointer[config.Config]
}

// Build assembles the whole wiring graph from an already-validated config and
// returns it. It performs the wiring main used to do inline: build the
// collector; build the registry and seed every backend's gauge series; install
// the circuit breaker as both the registry gate and a round-trip observer;
// build the selector from the config; build the proxy with the metrics
// collector and the latency, outlier, and circuit observers registered; build
// the active health checker; and build the health-endpoint handler. The
// decisions mirror ADR-0011 decisions 1/9/13, ADR-0012, ADR-0013, and
// ADR-0014.
//
// cfg must already have passed Validate: Build reads the pointers Validate
// guarantees are non-nil and does not re-default them.
func Build(cfg *config.Config, log *slog.Logger) (*App, error) {
	collector := metrics.NewCollector()

	reg, err := backend.NewRegistry(cfg.Backends)
	if err != nil {
		return nil, fmt.Errorf("app: backend registry build failed: %w", err)
	}
	seedMetrics(collector, reg)

	breaker := circuit.New(*cfg.Circuit.Cooldown, log, collector)
	reg.SetCircuitGate(breaker)

	sel, err := balancer.NewFromConfig(cfg, reg)
	if err != nil {
		return nil, fmt.Errorf("app: selector build failed for algorithm %q: %w", cfg.Algorithm, err)
	}

	p := proxy.New(reg, sel)
	p.SetMetrics(collector)
	p.RegisterObserver(proxy.NewLatencyObserver())
	p.RegisterObserver(health.NewOutlierDetector(log, collector))
	p.RegisterObserver(breaker)

	checker := health.New(reg, *cfg.Health.ProbeInterval, *cfg.Health.ProbeTimeout, log, collector)

	a := &App{
		log:       log,
		cfg:       cfg,
		collector: collector,
		reg:       reg,
		checker:   checker,
		srv: &http.Server{
			Addr:              cfg.Listen,
			Handler:           p,
			ReadHeaderTimeout: readHeaderTimeout,
		},
		metricsSrv: &http.Server{
			Addr:              *cfg.Metrics.Listen,
			Handler:           promhttp.HandlerFor(collector.Registry(), promhttp.HandlerOpts{}),
			ReadHeaderTimeout: readHeaderTimeout,
		},
		healthSrv: &http.Server{
			Addr:              *cfg.HealthEndpoint.Listen,
			Handler:           health.NewHandler(checker, reg, true, collector),
			ReadHeaderTimeout: readHeaderTimeout,
		},
	}
	a.loadedCfg.Store(cfg)
	return a, nil
}

// Handler returns the client-facing proxy handler.
func (a *App) Handler() http.Handler { return a.srv.Handler }

// Collector returns the metrics collector whose private registry the /metrics
// endpoint exposes.
func (a *App) Collector() *metrics.Collector { return a.collector }

// Registry returns the backend registry, for callers (tests, health probes)
// that need the current backend set.
func (a *App) Registry() *backend.Registry { return a.reg }

// LoadedConfig returns the config the running system was last built or reloaded
// from. It is the baseline the reload operation (S4.T3) diffs a new config
// against; in T2 only tests read it. It is safe to call concurrently with a
// reload.
func (a *App) LoadedConfig() *config.Config { return a.loadedCfg.Load() }

// Run serves until ctx is cancelled, then performs the graceful shutdown of
// the client, metrics, and health-endpoint servers in that order, each under
// the same shutdown timeout. A server that fails before shutdown is logged,
// the other two are still shut down, and Run returns that error.
//
// The active health checker shares ctx, so cancelling ctx stops probing as
// well as serving (ADR-0011 decision 13).
//
// Concurrency: the checker owns its own goroutines (one per backend), and each
// of the three servers runs in one goroutine that reports its failure on a
// buffered errCh; Run is the only reader and owns all shutdowns, so no two
// goroutines share mutable state.
func (a *App) Run(ctx context.Context) error {
	a.checker.Start(ctx)
	a.log.Info("health checker started",
		"probe_interval", *a.cfg.Health.ProbeInterval,
		"probe_timeout", *a.cfg.Health.ProbeTimeout,
		"backend_count", len(a.cfg.Backends),
	)

	errCh := make(chan runError, 3)

	go serve(a.srv, "server error", errCh)
	a.log.Info("metrics endpoint started", "listen", a.metricsSrv.Addr)
	go serve(a.metricsSrv, "metrics server error", errCh)

	a.log.Info("health endpoint started", "listen", a.healthSrv.Addr)
	go serve(a.healthSrv, "health server error", errCh)

	var runErr error
	select {
	case <-ctx.Done():
		a.log.Info("shutdown signal received")
	case re := <-errCh:
		a.log.Error(re.label, "err", re.err)
		runErr = re.err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := a.srv.Shutdown(shutdownCtx); err != nil {
		a.log.Error("graceful shutdown failed", "err", err)
		return err
	}
	if err := a.metricsSrv.Shutdown(shutdownCtx); err != nil {
		a.log.Error("metrics server shutdown failed", "err", err)
		return err
	}
	if err := a.healthSrv.Shutdown(shutdownCtx); err != nil {
		a.log.Error("health server shutdown failed", "err", err)
		return err
	}
	a.log.Info("shutdown complete")
	return runErr
}

// runError carries a server's failure with the label main used to log it.
type runError struct {
	label string
	err   error
}

// serve runs one server and reports a non-ErrServerClosed failure on errCh.
func serve(srv *http.Server, label string, errCh chan<- runError) {
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- runError{label: label, err: err}
	}
}

// seedMetrics materializes every backend's initial gauge series through the
// collector's own setter methods, so a freshly started, never-degraded system
// renders a complete dashboard on its first scrape — Prometheus Vec metrics
// create no series until first written (ADR-0013 decision 9). It seeds the
// active-connections gauge to 0, lb_backend_healthy to 1 (every backend starts
// healthy per NewRegistry), and lb_circuit_state to closed (every backend
// starts with a closed circuit per ADR-0011), using the same Collector methods
// real transitions use.
func seedMetrics(c *metrics.Collector, reg *backend.Registry) {
	for _, b := range reg.All() {
		c.SetActiveConnections(b.Name, 0)
		c.SetBackendHealthy(b.Name, true)
		c.SetCircuitState(b.Name, metrics.CircuitStateClosed)
	}
}
