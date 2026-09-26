package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DMJain/l7LoadBalancer/internal/app"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
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

// reloadSignalBuffer is the SIGHUP channel's depth. One is deliberate: the
// reload loop drains any pending signal before reloading, so a burst of
// SIGHUPs collapses into at most one pending reload and can never pile up
// (ADR-0015 decision 1).
const reloadSignalBuffer = 1

// reloadLoop consumes SIGHUP signals and reloads the running application from
// configPath, one reload at a time. It is main's whole reload policy (S4.T3):
// for each delivered signal it strictly parses the file, validates it, and asks
// the application to reload; a parse or validation failure is logged as
// config_reload_failed and the loop keeps serving the previous config. Before
// reloading it drains any further signals already queued, so several SIGHUPs in
// quick succession collapse into one reload. It returns when ctx is cancelled
// (SIGINT/SIGTERM), so a reload never blocks shutdown.
//
// It is a standalone function rather than an inline goroutine so tests can
// drive it with a fake signal channel, a temp config file, and a captured
// logger. application.Reload logs its own rejection reasons (non-backend
// change, apply error), so the loop only has to keep going.
func reloadLoop(ctx context.Context, configPath string, sigCh <-chan os.Signal, application *app.App, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-sigCh:
		}
		drainSignals(sigCh)

		cfg, err := config.Load(configPath)
		if err != nil {
			log.Warn("config reload failed",
				"event", logger.EventConfigReloadFailed,
				"reason", logger.ReasonParseError,
				"err", err,
			)
			continue
		}
		if err := cfg.Validate(); err != nil {
			log.Warn("config reload failed",
				"event", logger.EventConfigReloadFailed,
				"reason", logger.ReasonValidationError,
				"err", err,
			)
			continue
		}
		if err := application.Reload(ctx, cfg); err != nil {
			continue
		}
	}
}

// drainSignals consumes every signal already queued on ch without blocking, so
// a burst of SIGHUPs becomes a single reload.
func drainSignals(ch <-chan os.Signal) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

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
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: l7lb probe <url>")
		return 2, true
	}
	return runProbe(args[2]), true
}

// runProbe GETs url and maps the outcome to a process exit code: 0 for any
// 2xx, non-zero for any non-2xx response or transport error (connection
// refused, timeout, DNS failure). It deliberately does not follow redirects —
// a 3xx is a failure, matching the active health checker's policy.
func runProbe(url string) int {
	client := &http.Client{
		Timeout: probeTimeout,
		// Observe a redirect rather than chase it, so a 3xx is a non-2xx
		// failure even when it points at a healthy target — matching the
		// active health checker's CheckRedirect policy.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe %s: %v\n", url, err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		fmt.Fprintf(os.Stderr, "probe %s: status %d\n", url, resp.StatusCode)
		return 1
	}
	return 0
}

// main is now only flags, file I/O, and signals: parse the config path, load
// and validate the config, hand it to internal/app to build the whole wiring
// graph, and run until SIGINT/SIGTERM. All wiring lives in internal/app
// (S4.T0); main no longer knows about the registry, selector, proxy, health
// checker, or metrics collector.
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

	application, err := app.Build(cfg, log)
	if err != nil {
		log.Error("application build failed", "config", *configPath, "err", err)
		os.Exit(1)
	}

	log.Info("l7LoadBalancer starting",
		"config", *configPath,
		"listen", cfg.Listen,
		"algorithm", cfg.Algorithm,
		"backend_count", len(cfg.Backends),
		"circuit_cooldown", *cfg.Circuit.Cooldown,
	)

	// SIGINT/SIGTERM cancel sigCtx, which Run shares with the active health
	// checker (ADR-0011 decision 13) and the three servers' graceful shutdown.
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// SIGHUP reloads the backend list in place (S4.T3). It is registered
	// separately from the SIGINT/SIGTERM shutdown context, and the loop shares
	// sigCtx, so a reload never races shutdown and stops with it.
	reloadCh := make(chan os.Signal, reloadSignalBuffer)
	signal.Notify(reloadCh, syscall.SIGHUP)
	defer signal.Stop(reloadCh)
	go reloadLoop(sigCtx, *configPath, reloadCh, application, log)

	// Run logs its own server/shutdown failures, so main only owns the exit
	// code: a serve failure is fatal, and a clean shutdown returns nil.
	if err := application.Run(sigCtx); err != nil {
		os.Exit(1)
	}
}
