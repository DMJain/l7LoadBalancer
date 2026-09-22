package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/balancer"
	"github.com/DMJain/l7LoadBalancer/internal/circuit"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/health"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
	"github.com/DMJain/l7LoadBalancer/internal/proxy"
)

// main is a thin wiring layer: load and validate config, build the registry,
// pick a selector from the configured algorithm, wrap it in the proxy (with
// its round-trip observers registered), start the active health checker, and
// serve. All selection, routing, health, and connection accounting lives in
// the internal packages; see ADR-0002 for the interface placement this wiring
// relies on, ADR-0011 decision 9 for the observer registration, and ADR-0011
// decision 13 for the health-checker goroutines sharing sigCtx.
//
// The listen address comes from the config file (cfg.Listen), not a flag:
// `listen` is part of the frozen YAML schema and config.Validate checks it is
// a valid host:port. A flag would be a second source of truth.
func main() {
	configPath := flag.String("config", "configs/example.yaml", "path to config file")
	flag.Parse()

	log := logger.New(slog.LevelInfo)
	slog.SetDefault(log)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("config load failed", "config", *configPath, "err", err)
		os.Exit(1)
	}
	if err := cfg.Validate(); err != nil {
		log.Error("config validation failed", "config", *configPath, "err", err)
		os.Exit(1)
	}

	// The metrics collector owns a private Prometheus registry and is exposed on
	// its own listener, separate from client traffic (ADR-0013 decision 8). It is
	// built before the registry so every backend's gauge series can be seeded the
	// moment the registry exists (ADR-0013 decision 9).
	collector := metrics.NewCollector()

	reg, err := backend.NewRegistry(cfg.Backends)
	if err != nil {
		log.Error("backend registry build failed", "err", err)
		os.Exit(1)
	}
	seedMetrics(collector, reg)

	// The circuit breaker is both the registry's eligibility/admission gate and
	// a round-trip observer. Installing the gate before selection begins means
	// every selector's Registry.Selectable snapshot already excludes open
	// circuits (ADR-0011 decision 1, ADR-0012).
	breaker := circuit.New(*cfg.Circuit.Cooldown, log, collector)
	reg.SetCircuitGate(breaker)

	sel, err := balancer.NewFromConfig(cfg, reg)
	if err != nil {
		log.Error("selector build failed", "algorithm", cfg.Algorithm, "err", err)
		os.Exit(1)
	}

	log.Info("l7LoadBalancer starting",
		"config", *configPath,
		"listen", cfg.Listen,
		"algorithm", cfg.Algorithm,
		"backend_count", len(cfg.Backends),
		"circuit_cooldown", *cfg.Circuit.Cooldown,
	)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           newHandler(reg, sel, breaker, collector, log),
		ReadHeaderTimeout: 5 * time.Second,
	}

	metricsSrv := &http.Server{
		Addr:              *cfg.Metrics.Listen,
		Handler:           promhttp.HandlerFor(collector.Registry(), promhttp.HandlerOpts{}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Active health probing shares sigCtx for shutdown (ADR-0011 decision 13):
	// one goroutine per backend, no second shutdown primitive.
	checker := health.New(reg, *cfg.Health.ProbeInterval, *cfg.Health.ProbeTimeout, log, collector)
	checker.Start(sigCtx)
	log.Info("health checker started",
		"probe_interval", *cfg.Health.ProbeInterval,
		"probe_timeout", *cfg.Health.ProbeTimeout,
		"backend_count", len(cfg.Backends),
	)

	// The health endpoint is a third always-on listener mirroring metricsSrv:
	// it serves /livez, /readyz, and /startupz on health_endpoint.listen so
	// probes never enter the proxy path (ADR-0014 decision 1). configLoaded is
	// true by construction here — the process exits above if load or validation
	// failed — and the checker supplies the startup gate.
	healthSrv := &http.Server{
		Addr:              *cfg.HealthEndpoint.Listen,
		Handler:           health.NewHandler(checker, reg, true, collector),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	log.Info("metrics endpoint started", "listen", *cfg.Metrics.Listen)
	go func() {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server error", "err", err)
			os.Exit(1)
		}
	}()

	log.Info("health endpoint started", "listen", *cfg.HealthEndpoint.Listen)
	go func() {
		if err := healthSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("health server error", "err", err)
			os.Exit(1)
		}
	}()

	<-sigCtx.Done()
	log.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("metrics server shutdown failed", "err", err)
		os.Exit(1)
	}
	if err := healthSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("health server shutdown failed", "err", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}

// seedMetrics materializes every backend's initial gauge series through the
// collector's own setter methods, so a freshly started, never-degraded system
// renders a complete dashboard on its first scrape — Prometheus Vec metrics
// create no series until first written (ADR-0013 decision 9). It seeds the
// active-connections gauge to 0, lb_backend_healthy to 1 (every backend
// starts healthy per NewRegistry), and lb_circuit_state to closed (every
// backend starts with a closed circuit per ADR-0011), using the same Collector
// methods real transitions use.
func seedMetrics(c *metrics.Collector, reg *backend.Registry) {
	for _, b := range reg.All() {
		c.SetActiveConnections(b.Name, 0)
		c.SetBackendHealthy(b.Name, true)
		c.SetCircuitState(b.Name, metrics.CircuitStateClosed)
	}
}

// newHandler builds the proxy, installs the whole-request metrics collector,
// and registers the round-trip observers that record every backend round trip.
// All of this happens here, at construction time before the server starts, so
// the request path can read the observer slice and the metrics reference
// without a lock. Latency recording, passive outlier detection, and the
// circuit breaker are all wired (ADR-0011 decision 9); the request
// counter/histogram is wired via SetMetrics (ADR-0013 decision 14). Both
// registrations are deliberately separate from proxy.New, whose two-argument
// signature is frozen.
func newHandler(reg *backend.Registry, sel balancer.Selector, breaker *circuit.Breaker, collector *metrics.Collector, log *slog.Logger) http.Handler {
	p := proxy.New(reg, sel)
	p.SetMetrics(collector)
	p.RegisterObserver(proxy.NewLatencyObserver())
	p.RegisterObserver(health.NewOutlierDetector(log, collector))
	p.RegisterObserver(breaker)
	return p
}
