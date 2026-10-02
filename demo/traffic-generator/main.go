// Command traffic-generator is one of the demo's eight traffic clients
// (S5.T16.2, ADR-0023 decision 6, ADR-0024). It sends open-loop Poisson
// arrivals at its Zipf-ranked share of a total rate, mixes response sizes and
// request-body sizes, and exposes a control endpoint that sets the total rate
// and the target LB at runtime while reporting honest delivery counters.
//
// It is stdlib-only and, like the dummy backend, builds as its own small image.
// The target LB and a total request rate come from the environment at startup;
// the total rate and target change at runtime through the control listener. The
// demo compose sets GOMEMLIMIT/GOGC on every client (ADR-0024 decision 3); this
// program expects those bounds and sets none of its own.
//
// Concurrency: the arrival loop is a single goroutine that schedules one
// arrival at a time; each dispatched request runs on its own goroutine and holds
// one in-flight semaphore slot until its response is drained. Shared mutable
// state is (a) the settings — an atomic.Pointer to an immutable
// (totalRate, target) value, read once per arrival and swapped whole by the
// control handler — and (b) the five delivery counters, sync/atomic.Int64
// incremented by request goroutines and read by the control handler. The
// in-flight semaphore is a buffered channel. math/rand/v2's top-level functions
// are safe for concurrent use (ADR-0024).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// Named demo defaults (ADR-0024 decision 5). The demo compose may override any
// of them; they are sized for a laptop with Docker Desktop defaults.
const (
	defaultAddr        = ":9090"
	defaultClients     = 8
	defaultZipfS       = 1.0
	defaultTotalRate   = 200.0
	defaultMaxInflight = 64

	// pollInterval is how often a paused client (rate 0) re-checks for a rate
	// change. maxSleep caps one scheduled wait so a rate change is picked up
	// within a second even when the current rate is very low.
	pollInterval    = 50 * time.Millisecond
	maxSleep        = time.Second
	requestTimeout  = 30 * time.Second
	shutdownTimeout = 5 * time.Second
)

// Mix weights, named in one place (ADR-0024 decision 5). Response paths are the
// dummy backend's payload endpoints; a zero body size is a GET, a non-zero one a
// discarded POST to the payload path (S5.T16.1).
var (
	responseMix = []weightedPath{
		{path: "/200b", weight: 70},
		{path: "/10kb", weight: 25},
		{path: "/1mb", weight: 5},
	}
	bodyMix = []weightedBody{
		{size: 0, weight: 70},
		{size: 1 << 10, weight: 20},
		{size: 64 << 10, weight: 10},
	}
)

type weightedPath struct {
	path   string
	weight int
}

type weightedBody struct {
	size   int
	weight int
}

// bodyPayloads are the fixed request bodies, allocated once and shared
// read-only by every POST, so a high rate does not allocate one per request.
var bodyPayloads = map[int][]byte{
	1 << 10:  make([]byte, 1<<10),
	64 << 10: make([]byte, 64<<10),
}

// config is the generator's startup configuration, from the environment.
type config struct {
	rank        int
	clients     int
	zipfS       float64
	totalRate   float64
	target      string
	allowlist   []string
	maxInflight int
	addr        string
}

// settings is the runtime-mutable part of the configuration. It is immutable
// once stored; the control handler swaps the whole value atomically, so an
// arrival never reads a half-applied change (ADR-0024 decision 4).
type settings struct {
	totalRate float64
	target    string
}

// generator is one traffic client process.
type generator struct {
	cfg    config
	share  float64
	logger *slog.Logger
	client *http.Client
	sem    chan struct{}

	settings atomic.Pointer[settings]

	offered   atomic.Int64
	sent      atomic.Int64
	dropped   atomic.Int64
	completed atomic.Int64
	failed    atomic.Int64
}

// statusJSON is the control endpoint's GET response: current settings and every
// delivery counter (ADR-0024 decision 2/4).
type statusJSON struct {
	Rank        int     `json:"rank"`
	Clients     int     `json:"clients"`
	ZipfS       float64 `json:"zipf_s"`
	TotalRate   float64 `json:"total_rate"`
	Rate        float64 `json:"rate"`
	Target      string  `json:"target"`
	MaxInflight int     `json:"max_inflight"`
	Offered     int64   `json:"offered"`
	Sent        int64   `json:"sent"`
	Dropped     int64   `json:"dropped"`
	Completed   int64   `json:"completed"`
	Errors      int64   `json:"errors"`
}

// updateJSON is the control endpoint's POST body. Pointer fields distinguish an
// omitted field, which keeps its current value, from a present zero.
type updateJSON struct {
	TotalRate *float64 `json:"total_rate"`
	Target    *string  `json:"target"`
}

func main() {
	addr := flag.String("addr", defaultAddr, "control endpoint listen address")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := configFromEnv()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	cfg.addr = *addr

	g, err := newGenerator(cfg, logger)
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           g.controlHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("control shutdown", "error", err)
		}
	}()

	go g.run(ctx)

	g.logStartup()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("control server", "error", err)
		os.Exit(1)
	}
}

// zipfShare is the client's share of the total: w_k / Σ w_i with w_k = 1/k^s
// (ADR-0024 decision 1).
func zipfShare(rank, clients int, s float64) float64 {
	var sum float64
	for i := 1; i <= clients; i++ {
		sum += math.Pow(float64(i), -s)
	}
	return math.Pow(float64(rank), -s) / sum
}

// newGenerator validates the configuration and builds the generator. An invalid
// configuration is a startup error, so a typo in compose fails loudly.
func newGenerator(cfg config, logger *slog.Logger) (*generator, error) {
	if cfg.clients < 1 {
		return nil, fmt.Errorf("CLIENTS: %d must be >= 1", cfg.clients)
	}
	if cfg.rank < 1 || cfg.rank > cfg.clients {
		return nil, fmt.Errorf("RANK: %d must be between 1 and CLIENTS (%d)", cfg.rank, cfg.clients)
	}
	if cfg.maxInflight < 1 {
		return nil, fmt.Errorf("MAX_INFLIGHT: %d must be >= 1", cfg.maxInflight)
	}
	if cfg.totalRate < 0 {
		return nil, fmt.Errorf("TOTAL_RATE: %g must be >= 0", cfg.totalRate)
	}
	if cfg.target == "" {
		return nil, errors.New("TARGET_LB: must be set")
	}
	if !contains(cfg.allowlist, cfg.target) {
		return nil, fmt.Errorf("TARGET_LB: %q is not in TARGET_ALLOWLIST", cfg.target)
	}

	g := &generator{
		cfg:    cfg,
		share:  zipfShare(cfg.rank, cfg.clients, cfg.zipfS),
		logger: logger,
		client: &http.Client{Timeout: requestTimeout},
		sem:    make(chan struct{}, cfg.maxInflight),
	}
	g.settings.Store(&settings{totalRate: cfg.totalRate, target: cfg.target})
	return g, nil
}

// ownRate is the client's current rate: its share of the current total
// (ADR-0024 decision 1).
func (g *generator) ownRate() float64 {
	return g.settings.Load().totalRate * g.share
}

// logStartup records rank, share, target and bounds (S5.T16.2).
func (g *generator) logStartup() {
	st := g.settings.Load()
	g.logger.Info("traffic generator starting",
		"rank", g.cfg.rank,
		"clients", g.cfg.clients,
		"zipf_s", g.cfg.zipfS,
		"share", g.share,
		"total_rate", st.totalRate,
		"rate", g.ownRate(),
		"target", st.target,
		"max_inflight", g.cfg.maxInflight,
		"addr", g.cfg.addr,
	)
}

// run schedules open-loop Poisson arrivals until ctx is cancelled (ADR-0024
// decision 1). Each arrival is due independently of earlier requests, so a slow
// backend is seen as latency and in-flight count, not as reduced load.
func (g *generator) run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		rate := g.ownRate()
		if rate <= 0 {
			if !sleepCtx(ctx, pollInterval) {
				return
			}
			continue
		}
		// Clamp in seconds before converting: a very low rate would otherwise
		// overflow the Duration and busy-loop on a negative wait.
		seconds := rand.ExpFloat64() / rate
		if seconds > maxSleep.Seconds() {
			seconds = maxSleep.Seconds()
		}
		if !sleepCtx(ctx, time.Duration(seconds*float64(time.Second))) {
			return
		}
		g.arrival(ctx)
	}
}

// arrival dispatches one due request, or counts it dropped when the in-flight
// bound is full. The arrival is never queued (ADR-0024 decision 2).
func (g *generator) arrival(ctx context.Context) {
	g.offered.Add(1)
	select {
	case g.sem <- struct{}{}:
		g.sent.Add(1)
		target := g.settings.Load().target
		go func() {
			defer func() { <-g.sem }()
			g.send(ctx, target)
		}()
	default:
		g.dropped.Add(1)
	}
}

// send performs one request against the target and drains its response body.
func (g *generator) send(ctx context.Context, target string) {
	path, method, body := pickRequest()

	var rdr io.Reader
	if method == http.MethodPost {
		rdr = bytes.NewReader(bodyPayloads[body])
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(target, "/")+path, rdr)
	if err != nil {
		g.failed.Add(1)
		return
	}

	resp, err := g.client.Do(req)
	if err != nil {
		g.failed.Add(1)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	g.completed.Add(1)
}

// pickRequest chooses a response path and a request body by weight; a zero body
// is a GET, a non-zero one a POST to the payload path (ADR-0024 decision 5).
func pickRequest() (path, method string, body int) {
	path = pickWeightedPath()
	body = pickWeightedBody()
	method = http.MethodGet
	if body > 0 {
		method = http.MethodPost
	}
	return path, method, body
}

func pickWeightedPath() string {
	total := 0
	for _, w := range responseMix {
		total += w.weight
	}
	n := rand.IntN(total)
	for _, w := range responseMix {
		if n < w.weight {
			return w.path
		}
		n -= w.weight
	}
	return responseMix[len(responseMix)-1].path
}

func pickWeightedBody() int {
	total := 0
	for _, w := range bodyMix {
		total += w.weight
	}
	n := rand.IntN(total)
	for _, w := range bodyMix {
		if n < w.weight {
			return w.size
		}
		n -= w.weight
	}
	return bodyMix[len(bodyMix)-1].size
}

// controlHandler is the client's internal control endpoint. GET returns the
// current settings and counters; POST updates the total rate and/or target,
// rejecting unknown fields, a negative rate and a target outside the allowlist
// with 400 (ADR-0024 decision 4).
func (g *generator) controlHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			g.writeStatus(w)
		case http.MethodPost:
			g.handleUpdate(w, r)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func (g *generator) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var in updateJSON
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	// An empty body is the degenerate "all fields omitted" case and is a no-op.
	if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	next := *g.settings.Load()
	if in.TotalRate != nil {
		if *in.TotalRate < 0 {
			http.Error(w, "total_rate must be >= 0", http.StatusBadRequest)
			return
		}
		next.totalRate = *in.TotalRate
	}
	if in.Target != nil {
		if !contains(g.cfg.allowlist, *in.Target) {
			http.Error(w, "target is not in the allowlist", http.StatusBadRequest)
			return
		}
		next.target = *in.Target
	}
	g.settings.Store(&next)
	g.writeStatus(w)
}

func (g *generator) writeStatus(w http.ResponseWriter) {
	st := g.settings.Load()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(statusJSON{
		Rank:        g.cfg.rank,
		Clients:     g.cfg.clients,
		ZipfS:       g.cfg.zipfS,
		TotalRate:   st.totalRate,
		Rate:        st.totalRate * g.share,
		Target:      st.target,
		MaxInflight: g.cfg.maxInflight,
		Offered:     g.offered.Load(),
		Sent:        g.sent.Load(),
		Dropped:     g.dropped.Load(),
		Completed:   g.completed.Load(),
		Errors:      g.failed.Load(),
	})
}

// sleepCtx waits for d or until ctx is done, reporting whether the full wait
// elapsed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// configFromEnv reads the startup configuration with the dummy backend's
// strictness: empty is treated as unset, and every error names the variable
// (S5.T16.2).
func configFromEnv() (config, error) {
	var cfg config

	clients, err := envInt("CLIENTS", defaultClients)
	if err != nil {
		return cfg, err
	}
	cfg.clients = clients

	rank, err := envIntRequired("RANK")
	if err != nil {
		return cfg, err
	}
	if rank < 1 || rank > clients {
		return cfg, fmt.Errorf("RANK: %d must be between 1 and CLIENTS (%d)", rank, clients)
	}
	cfg.rank = rank

	zipfS, err := envFloat("ZIPF_S", defaultZipfS)
	if err != nil {
		return cfg, err
	}
	cfg.zipfS = zipfS

	totalRate, err := envFloat("TOTAL_RATE", defaultTotalRate)
	if err != nil {
		return cfg, err
	}
	if totalRate < 0 {
		return cfg, fmt.Errorf("TOTAL_RATE: %g must be >= 0", totalRate)
	}
	cfg.totalRate = totalRate

	maxInflight, err := envInt("MAX_INFLIGHT", defaultMaxInflight)
	if err != nil {
		return cfg, err
	}
	if maxInflight < 1 {
		return cfg, fmt.Errorf("MAX_INFLIGHT: %d must be >= 1", maxInflight)
	}
	cfg.maxInflight = maxInflight

	allowlist, err := envCSV("TARGET_ALLOWLIST")
	if err != nil {
		return cfg, err
	}
	cfg.allowlist = allowlist

	target, err := envRequired("TARGET_LB")
	if err != nil {
		return cfg, err
	}
	if !contains(allowlist, target) {
		return cfg, fmt.Errorf("TARGET_LB: %q is not in TARGET_ALLOWLIST", target)
	}
	cfg.target = target

	return cfg, nil
}

// envInt reads key as an int, defaulting to def when unset or empty. A
// present-but-unparseable or negative value is an error naming the variable.
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

// envIntRequired reads key as an int, erroring when unset or empty.
func envIntRequired(key string) (int, error) {
	raw, err := envRequired(key)
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not an integer: %w", key, raw, err)
	}
	return v, nil
}

// envFloat reads key as a float64, defaulting to def when unset or empty.
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

// envRequired reads key, erroring when unset or empty.
func envRequired(key string) (string, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return "", fmt.Errorf("%s: must be set", key)
	}
	return raw, nil
}

// envCSV reads a required non-empty comma-separated list, trimming surrounding
// space from each entry.
func envCSV(key string) ([]string, error) {
	raw, err := envRequired(key)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: must list at least one URL", key)
	}
	return out, nil
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
