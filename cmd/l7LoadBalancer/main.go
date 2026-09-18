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

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/balancer"
	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
	"github.com/DMJain/l7LoadBalancer/internal/proxy"
)

// main is a thin wiring layer: load and validate config, build the registry,
// pick a selector from the configured algorithm, wrap it in the proxy, and
// serve. All selection, routing, and connection accounting lives in the
// internal packages; see docs/architecture.md and ADR-0002.
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

	reg, err := backend.NewRegistry(cfg.Backends)
	if err != nil {
		log.Error("backend registry build failed", "err", err)
		os.Exit(1)
	}

	sel, err := balancer.NewFromConfig(cfg, reg)
	if err != nil {
		log.Error("selector build failed", "algorithm", cfg.Algorithm, "err", err)
		os.Exit(1)
	}

	log.Info("l7LoadBalancer starting",
		"config", *configPath,
		"listen", cfg.Listen,
		"algorithm", cfg.Algorithm,
		"backends", len(cfg.Backends),
	)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           proxy.New(reg, sel),
		ReadHeaderTimeout: 5 * time.Second,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
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
	log.Info("shutdown complete")
}
