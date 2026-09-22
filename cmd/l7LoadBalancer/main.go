package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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

// version and commit identify the source tree a binary was built from. They
// default to placeholders so a plain `go build`/`go run` still logs something
// identifiable, and are overridden at image-build time via
// -ldflags "-X main.version=$VERSION -X main.commit=$COMMIT" (S3.T10).
var (
	version = "dev"
	commit  = "unknown"
)

// probeTimeout bounds a single `probe` request. It is a constant rather than a
// config knob: the only caller is the container HEALTHCHECK.
const probeTimeout = 2 * time.Second

// probeCommand inspects the process arguments and, when the invocation is the
// `l7lb probe <url>` subcommand, runs a one-shot HTTP probe and reports the
// process exit code. The grammar is a positional subcommand
// (kubectl/docker style), not a flag, and it is handled before flag.Parse so a
// probe never triggers -config's default file lookup or any other flag
// side-effect. This subcommand exists so the distroless container image
// (S3.T10) has a HEALTHCHECK it can run without a shell or curl; the default
// HEALTHCHECK URL targets /livez — see ADR-0014 decision 2 for why.
//
// It returns handled=false for any other invocation, leaving the load-balancer
// startup path — including the bare no-args case — untouched.
func probeCommand(args []string) (code int, handled bool) {
	if len(args) < 2 || args[1] != "probe" {
		return 0, false
	}
	return runProbe(args[2:]), true
}

// runProbe GETs the probe URL and maps the outcome to a process exit code: 0
// for any 2xx, non-zero for any non-2xx response or transport error
// (connection refused, timeout, DNS failure). It deliberately does not follow
// redirects — a 3xx is a failure, matching the active health checker's policy.
func runProbe(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: l7lb probe <url>")
		return 2
	}
	client := &http.Client{
		Timeout: probeTimeout,
		// Observe a redirect rather than chase it, so a 3xx is a non-2xx
		// failure even when it points at a healthy target — matching the
		// active health checker's CheckRedirect policy.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe %s: %v\n", args[0], err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		fmt.Fprintf(os.Stderr, "probe %s: status %d\n", args[0], resp.StatusCode)
		return 1
	}
	return 0
}

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
	log := logger.New(slog.LevelInfo)
	slog.SetDefault(log)
	slog.Info("starting", "version", version, "commit", commit)

	// `l7lb probe <url>` (S3.T10): a self-contained HEALTHCHECK for the
	// distroless image. Handled before flag.Parse so it never triggers
	// -config's default file lookup; see probeCommand's doc comment and
	// ADR-0014 decision 2 for why the URL targets /livez.
	if code, handled := probeCommand(os.Args); handled {
		os.Exit(code)
	}

	configPath := flag.String("config", "configs/example.yaml", "path to config file")
	flag.Parse()

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
