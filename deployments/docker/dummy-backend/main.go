// Command dummy-backend is a lightweight, stdlib-only HTTP server used to
// exercise the load balancer end-to-end without real upstreams (S1.T9).
//
// It identifies itself in every response and can inject artificial latency
// and failures, so selection-algorithm differences and (from Sprint 3)
// resilience behavior are observable on `docker compose up` with no manual
// configuration.
//
// Concurrency: net/http serves each request on its own goroutine, so handler
// invocations run concurrently. The only shared mutable state is the
// per-process arrival counter — a sync/atomic.Int64 incremented on entry and
// read with Load (S5.T5.5.2). Each backend runs as its own container, so one
// counter is the whole story. math/rand/v2's top-level functions are safe for
// concurrent use.
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
	"sync/atomic"
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

// statsResponse is the /stats body: the backend's name and how many benchmark
// requests have arrived at it (S5.T5.5.2).
type statsResponse struct {
	Backend  string `json:"backend"`
	Requests int64  `json:"requests"`
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

	logRequests, err := envBool("LOG_REQUESTS", true)
	if err != nil {
		logger.Error("invalid LOG_REQUESTS", "error", err)
		os.Exit(1)
	}

	certFile, keyFile, err := tlsFilesFromEnv()
	if err != nil {
		logger.Error("invalid TLS configuration", "error", err)
		os.Exit(1)
	}

	b := newBackend(*name, profile{sleepMS: sleepMS, failRate: failRate}, logRequests, logger)
	server := &http.Server{
		Addr:              *addr,
		Handler:           b.handler(),
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
		"log_requests", logRequests, "tls", certFile != "")

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

// profile is one immutable snapshot of a backend's injected chaos: the base
// sleep, the uniform jitter applied around it, and the per-request failure
// probability. It is held behind an atomic.Pointer and swapped whole, so a
// request reads one consistent (sleep, jitter, fail_rate) triple even while the
// admin listener is changing it (S5.T16.1).
type profile struct {
	sleepMS  int
	jitterMS int
	failRate float64
}

// backend is one dummy-backend process. It owns the identity and logging switch
// and the two pieces of shared mutable state: the arrival counter (incremented
// on entry, read by /stats) and the chaos profile (read once per request, written
// by the admin listener when ADMIN_ENABLED is set). net/http serves each request
// on its own goroutine, so both are safe for concurrent use; each backend runs as
// its own container, so one counter and one profile are the whole story
// (S5.T5.5.2, S5.T16.1).
type backend struct {
	name        string
	logger      *slog.Logger
	logRequests bool
	arrivals    atomic.Int64
	profile     atomic.Pointer[profile]
}

// newBackend builds the backend with its initial chaos profile. The env values
// (SLEEP_MS, FAIL_RATE, jitter 0) are that initial profile; with ADMIN_ENABLED
// unset nothing can change it, so runtime behaviour is unchanged (S5.T16.1).
func newBackend(name string, initial profile, logRequests bool, logger *slog.Logger) *backend {
	b := &backend{name: name, logger: logger, logRequests: logRequests}
	b.profile.Store(&initial)
	return b
}

// newHandler returns the dummy backend's HTTP handler bound to a fresh initial
// profile, with no admin listener. Every GET except /health and /stats sleeps
// for sleepMS, then fails with HTTP 500 with probability failRate. /health is
// the always-200, chaos-free probe target; /stats reports the arrival count of
// benchmark requests. The paths /200b, /10kb, and /1mb serve fixed-size
// pre-generated bodies; every other path answers with a JSON body identifying
// the backend so distribution stays observable even through failing responses.
// Non-GET requests get a 405.
func newHandler(name string, sleepMS int, failRate float64, logRequests bool, logger *slog.Logger) http.Handler {
	return newBackend(name, profile{sleepMS: sleepMS, failRate: failRate}, logRequests, logger).handler()
}

// handler returns the request handler served on the backend's proxied port. It
// reads the chaos profile once per request, so a concurrent admin write cannot
// change the sleep and failure of a request already in flight.
func (b *backend) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			logRequest(b.logger, b.logRequests, r, b.name, http.StatusMethodNotAllowed, start)
			return
		}

		// /health is the LB health-checker's target; it must stay cheap and
		// always-200, so it deliberately bypasses the injected sleep/failure.
		// Otherwise an injected failure rate would make the LB flap.
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok\n"))
			logRequest(b.logger, b.logRequests, r, b.name, http.StatusOK, start)
			return
		}

		// /stats reports the arrival count. Like /health it is a control
		// endpoint, so it bypasses the injected sleep/failure and is not itself
		// counted.
		if r.URL.Path == "/stats" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(statsResponse{Backend: b.name, Requests: b.arrivals.Load()})
			logRequest(b.logger, b.logRequests, r, b.name, http.StatusOK, start)
			return
		}

		// Arrival: counted before any injected latency or failure decision, so a
		// request still in flight is already visible to /stats.
		b.arrivals.Add(1)

		p := b.profile.Load()
		if p.sleepMS > 0 {
			time.Sleep(time.Duration(p.sleepMS) * time.Millisecond)
		}

		status := http.StatusOK
		if p.failRate > 0 && rand.Float64() < p.failRate {
			status = http.StatusInternalServerError
		}

		// Response-size endpoints serve a pre-generated body so benchmarks can
		// vary payload profile without changing the backend image.
		if payload, ok := payloads[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(status)
			_, _ = w.Write(payload)
			logRequest(b.logger, b.logRequests, r, b.name, status, start)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"backend": b.name})
		logRequest(b.logger, b.logRequests, r, b.name, status, start)
	})
}

// logRequest is the single gate for every per-request line, so switching
// LOG_REQUESTS off silences all paths (health and 405 included) by short-
// circuiting here rather than testing the flag at each call site.
func logRequest(logger *slog.Logger, logRequests bool, r *http.Request, name string, status int, start time.Time) {
	if !logRequests {
		return
	}
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

// envBool reads key as a bool, defaulting to def when unset or empty. Only the
// exact strings "true" and "false" are accepted; anything else is an error so a
// typo in compose fails loudly instead of silently taking a default (the
// SLEEP_MS/FAIL_RATE convention).
func envBool(key string, def bool) (bool, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def, nil
	}
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s: %q is not true or false", key, raw)
	}
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
