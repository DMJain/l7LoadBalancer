// Command control-service is the local live demo's control page and JSON API
// (S5.T16.4.1, ADR-0023 decisions 8 and 9, ADR-0025). It serves one embedded
// static page from which the owner switches the active LB, sets the total load
// rate, and sets each backend's latency/jitter/failure — no terminal needed —
// and reads the current state back from the peers that hold it.
//
// It is stdlib-only and, like the dummy backend and the traffic generator,
// builds as its own small image. It holds no desired configuration: the eight
// generators and four admin listeners are the source of truth, and this service
// reads them live. The active LB and the total rate are derived and reported
// only when every generator agrees, so a partial fan-out shows as mixed rather
// than as the last value asked for (ADR-0025 decisions 1 and 4).
//
// Concurrency: every request is served on its own goroutine by net/http. A
// fan-out launches one goroutine per peer and each writes only its own slot of
// the results slice, so no shared mutable state is touched concurrently and no
// mutex is needed. The service itself keeps no mutable state.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// defaultAddr is the control service's listen address inside its container. The
// demo compose publishes it on 127.0.0.1 only (ADR-0023 decision 2).
const defaultAddr = ":8090"

// requestTimeout bounds every outbound peer call. The state endpoint reads
// twelve peers per poll; without a bound a hung peer would stall the page.
const requestTimeout = 5 * time.Second

//go:embed page.html
var pageHTML string

// Demo topology defaults, matching demo/docker-compose.yml. An LB's identity is
// the hostname of its URL — the compose service name, which is also its
// Prometheus `job` and dashboard `$lb` value (ADR-0025 decision 2).
var (
	defaultGenerators = []string{
		"http://gen1:9090", "http://gen2:9090", "http://gen3:9090", "http://gen4:9090",
		"http://gen5:9090", "http://gen6:9090", "http://gen7:9090", "http://gen8:9090",
	}
	defaultBackends = []string{
		"http://backend1:9091", "http://backend2:9091",
		"http://backend3:9091", "http://backend4:9091",
	}
	defaultLBs = []string{
		"http://lb-roundrobin:8080", "http://lb-leastconn:8080",
		"http://lb-consistent-hash:8080", "http://lb-p2c-ewma:8080",
	}
)

// defaultGrafanaURL is the browser-reachable Grafana (host loopback, not the
// internal `http://grafana:3000`): the iframes are loaded by the owner's
// browser, not by this service (ADR-0025 decision 5).
const defaultGrafanaURL = "http://127.0.0.1:3000"

// peer is one named peer: a generator's control endpoint, a backend's admin
// listener, or an LB's client URL. The name is what the page and the fan-out
// response report.
type peer struct {
	name string
	url  string
}

// config is the control service's startup configuration.
type config struct {
	generators []peer
	backends   []peer
	lbs        []peer
	grafanaURL string
	addr       string
}

// server serves the page and the JSON API. It is immutable after construction.
type server struct {
	cfg    config
	logger *slog.Logger
	client *http.Client
}

// fanOutResponse is the LB/rate actions' response: whether every peer accepted
// the update, the per-peer result, and the names of every peer that failed
// (ADR-0025 decision 4).
type fanOutResponse struct {
	OK      bool         `json:"ok"`
	Results []peerResult `json:"results"`
	Failed  []string     `json:"failed"`
}

type peerResult struct {
	Peer  string `json:"peer"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// backendProfile is a backend's chaos profile, the admin listener's contract
// (S5.T16.1).
type backendProfile struct {
	SleepMS  int     `json:"sleep_ms"`
	JitterMS int     `json:"jitter_ms"`
	FailRate float64 `json:"fail_rate"`
}

// backendResponse is the profile action's response.
type backendResponse struct {
	OK      bool           `json:"ok"`
	Error   string         `json:"error,omitempty"`
	Profile backendProfile `json:"profile"`
}

// stateResponse is GET /api/state: the live snapshot the page renders. ActiveLB
// is empty and TotalRate is null when the generators disagree (ADR-0025
// decision 1).
type stateResponse struct {
	GrafanaURL string           `json:"grafana_url"`
	ActiveLB   string           `json:"active_lb"`
	TotalRate  *float64         `json:"total_rate"`
	LBs        []peerJSON       `json:"lbs"`
	Backends   []backendState   `json:"backends"`
	Generators []generatorState `json:"generators"`
}

type peerJSON struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

// backendState and generatorState embed the peer's own contract type, so a
// change to the admin or generator status shape cannot drift from what the page
// renders.
type backendState struct {
	Name string `json:"name"`
	backendProfile
	Error string `json:"error,omitempty"`
}

type generatorState struct {
	Name string `json:"name"`
	generatorStatus
	Error string `json:"error,omitempty"`
}

// generatorStatus is the subset of the generator's status the control service
// reads (S5.T16.2).
type generatorStatus struct {
	TotalRate float64 `json:"total_rate"`
	Target    string  `json:"target"`
	Offered   int64   `json:"offered"`
	Sent      int64   `json:"sent"`
	Dropped   int64   `json:"dropped"`
	Errors    int64   `json:"errors"`
}

func main() {
	addr := flag.String("addr", defaultAddr, "control page listen address")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := configFromEnv()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	cfg.addr = *addr

	s, err := newServer(cfg, logger)
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           s.handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("control shutdown", "error", err)
		}
	}()

	logger.Info("control service starting",
		"addr", cfg.addr,
		"generators", len(cfg.generators),
		"backends", len(cfg.backends),
		"lbs", len(cfg.lbs),
		"grafana_url", cfg.grafanaURL,
	)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("control server", "error", err)
		os.Exit(1)
	}
}

// newServer validates the configuration and builds the service. An empty peer
// set is a startup error so a misconfigured compose fails loudly.
func newServer(cfg config, logger *slog.Logger) (*server, error) {
	if len(cfg.generators) == 0 {
		return nil, errors.New("at least one generator is required")
	}
	if len(cfg.backends) == 0 {
		return nil, errors.New("at least one backend is required")
	}
	if len(cfg.lbs) == 0 {
		return nil, errors.New("at least one LB is required")
	}
	if cfg.grafanaURL == "" {
		return nil, errors.New("grafana URL is required")
	}
	return &server{
		cfg:    cfg,
		logger: logger,
		client: &http.Client{Timeout: requestTimeout},
	}, nil
}

// handler routes the page and the JSON API.
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handlePage)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/lb", s.handleLB)
	mux.HandleFunc("POST /api/rate", s.handleRate)
	mux.HandleFunc("POST /api/backend", s.handleBackend)
	return mux
}

// handlePage serves the embedded page. The `GET /` pattern is a catch-all, so
// any other unmatched GET path is a 404 rather than the page.
func (s *server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, pageHTML)
}

// handleLB switches the active LB by sending its target to every generator
// (ADR-0025 decision 3).
func (s *server) handleLB(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LB string `json:"lb"`
	}
	if !decodeStrict(w, r, &in) {
		return
	}
	lb, ok := s.lbByName(in.LB)
	if !ok {
		http.Error(w, "unknown lb", http.StatusBadRequest)
		return
	}
	results := s.fanOut(r.Context(), s.cfg.generators, map[string]string{"target": lb.url})
	writeFanOut(w, results)
}

// handleRate sets the total rate on every generator; each derives its own share
// (ADR-0025 decision 3).
func (s *server) handleRate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TotalRate *float64 `json:"total_rate"`
	}
	if !decodeStrict(w, r, &in) {
		return
	}
	if in.TotalRate == nil {
		http.Error(w, "total_rate is required", http.StatusBadRequest)
		return
	}
	if *in.TotalRate < 0 {
		http.Error(w, "total_rate must be >= 0", http.StatusBadRequest)
		return
	}
	results := s.fanOut(r.Context(), s.cfg.generators, map[string]float64{"total_rate": *in.TotalRate})
	writeFanOut(w, results)
}

// backendUpdate is the profile action's request body. Pointer fields distinguish
// an omitted field, which keeps its value, from a present zero (S5.T16.1).
type backendUpdate struct {
	Backend  string   `json:"backend"`
	SleepMS  *int     `json:"sleep_ms"`
	JitterMS *int     `json:"jitter_ms"`
	FailRate *float64 `json:"fail_rate"`
}

// handleBackend sets one backend's profile by forwarding to its admin listener
// (ADR-0025 decision 3). Validation mirrors the admin listener's contract, and
// omitted fields keep their value.
func (s *server) handleBackend(w http.ResponseWriter, r *http.Request) {
	var in backendUpdate
	if !decodeStrict(w, r, &in) {
		return
	}
	backend, ok := s.backendByName(in.Backend)
	if !ok {
		http.Error(w, "unknown backend", http.StatusBadRequest)
		return
	}
	if in.SleepMS != nil && *in.SleepMS < 0 {
		http.Error(w, "sleep_ms must be >= 0", http.StatusBadRequest)
		return
	}
	if in.JitterMS != nil && *in.JitterMS < 0 {
		http.Error(w, "jitter_ms must be >= 0", http.StatusBadRequest)
		return
	}
	if in.FailRate != nil && (*in.FailRate < 0 || *in.FailRate > 1) {
		http.Error(w, "fail_rate must be between 0 and 1", http.StatusBadRequest)
		return
	}

	forward := map[string]any{}
	if in.SleepMS != nil {
		forward["sleep_ms"] = *in.SleepMS
	}
	if in.JitterMS != nil {
		forward["jitter_ms"] = *in.JitterMS
	}
	if in.FailRate != nil {
		forward["fail_rate"] = *in.FailRate
	}

	var prof backendProfile
	if err := s.doJSON(r.Context(), http.MethodPost, backend.url, forward, &prof); err != nil {
		writeJSON(w, backendResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, backendResponse{OK: true, Profile: prof})
}

// handleState reads every peer live and derives the active LB and total rate
// only when the generators agree (ADR-0025 decision 1).
func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	generators := s.gatherGenerators(ctx)
	backends := s.gatherBackends(ctx)

	lbs := make([]peerJSON, 0, len(s.cfg.lbs))
	for _, lb := range s.cfg.lbs {
		lbs = append(lbs, peerJSON{Name: lb.name, Target: lb.url})
	}

	writeJSON(w, stateResponse{
		GrafanaURL: s.cfg.grafanaURL,
		ActiveLB:   activeLB(generators, s.cfg.lbs),
		TotalRate:  commonRate(generators),
		LBs:        lbs,
		Backends:   backends,
		Generators: generators,
	})
}

// forEachPeer runs fn for every peer concurrently and waits. Each call writes
// only its own slot, so no lock is needed and one slow peer never blocks the
// rest.
func forEachPeer(peers []peer, fn func(i int, p peer)) {
	var wg sync.WaitGroup
	for i, p := range peers {
		wg.Add(1)
		go func(i int, p peer) {
			defer wg.Done()
			fn(i, p)
		}(i, p)
	}
	wg.Wait()
}

// gatherGenerators reads all eight generators concurrently, each writing its own
// slot.
func (s *server) gatherGenerators(ctx context.Context) []generatorState {
	states := make([]generatorState, len(s.cfg.generators))
	forEachPeer(s.cfg.generators, func(i int, p peer) {
		st := generatorState{Name: p.name}
		if err := s.doJSON(ctx, http.MethodGet, p.url, nil, &st.generatorStatus); err != nil {
			st.Error = err.Error()
		}
		states[i] = st
	})
	return states
}

// gatherBackends reads all four profiles concurrently. The admin listener takes
// POST only, so a read is a POST with an empty object — every field omitted,
// every field kept (S5.T16.1).
func (s *server) gatherBackends(ctx context.Context) []backendState {
	states := make([]backendState, len(s.cfg.backends))
	forEachPeer(s.cfg.backends, func(i int, p peer) {
		st := backendState{Name: p.name}
		if err := s.doJSON(ctx, http.MethodPost, p.url, map[string]any{}, &st.backendProfile); err != nil {
			st.Error = err.Error()
		}
		states[i] = st
	})
	return states
}

// fanOut sends body to every peer concurrently and returns one result per peer,
// in configuration order. Every peer is attempted even when one fails; a failed
// peer's error is captured, never fatal (ADR-0025 decision 4).
func (s *server) fanOut(ctx context.Context, peers []peer, body any) []peerResult {
	results := make([]peerResult, len(peers))
	forEachPeer(peers, func(i int, p peer) {
		res := peerResult{Peer: p.name}
		if err := s.doJSON(ctx, http.MethodPost, p.url, body, nil); err != nil {
			res.Error = err.Error()
		} else {
			res.OK = true
		}
		results[i] = res
	})
	return results
}

// doJSON performs one JSON request against a peer. A non-2xx response is an
// error carrying the status text; the body is always drained.
func (s *server) doJSON(ctx context.Context, method, rawURL string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(rawURL, "/")+"/", rdr)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return errors.New(resp.Status)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// decodeStrict rejects unknown fields and malformed bodies with a 400, before
// any outbound call is made (ADR-0025 decision 3).
func decodeStrict(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return false
	}
	return true
}

// writeFanOut reports the fan-out honestly: ok is false and every failed peer is
// named when any peer failed (ADR-0025 decision 4).
func writeFanOut(w http.ResponseWriter, results []peerResult) {
	failed := []string{}
	ok := true
	for _, res := range results {
		if !res.OK {
			ok = false
			failed = append(failed, res.Peer)
		}
	}
	writeJSON(w, fanOutResponse{OK: ok, Results: results, Failed: failed})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// activeLB returns the LB name when every generator answered and their targets
// agree on a configured LB, and "" otherwise.
func activeLB(states []generatorState, lbs []peer) string {
	if len(states) == 0 {
		return ""
	}
	for _, st := range states {
		if st.Error != "" || st.Target != states[0].Target {
			return ""
		}
	}
	for _, lb := range lbs {
		if lb.url == states[0].Target {
			return lb.name
		}
	}
	return ""
}

// commonRate returns the shared total rate when every generator answered with
// the same value, and nil otherwise.
func commonRate(states []generatorState) *float64 {
	if len(states) == 0 {
		return nil
	}
	for _, st := range states {
		if st.Error != "" || st.TotalRate != states[0].TotalRate {
			return nil
		}
	}
	rate := states[0].TotalRate
	return &rate
}

func (s *server) lbByName(name string) (peer, bool) {
	for _, lb := range s.cfg.lbs {
		if lb.name == name {
			return lb, true
		}
	}
	return peer{}, false
}

func (s *server) backendByName(name string) (peer, bool) {
	for _, b := range s.cfg.backends {
		if b.name == name {
			return b, true
		}
	}
	return peer{}, false
}

// configFromEnv builds the configuration from the environment, defaulting to the
// demo's topology so the binary runs unconfigured for local rehearsal.
func configFromEnv() (config, error) {
	generators, err := envPeers("GENERATORS", defaultGenerators)
	if err != nil {
		return config{}, err
	}
	backends, err := envPeers("BACKENDS", defaultBackends)
	if err != nil {
		return config{}, err
	}
	lbs, err := envPeers("LBS", defaultLBs)
	if err != nil {
		return config{}, err
	}
	return config{
		generators: generators,
		backends:   backends,
		lbs:        lbs,
		grafanaURL: envString("GRAFANA_URL", defaultGrafanaURL),
	}, nil
}

// envPeers reads a comma-separated list of base URLs, defaulting when unset or
// empty. Each peer's name is its URL hostname (ADR-0025 decision 2).
func envPeers(key string, def []string) ([]peer, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return peersFromURLs(def)
	}
	var urls []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			urls = append(urls, p)
		}
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("%s: must list at least one URL", key)
	}
	return peersFromURLs(urls)
}

func peersFromURLs(urls []string) ([]peer, error) {
	peers := make([]peer, 0, len(urls))
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid peer URL %q: %w", raw, err)
		}
		if u.Hostname() == "" {
			return nil, fmt.Errorf("peer URL %q has no host", raw)
		}
		peers = append(peers, peer{name: u.Hostname(), url: raw})
	}
	return peers, nil
}

func envString(key, def string) string {
	if raw, ok := os.LookupEnv(key); ok && raw != "" {
		return raw
	}
	return def
}
