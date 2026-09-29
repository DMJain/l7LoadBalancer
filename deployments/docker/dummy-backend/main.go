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

// payloads are the pre-generated bodies for the benchmark response-size
// endpoints (S5.T4-infra). Built once at package init so a benchmark measures
// transport overhead, not per-request allocation. Sizes are exact: 200 B,
// 10 KiB, 1 MiB.
var payloads = map[string][]byte{
	"/200b": make([]byte, 200),
	"/10kb": make([]byte, 10*1024),
	"/1mb":  make([]byte, 1<<20),
}

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

	certFile, keyFile, err := tlsFilesFromEnv()
	if err != nil {
		logger.Error("invalid TLS configuration", "error", err)
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
		"backend", *name, "addr", *addr, "sleep_ms", sleepMS, "fail_rate", failRate,
		"tls", certFile != "")

	// server.TLSNextProto is deliberately left nil: ListenAndServeTLS
	// auto-configures HTTP/2 via ALPN only while it is nil. Setting it to an
	// empty map silently disables HTTP/2 server-side — the server-side twin of
	// the client's ForceAttemptHTTP2 gotcha (S5.T4-infra, spec §20).
	var serveErr error
	if certFile != "" {
		serveErr = server.ListenAndServeTLS(certFile, keyFile)
	} else {
		serveErr = server.ListenAndServe()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		logger.Error("server", "error", serveErr)
		os.Exit(1)
	}
}

// tlsFilesFromEnv reads TLS_CERT_FILE and TLS_KEY_FILE. Both set enables TLS
// via ListenAndServeTLS; neither set means plain HTTP; exactly one set is an
// error so a half-configured TLS deployment fails loudly at startup, matching
// the SLEEP_MS/FAIL_RATE convention.
func tlsFilesFromEnv() (certFile, keyFile string, err error) {
	certFile = os.Getenv("TLS_CERT_FILE")
	keyFile = os.Getenv("TLS_KEY_FILE")
	switch {
	case certFile == "" && keyFile == "":
		return "", "", nil
	case certFile == "":
		return "", "", errors.New("TLS_KEY_FILE is set but TLS_CERT_FILE is not")
	case keyFile == "":
		return "", "", errors.New("TLS_CERT_FILE is set but TLS_KEY_FILE is not")
	}
	return certFile, keyFile, nil
}

// newHandler returns the dummy backend's HTTP handler. Every GET except
// /health sleeps for sleepMS, then fails with HTTP 500 with probability
// failRate. /health is the always-200, chaos-free probe target. The paths
// /200b, /10kb, and /1mb serve fixed-size pre-generated bodies; every other
// path answers with a JSON body identifying the backend so distribution stays
// observable even through failing responses. Non-GET requests get a 405.
func newHandler(name string, sleepMS int, failRate float64, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			logRequest(logger, r, name, http.StatusMethodNotAllowed, start)
			return
		}

		// /health is the LB health-checker's target; it must stay cheap and
		// always-200, so it deliberately bypasses SLEEP_MS/FAIL_RATE. Otherwise
		// an injected failure rate would make the LB flap.
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok\n"))
			logRequest(logger, r, name, http.StatusOK, start)
			return
		}

		if sleepMS > 0 {
			time.Sleep(time.Duration(sleepMS) * time.Millisecond)
		}

		status := http.StatusOK
		if failRate > 0 && rand.Float64() < failRate {
			status = http.StatusInternalServerError
		}

		// Response-size endpoints serve a pre-generated body so benchmarks can
		// vary payload profile without changing the backend image.
		if payload, ok := payloads[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(status)
			_, _ = w.Write(payload)
			logRequest(logger, r, name, status, start)
			return
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
