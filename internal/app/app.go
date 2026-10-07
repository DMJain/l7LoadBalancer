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
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/balancer"
	"github.com/DMJain/l7LoadBalancer/internal/circuit"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/health"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
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
// loadedCfg is the one config record the application keeps: the field the
// reload operation (S4.T3) replaces, holding the config the running system was
// last built or reloaded from, so each reload diffs against the currently
// loaded config rather than the startup config and successive reloads compose.
// It is an atomic pointer so a reader never sees a half-replaced config
// (ADR-0015 decision 12).
//
// reloadMu serializes every reload, whichever trigger started it (SIGHUP or
// the admin endpoint), so the registry apply keeps a single writer (ADR-0015
// decisions 6 and 14). lastReload is the outcome of the most recent attempt,
// one atomic pointer so a reader never sees half a record (decision 16).
// runCtx is the context Run was given; the admin endpoint reloads on it, not on
// the request's context, because a reload starts probers and drains that must
// outlive the request (decision 17). It is nil until Run starts.
type App struct {
	log        *slog.Logger
	collector  *metrics.Collector
	reg        *backend.Registry
	checker    *health.Checker
	outlier    *health.OutlierDetector
	srv        *http.Server
	metricsSrv *http.Server
	healthSrv  *http.Server
	proxy      *proxy.Proxy
	loadedCfg  atomic.Pointer[config.Config]
	reloadMu   sync.Mutex
	lastReload atomic.Pointer[ReloadStatus]
	runCtx     atomic.Pointer[context.Context]
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
	p.SetTransport(buildTransport(cfg))
	p.SetMetrics(collector)
	collector.SetAlgorithm("", cfg.Algorithm)
	p.RegisterObserver(proxy.NewLatencyObserver())
	outlier := health.NewOutlierDetector(log, collector)
	p.RegisterObserver(outlier)
	p.RegisterObserver(breaker)

	checker := health.New(reg, *cfg.Health.ProbeInterval, *cfg.Health.ProbeTimeout, log, collector)

	// The client handler is built per listener mode: plain and TLS both serve
	// the proxy handler directly (TLS wraps it in a TLSConfig on the server
	// below), while h2c wraps it in the h2c handler. The wrap is outermost
	// because h2c.NewHandler inspects the raw connection for the HTTP/2 preface
	// (and the HTTP/1.1 Upgrade handshake) before any request reaches the
	// proxy. Plain and TLS modes deliberately carry no h2c handler.
	clientHandler := http.Handler(p)
	if cfg.H2C {
		clientHandler = h2c.NewHandler(p, &http2.Server{})
	}
	srv, err := buildClientServer(cfg, clientHandler)
	if err != nil {
		return nil, err
	}

	a := &App{
		log:       log,
		collector: collector,
		reg:       reg,
		checker:   checker,
		outlier:   outlier,
		srv:       srv,
		proxy:     p,
		healthSrv: &http.Server{
			Addr:              *cfg.HealthEndpoint.Listen,
			Handler:           health.NewHandler(checker, reg, true, collector),
			ReadHeaderTimeout: readHeaderTimeout,
		},
	}
	a.metricsSrv = &http.Server{
		Addr:              *cfg.Metrics.Listen,
		Handler:           buildMetricsHandler(collector, a.adminHandler()),
		ReadHeaderTimeout: readHeaderTimeout,
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
// from. It is the baseline the reload operation diffs a new config against, so
// successive reloads compose; it is safe to call concurrently with a reload.
func (a *App) LoadedConfig() *config.Config { return a.loadedCfg.Load() }

// Reload replaces the running backend set with the one in cfg, an already
// parsed and validated config, while traffic flows. It is the application's
// reload operation (S4.T3). Production reaches it through ReloadFrom, from
// main's SIGHUP loop or the admin endpoint; it takes the same reload mutex, so
// it is safe to call concurrently with either (ADR-0015 decisions 6 and 14).
// Its outcome is recorded as the last reload (decision 16).
//
// The backend list and the algorithm are reloadable. A change to any other
// non-backend field rejects the reload whole, logged as
// config_reload_failed/non_backend_change with the changed fields named, and
// the previously loaded config keeps serving (ADR-0015 decision 4). A changed
// algorithm is built before anything is applied, so a build failure rejects
// the reload with nothing changed; after the backend diff is applied the new
// selector is swapped into the proxy in one atomic store, so requests in
// flight keep their backend and the next request uses the new algorithm
// (ADR-0015 decisions 18–21, S5.T20.7). Otherwise the diff is taken against the currently
// loaded config (not the startup config), applied to the registry, and the
// loaded-config record is replaced; unchanged backends keep their instance and
// all their state (ADR-0015 decisions 2, 6, 12).
//
// Subsystem hook-up mirrors what a backend's membership implies:
//   - the active checker stops probing a removed backend and starts probing an
//     added one, which begins unhealthy and is admitted by one successful probe
//     with reason initial_probe (ADR-0015 decision 10);
//   - the outlier detector forgets a removed backend's window, so a same-name
//     backend added later cannot inherit a stale one;
//   - the collector deletes a removed backend's healthy and circuit-state
//     series and seeds an added backend's (active requests 0, healthy 0,
//     circuit closed). A removed backend's active-requests series is left in
//     place: its in-flight requests still decrement it by name, and deleting it
//     now would recreate it negative. A name shared with a removed backend
//     skips the active seed for that same reason — the fresh instance must not
//     reset a series the removed instance's requests are still decrementing;
//     the drain (S4.T4) deletes the series only once it is idle and no current
//     backend shares the name;
//   - one drain goroutine starts per removed backend (S4.T4), letting its
//     in-flight requests finish until it is idle or reload.drain_window
//     expires, then cancelling the rest and logging one backend drained line.
//     Drains run independently of this call and of later reloads (ADR-0016).
//
// Removals are processed before additions so a same-name re-added identity's
// fresh series are not deleted after being seeded, and the old prober cannot
// write the health gauge once Remove returns. Added series are seeded before
// their prober starts, so a first successful probe cannot race the seed.
//
// The one config-reloaded line carries added/removed/unchanged counts, at WARN
// when unchanged is zero (the blue/green empty-selectable window) and INFO
// otherwise (ADR-0015 decision 11).
func (a *App) Reload(ctx context.Context, cfg *config.Config) error {
	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()
	err := a.reloadLocked(ctx, cfg)
	a.recordReload(err)
	return err
}

// reloadLocked is Reload's body; the caller holds reloadMu.
func (a *App) reloadLocked(ctx context.Context, cfg *config.Config) error {
	oldCfg := a.loadedCfg.Load()

	if changed := config.NonBackendChanges(oldCfg, cfg); len(changed) > 0 {
		a.log.Warn("config reload failed",
			"event", logger.EventConfigReloadFailed,
			"reason", logger.ReasonNonBackendChange,
			"fields", changed,
		)
		return fmt.Errorf("app: reload rejected: non-backend fields changed: %v", changed)
	}

	// Build the new selector first: a failure here must leave everything
	// unchanged (ADR-0015 decision 18).
	var nextSel balancer.Selector
	if cfg.Algorithm != oldCfg.Algorithm {
		sel, err := balancer.NewFromConfig(cfg, a.reg)
		if err != nil {
			a.log.Error("config reload failed",
				"event", logger.EventConfigReloadFailed,
				"reason", logger.ReasonApplyError,
				"err", err,
			)
			return fmt.Errorf("app: reload selector build failed for algorithm %q: %w", cfg.Algorithm, err)
		}
		nextSel = sel
	}

	diff := config.DiffBackends(oldCfg, cfg)
	added, removed, err := a.reg.Apply(diff, cfg.Backends)
	if err != nil {
		a.log.Error("config reload failed",
			"event", logger.EventConfigReloadFailed,
			"reason", logger.ReasonApplyError,
			"err", err,
		)
		return fmt.Errorf("app: reload apply failed: %w", err)
	}

	// The new algorithm takes over from the next request; the gauge moves with
	// it (ADR-0015 decisions 18 and 21).
	if nextSel != nil {
		a.proxy.SetSelector(nextSel)
		a.collector.SetAlgorithm(oldCfg.Algorithm, cfg.Algorithm)
	}

	// The record is replaced after the apply succeeds, so it never names a
	// config the registry has not published; it is the baseline the next reload
	// diffs against, which is what makes successive reloads compose
	// (ADR-0015 decision 12).
	a.loadedCfg.Store(cfg)

	removedNames := make(map[string]bool, len(removed))
	for _, b := range removed {
		removedNames[b.Name] = true
		a.checker.Remove(b)
		a.outlier.Forget(b)
		a.collector.DeleteBackendHealthy(b.Name)
		a.collector.DeleteCircuitState(b.Name)
		go a.drainBackend(ctx, b, *cfg.Reload.DrainWindow)
	}
	for _, b := range added {
		// A name shared with a removed backend already has an
		// active-requests series: the removed instance's in-flight requests
		// still decrement it by name until its drain finishes, so seeding 0
		// here would leave the shared series negative once they do. The
		// healthy and circuit-state series, by contrast, were just deleted with
		// the removed instance and so are seeded fresh for the new one.
		if !removedNames[b.Name] {
			a.collector.SetActiveRequests(b.Name, 0)
		}
		a.collector.SetBackendHealthy(b.Name, false)
		a.collector.SetCircuitState(b.Name, metrics.CircuitStateClosed)
	}
	for _, b := range added {
		a.checker.Add(ctx, b)
	}

	level := slog.LevelInfo
	if len(diff.Unchanged) == 0 {
		level = slog.LevelWarn
	}
	attrs := []any{
		"event", logger.EventConfigReloaded,
		"added", len(added),
		"removed", len(removed),
		"unchanged", len(diff.Unchanged),
	}
	if nextSel != nil {
		attrs = append(attrs, "previous_algorithm", oldCfg.Algorithm, "algorithm", cfg.Algorithm)
	}
	a.log.Log(ctx, level, "config reloaded", attrs...)
	return nil
}

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
	a.runCtx.Store(&ctx)
	cfg := a.loadedCfg.Load()
	a.checker.Start(ctx)
	a.log.Info("health checker started",
		"probe_interval", *cfg.Health.ProbeInterval,
		"probe_timeout", *cfg.Health.ProbeTimeout,
		"backend_count", len(cfg.Backends),
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

// serve runs one server and reports a non-ErrServerClosed failure on errCh. A
// server with a TLSConfig set serves TLS (and negotiates HTTP/2 via ALPN). The
// client server in h2c mode and in plain mode has no TLSConfig and so is served
// with ListenAndServe: in h2c mode its handler is the h2c handler (cleartext
// HTTP/2), in plain mode it is the proxy handler (HTTP/1.1). The metrics and
// health-endpoint servers are always plain HTTP (S5.T1, S5.T2).
func serve(srv *http.Server, label string, errCh chan<- runError) {
	var err error
	if srv.TLSConfig != nil {
		// The cert pair is already loaded into TLSConfig by buildClientServer,
		// so the file arguments are empty.
		err = srv.ListenAndServeTLS("", "")
	} else {
		err = srv.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- runError{label: label, err: err}
	}
}

// buildClientServer constructs the client-facing http.Server for the config's
// listener mode. IdleTimeout is taken from config in every mode, so the knob is
// never accepted-but-ignored. Plain HTTP sets ReadTimeout from config (the
// slow-loris bound of S4.T7). TLS mode loads the configured cert pair into the
// server's TLSConfig up front — so a bad path is a startup error, not a silent
// plaintext listener — which also makes net/http's ListenAndServeTLS negotiate
// HTTP/2 via ALPN. It deliberately does NOT set srv.TLSNextProto: an empty map
// there would silently disable HTTP/2 (S5.T1). h2c mode leaves TLSConfig nil
// (its handler, built in Build, serves HTTP/2 over cleartext) and joins TLS in
// disabling ReadTimeout.
//
// ReadTimeout is hardcoded to 0 in both HTTP/2 modes (TLS and h2c), not
// configurable. net/http's ReadTimeout applies to the whole connection, so on a
// multiplexed HTTP/2 connection it would kill every active stream after N
// seconds regardless of activity; HTTP/2 stream-level flow control is the
// slow-client bound instead (see https://pkg.go.dev/net/http#Server.ReadTimeout
// and the HTTP/2 MaxConcurrentStreams default of 250). IdleTimeout is what
// reclaims idle connections.
func buildClientServer(cfg *config.Config, handler http.Handler) (*http.Server, error) {
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       *cfg.Server.IdleTimeout,
	}
	switch {
	case cfg.TLS != nil:
		cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("app: tls key pair load failed: %w", err)
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
		// ReadTimeout deliberately left at its zero value (see above).
	case cfg.H2C:
		// Cleartext HTTP/2; ReadTimeout deliberately left at its zero value.
		// Same rationale as TLS: h2c is HTTP/2 regardless of encryption.
	default:
		// Plain HTTP: ReadTimeout bounds the full client request read
		// including the body, the slow-body slow-loris vector (S4.T7).
		// WriteTimeout is deliberately omitted: it would span the whole
		// response copy and trip on a slow-but-healthy upstream. See
		// Config.ServerConfig.
		srv.ReadTimeout = *cfg.Server.ReadTimeout
	}
	return srv, nil
}

// buildMetricsHandler returns the metrics listener's handler: the Prometheus
// /metrics endpoint plus net/http/pprof's profile endpoints on the same mux
// (S4.T9). pprof shares the already-on, unauthenticated operational listener
// rather than adding a second port, so it adds no attack surface the project
// has not already accepted; security hardening is out of scope (ADR-0005).
//
// admin serves the reload admin routes under /admin/ (S5.T20.1). They are the
// listener's first state-changing routes; the listener is never published by
// any compose file (ADR-0013 amendment of 2026-10-07).
func buildMetricsHandler(c *metrics.Collector, admin http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/admin/", admin)
	mux.Handle("/metrics", promhttp.HandlerFor(c.Registry(), promhttp.HandlerOpts{}))
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}

// buildTransport constructs the tuned *http.Transport the proxy uses for
// backend connections in place of http.DefaultTransport (S4.T8): a bounded
// dial, a response-header timeout, and a per-host idle pool. MaxIdleConns is
// sized from max_idle_conns_per_host × backend count so the per-host knob is
// not silently capped by the stdlib default of 100; operators tune per-host,
// not total (YAGNI).
//
// Protocol selection is scheme-driven and shared: one transport handles both
// http:// (HTTP/1.1) and https:// (HTTP/2 via ALPN) backends in the same
// config, so migrating backends to HTTP/2 is incremental (S5.T3-main).
// ForceAttemptHTTP2 is set explicitly from force_http2 for a reason: Go only
// auto-negotiates HTTP/2 on a transport whose TLSClientConfig is nil, and
// tls_skip_verify sets it — so without this line every https:// backend would
// silently fall back to HTTP/1.1 with no error, warning, or log (spec §17).
//
// cfg must already have passed Validate.
func buildTransport(cfg *config.Config) *http.Transport {
	perHost := *cfg.Transport.MaxIdleConnsPerHost
	tr := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: *cfg.Transport.DialTimeout,
		}).DialContext,
		ResponseHeaderTimeout: *cfg.Transport.ResponseHeaderTimeout,
		MaxIdleConnsPerHost:   perHost,
		MaxIdleConns:          perHost * len(cfg.Backends),
		IdleConnTimeout:       *cfg.Transport.IdleConnTimeout,
		ForceAttemptHTTP2:     *cfg.Transport.ForceHTTP2,
	}
	if cfg.Transport.TLSSkipVerify {
		// Test/staging only: an operator opt-in for self-signed backend certs.
		// The explicit ForceAttemptHTTP2 above keeps HTTP/2 alive despite this.
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return tr
}

// seedMetrics materializes every backend's initial gauge series through the
// collector's own setter methods, so a freshly started, never-degraded system
// renders a complete dashboard on its first scrape — Prometheus Vec metrics
// create no series until first written (ADR-0013 decision 9). It seeds the
// active-requests gauge to 0, lb_backend_healthy to 1 (every backend starts
// healthy per NewRegistry), and lb_circuit_state to closed (every backend
// starts with a closed circuit per ADR-0011), using the same Collector methods
// real transitions use.
func seedMetrics(c *metrics.Collector, reg *backend.Registry) {
	for _, b := range reg.All() {
		c.SetActiveRequests(b.Name, 0)
		c.SetBackendHealthy(b.Name, true)
		c.SetCircuitState(b.Name, metrics.CircuitStateClosed)
	}
}
