// Command dummy-backend is a lightweight, stdlib-only HTTP server used to
// exercise the load balancer end-to-end without real upstreams (S1.T9).
//
// It identifies itself in every response and can inject artificial latency
// and failures, so selection-algorithm differences and (from Sprint 3)
// resilience behavior are observable on `docker compose up` with no manual
// configuration.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

const (
	defaultAddr     = ":8080"
	shutdownTimeout = 5 * time.Second
)

func main() {
	name := flag.String("name", "backend", "identifier returned in every response body")
	addr := flag.String("addr", defaultAddr, "address to listen on")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	sleepMS, err := envInt("SLEEP_MS", 0)
	if err != nil {
		logger.Error("invalid SLEEP_MS", "error", err)
		os.Exit(1)
	}
	failRate, err := envFloat("FAIL_RATE", 0)
	if err != nil {
		logger.Error("invalid FAIL_RATE", "error", err)
		os.Exit(1)
	}
	if failRate < 0 || failRate > 1 {
		logger.Error("FAIL_RATE out of range", "fail_rate", failRate, "min", 0.0, "max", 1.0)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(*name, sleepMS, failRate, logger),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown", "error", err)
		}
	}()

	logger.Info("dummy backend listening",
		"backend", *name, "addr", *addr, "sleep_ms", sleepMS, "fail_rate", failRate)

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server", "error", err)
		os.Exit(1)
	}
}

// newHandler returns the dummy backend's HTTP handler. Every GET sleeps for
// sleepMS, then fails with HTTP 500 with probability failRate; the response
// body always identifies the backend so distribution stays observable even
// through failing responses. Non-GET requests get a 405.
func newHandler(name string, sleepMS int, failRate float64, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			logRequest(logger, r, name, http.StatusMethodNotAllowed, start)
			return
		}

		if sleepMS > 0 {
			time.Sleep(time.Duration(sleepMS) * time.Millisecond)
		}

		status := http.StatusOK
		if failRate > 0 && rand.Float64() < failRate {
			status = http.StatusInternalServerError
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"backend": name})
		logRequest(logger, r, name, status, start)
	})
}

func logRequest(logger *slog.Logger, r *http.Request, name string, status int, start time.Time) {
	logger.Info("request complete",
		"backend", name,
		"method", r.Method,
		"status", status,
		"latency_ms", float64(time.Since(start).Microseconds())/1000,
		"remote_addr", r.RemoteAddr,
		"path", r.URL.Path)
}

// envInt reads key as an int, defaulting to def when unset or empty. A
// present-but-unparseable or negative value is an error so typos in compose
// fail loudly instead of silently falling back.
func envInt(key string, def int) (int, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not an integer: %w", key, raw, err)
	}
	if v < 0 {
		return 0, fmt.Errorf("%s: %d must be >= 0", key, v)
	}
	return v, nil
}

// envFloat reads key as a float64, defaulting to def when unset or empty.
// Range checking (for FAIL_RATE) is the caller's responsibility.
func envFloat(key string, def float64) (float64, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a number: %w", key, raw, err)
	}
	return v, nil
}
