// Command control-service is the local live demo's control page and JSON API
// (S5.T16.4.1–T16.4.2, ADR-0023 decisions 8 and 9, ADR-0025 and its amendment).
// It serves one embedded static page from which the owner switches the active
// LB, sets the total load rate, sets each backend's latency/jitter/failure, and
// kills or revives a backend — no terminal needed — and reads the current state
// back from the peers that hold it.
//
// It is stdlib-only and, like the dummy backend and the traffic generator,
// builds as its own small image. It holds no desired configuration: the eight
// generators, four admin listeners, and the Docker Engine are the source of
// truth, and this service reads them live. The active LB and the total rate are
// derived and reported only when every generator agrees, so a partial fan-out
// shows as mixed rather than as the last value asked for (ADR-0025 decisions 1
// and 4).
//
// Concurrency: every request is served on its own goroutine by net/http. A
// fan-out launches one goroutine per peer and each writes only its own slot of
// the results slice, so no shared mutable state is touched concurrently and no
// mutex is needed. The service itself keeps no mutable state; the Engine client
// wraps an *http.Client, which is safe for concurrent use.
package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
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

// defaultDockerHost is Docker's own convention for the Engine endpoint. The demo
// mounts the socket and uses this default; tests set an http URL (S5.T16.4.2).
const defaultDockerHost = "unix:///var/run/docker.sock"

// defaultContainerPrefix maps a backend name to its Docker container name. The
// demo compose pins each backend's container_name to prefix+name so the
// allowlist is a stated string, not a computed one (ADR-0025 amendment d8).
const defaultContainerPrefix = "l7loadbalancer-demo-"

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
	// The demo runs one LB whose algorithm changes by reload (S5.T20.8,
	// ADR-0023 decision 14). LBS and LB_ADMINS still take a list.
	defaultLBs = []string{"http://lb:8080"}
	// defaultLBAdmins are the LBs' metrics listeners, which serve /metrics and
	// the reload admin routes (ADR-0025 decisions 12 and 16). Same order as
	// defaultLBs.
	defaultLBAdmins = []string{"http://lb:9090"}
)

// invalidReloadBody is the one config the invalid-reload action ever sends
// (ADR-0025 decision 13). Every field is valid except the algorithm, so the
// LB's validation rejects exactly that and its running config never changes.
// It is a constant: nothing a caller sends reaches an LB's reload path.
const invalidReloadBody = `listen: ":8080"
algorithm: "p3c_ewma"
backends:
  - name: "backend1"
    url: "http://backend1:8080"
`

// defaultGrafanaURL is the browser-reachable Grafana (host loopback, not the
// internal `http://grafana:3000`): the iframes are loaded by the owner's
// browser, not by this service (ADR-0025 decision 5).
const defaultGrafanaURL = "http://127.0.0.1:3000"

// peer is one named peer: a generator's control endpoint, a backend's admin
// listener, or an LB's client URL. The name is what the page and the fan-out
// response report. container is the backend's Docker container name, derived
// from the name and CONTAINER_PREFIX and empty for generators and LBs
// (S5.T16.4.2).
type peer struct {
	name      string
	url       string
	container string
}

// config is the control service's startup configuration.
type config struct {
	generators []peer
	backends   []peer
	lbs        []peer
	// lbAdmins are the LBs' metrics listeners, index-paired with lbs and
	// named after them (S5.T20.2).
	lbAdmins   []peer
	grafanaURL string
	addr       string
	// dockerHost is the Docker Engine endpoint: a unix socket in the demo, an
	// http URL in tests (S5.T16.4.2).
	dockerHost string
}

// server serves the page and the JSON API. It is immutable after construction.
type server struct {
	cfg    config
	logger *slog.Logger
	client *http.Client
	engine *engineClient
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

// containerResponse is the kill/revive action's response: whether the Engine
// accepted the call, and on failure its error, so the page shows a failed
// action rather than swallowing it (S5.T16.4.2, ADR-0025 amendment decision 9).
type containerResponse struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Container string `json:"container"`
}

// engineClient talks to the Docker Engine API with the standard library's HTTP
// client — no Docker SDK (S5.T16.4.2, ADR-0025 amendment decision 7). In the
// demo it dials the unix socket; tests point it at an httptest stand-in. It is
// safe for concurrent use: http.Client is.
type engineClient struct {
	baseURL string
	client  *http.Client
}

// newEngine builds the Engine client from a DOCKER_HOST value. A unix:// host
// dials the socket with a custom DialContext; an http/https host is used
// directly (the test seam).
func newEngine(dockerHost string) (*engineClient, error) {
	u, err := url.Parse(dockerHost)
	if err != nil {
		return nil, fmt.Errorf("invalid docker host %q: %w", dockerHost, err)
	}
	switch u.Scheme {
	case "unix":
		sock := u.Path
		tr := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		}
		return &engineClient{baseURL: "http://docker", client: &http.Client{Transport: tr, Timeout: requestTimeout}}, nil
	case "http", "https":
		return &engineClient{baseURL: strings.TrimRight(dockerHost, "/"), client: &http.Client{Timeout: requestTimeout}}, nil
	default:
		return nil, fmt.Errorf("docker host %q: unsupported scheme (want unix, http or https)", dockerHost)
	}
}

// kill stops a container abruptly — the Engine's kill, not stop (S5.T16.4.2).
func (e *engineClient) kill(ctx context.Context, container string) error {
	return e.post(ctx, "/containers/"+url.PathEscape(container)+"/kill")
}

// start starts an existing container again (revive).
func (e *engineClient) start(ctx context.Context, container string) error {
	return e.post(ctx, "/containers/"+url.PathEscape(container)+"/start")
}

// state returns the container's Engine status ("running", "exited", ...).
func (e *engineClient) state(ctx context.Context, container string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.baseURL+"/containers/"+url.PathEscape(container)+"/json", nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call engine: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("engine returned %s", resp.Status)
	}
	var body struct {
		State struct {
			Status string `json:"Status"`
		} `json:"State"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode engine response: %w", err)
	}
	return body.State.Status, nil
}

// post issues a bodyless Engine POST. A 2xx is success; 304 (already started) is
// also success; anything else is an error carrying the status text.
func (e *engineClient) post(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("call engine: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusNotModified {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("engine returned %s", resp.Status)
	}
	return nil
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
	// LastReload is the LB's GET /admin/reload/last answer; LastReloadError
	// replaces it when the read failed (S5.T20.2).
	LastReload      *lastReload `json:"last_reload,omitempty"`
	LastReloadError string      `json:"last_reload_error,omitempty"`
	// Algorithm is the algorithm the LB reports through lb_algorithm_info;
	// AlgorithmError replaces it when the read failed (S5.T20.8).
	Algorithm      string `json:"algorithm,omitempty"`
	AlgorithmError string `json:"algorithm_error,omitempty"`
}

// lastReload is the LB's last-reload record (S5.T20.1, ADR-0015 decision 16).
type lastReload struct {
	Result string `json:"result"`
	Time   string `json:"time,omitempty"`
	Error  string `json:"error,omitempty"`
}

// reloadInvalidResponse is the invalid-reload action's response: which LB was
// asked, and its answer, or Error alone when the call itself failed.
type reloadInvalidResponse struct {
	LB     string `json:"lb"`
	Result string `json:"result,omitempty"`
	Time   string `json:"time,omitempty"`
	Error  string `json:"error,omitempty"`
}

// backendState and generatorState embed the peer's own contract type, so a
// change to the admin or generator status shape cannot drift from what the page
// renders. Container is the Engine container name; ContainerState is its
// running/exited status, with ContainerError when the Engine call failed
// (S5.T16.4.2).
type backendState struct {
	Name string `json:"name"`
	backendProfile
	Container      string `json:"container"`
	ContainerState string `json:"container_state,omitempty"`
	ContainerError string `json:"container_error,omitempty"`
	Error          string `json:"error,omitempty"`
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
	for _, b := range cfg.backends {
		if b.container == "" {
			return nil, fmt.Errorf("backend %q has no container name", b.name)
		}
	}
	if len(cfg.lbs) == 0 {
		return nil, errors.New("at least one LB is required")
	}
	if len(cfg.lbAdmins) != len(cfg.lbs) {
		return nil, fmt.Errorf("LB_ADMINS lists %d URLs but LBS lists %d; they must pair one to one", len(cfg.lbAdmins), len(cfg.lbs))
	}
	if cfg.grafanaURL == "" {
		return nil, errors.New("grafana URL is required")
	}
	if cfg.dockerHost == "" {
		cfg.dockerHost = defaultDockerHost
	}
	engine, err := newEngine(cfg.dockerHost)
	if err != nil {
		return nil, err
	}
	return &server{
		cfg:    cfg,
		logger: logger,
		client: &http.Client{Timeout: requestTimeout},
		engine: engine,
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
	mux.HandleFunc("POST /api/backend/kill", s.handleKill)
	mux.HandleFunc("POST /api/backend/revive", s.handleRevive)
	mux.HandleFunc("POST /api/reload-invalid", s.handleReloadInvalid)
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

// handleKill stops a backend's container abruptly (S5.T16.4.2).
func (s *server) handleKill(w http.ResponseWriter, r *http.Request) {
	s.handlePower(w, r, "kill")
}

// handleRevive starts a backend's container again (S5.T16.4.2).
func (s *server) handleRevive(w http.ResponseWriter, r *http.Request) {
	s.handlePower(w, r, "revive")
}

// handlePower validates the backend against the allowlist before any Engine call
// and then kills or starts its container. The container name is derived from the
// validated backend, never taken from the request. A failed Engine call is
// reported as a failed action, never swallowed (S5.T16.4.2, ADR-0025 amendment
// decisions 8 and 9).
func (s *server) handlePower(w http.ResponseWriter, r *http.Request, action string) {
	var in struct {
		Backend string `json:"backend"`
	}
	if !decodeStrict(w, r, &in) {
		return
	}
	backend, ok := s.backendByName(in.Backend)
	if !ok {
		http.Error(w, "unknown backend", http.StatusBadRequest)
		return
	}
	var err error
	if action == "kill" {
		err = s.engine.kill(r.Context(), backend.container)
	} else {
		err = s.engine.start(r.Context(), backend.container)
	}
	if err != nil {
		writeJSON(w, containerResponse{OK: false, Error: err.Error(), Container: backend.container})
		return
	}
	writeJSON(w, containerResponse{OK: true, Container: backend.container})
}

// handleState reads every peer live and derives the active LB and total rate
// only when the generators agree (ADR-0025 decision 1).
func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	generators := s.gatherGenerators(ctx)
	backends := s.gatherBackends(ctx)

	lbs := s.gatherLBs(ctx)

	writeJSON(w, stateResponse{
		GrafanaURL: s.cfg.grafanaURL,
		ActiveLB:   activeLB(generators, s.cfg.lbs),
		TotalRate:  commonRate(generators),
		LBs:        lbs,
		Backends:   backends,
		Generators: generators,
	})
}

// gatherLBs lists the LBs with each one's last reload and current algorithm,
// read from its metrics listener concurrently; a failed read is reported on
// that LB (ADR-0025 decisions 12 and 16).
func (s *server) gatherLBs(ctx context.Context) []peerJSON {
	lbs := make([]peerJSON, len(s.cfg.lbs))
	forEachPeer(s.cfg.lbAdmins, func(i int, admin peer) {
		base := strings.TrimRight(admin.url, "/")
		lb := peerJSON{Name: s.cfg.lbs[i].name, Target: s.cfg.lbs[i].url}
		var last lastReload
		if _, err := s.call(ctx, http.MethodGet, base+"/admin/reload/last", nil, &last); err != nil {
			lb.LastReloadError = err.Error()
		} else {
			lb.LastReload = &last
		}
		if algorithm, err := s.readAlgorithm(ctx, base+"/metrics"); err != nil {
			lb.AlgorithmError = err.Error()
		} else {
			lb.Algorithm = algorithm
		}
		lbs[i] = lb
	})
	return lbs
}

// readAlgorithm fetches an LB's Prometheus exposition and returns the
// algorithm lb_algorithm_info reports (ADR-0025 decision 16).
func (s *server) readAlgorithm(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", errors.New(resp.Status)
	}
	return parseAlgorithm(resp.Body)
}

// algorithmSeries is the exposition prefix of the LB's algorithm gauge
// (ADR-0013 amendment, S5.T20.7).
const algorithmSeries = `lb_algorithm_info{algorithm="`

// parseAlgorithm returns the label of the lb_algorithm_info series whose value
// is 1. The gauge has exactly one such series (the LB sets the new one before
// deleting the old), so the first match is the answer.
func parseAlgorithm(r io.Reader) (string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		rest, ok := strings.CutPrefix(line, algorithmSeries)
		if !ok {
			continue
		}
		name, value, ok := strings.Cut(rest, `"} `)
		if ok && strings.TrimSpace(value) == "1" {
			return name, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read metrics: %w", err)
	}
	return "", errors.New("no lb_algorithm_info series")
}

// handleReloadInvalid posts the fixed invalid config to the active LB and
// returns its answer (ADR-0025 decision 13). It takes no input: an empty body
// or `{}` is accepted and any field is a 400. With no agreed active LB it is a
// 409 and no LB is called. The LB's 422 is the expected answer, so it is
// reported in a 200 body like every other action's outcome (decision 4).
func (s *server) handleReloadInvalid(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<10))
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&struct{}{}); err != nil {
			http.Error(w, "this action takes no input", http.StatusBadRequest)
			return
		}
	}

	active := activeLB(s.gatherGenerators(r.Context()), s.cfg.lbs)
	if active == "" {
		http.Error(w, "no active LB: the generators disagree", http.StatusConflict)
		return
	}
	var admin peer
	for i, lb := range s.cfg.lbs {
		if lb.name == active {
			admin = s.cfg.lbAdmins[i]
		}
	}

	out := reloadInvalidResponse{LB: active}
	var answer lastReload
	if _, err := s.call(r.Context(), http.MethodPost, strings.TrimRight(admin.url, "/")+"/admin/reload",
		strings.NewReader(invalidReloadBody), &answer); err != nil {
		out.Error = err.Error()
	} else {
		out.Result, out.Time, out.Error = answer.Result, answer.Time, answer.Error
	}
	writeJSON(w, out)
}

// call performs one request against an exact URL (no trailing-slash rewrite,
// unlike doJSON) and decodes a JSON answer into out. Any status with a JSON
// body is decoded, because the LB reports a rejected reload as a 422 with a
// JSON body; a non-JSON non-2xx answer is an error carrying the status.
func (s *server) call(ctx context.Context, method, rawURL string, body io.Reader, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/yaml")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("call %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, errors.New(resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return resp.StatusCode, fmt.Errorf("decode %s: %w", rawURL, err)
	}
	return resp.StatusCode, nil
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

// gatherBackends reads all four profiles and their Engine container states
// concurrently. The admin listener takes POST only, so a read is a POST with an
// empty object — every field omitted, every field kept (S5.T16.1). The Engine
// state is read beside each profile; a failed read is reported, not fatal
// (S5.T16.4.2).
func (s *server) gatherBackends(ctx context.Context) []backendState {
	states := make([]backendState, len(s.cfg.backends))
	forEachPeer(s.cfg.backends, func(i int, p peer) {
		st := backendState{Name: p.name, Container: p.container}
		if err := s.doJSON(ctx, http.MethodPost, p.url, map[string]any{}, &st.backendProfile); err != nil {
			st.Error = err.Error()
		}
		if state, err := s.engine.state(ctx, p.container); err != nil {
			st.ContainerError = err.Error()
		} else {
			st.ContainerState = state
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
	prefix := envString("CONTAINER_PREFIX", defaultContainerPrefix)
	for i := range backends {
		backends[i].container = prefix + backends[i].name
	}
	lbs, err := envPeers("LBS", defaultLBs)
	if err != nil {
		return config{}, err
	}
	lbAdmins, err := envPeers("LB_ADMINS", defaultLBAdmins)
	if err != nil {
		return config{}, err
	}
	// An admin peer is named after the LB it pairs with, so the page and the
	// state report one name per LB (ADR-0025 decision 12).
	for i := range lbAdmins {
		if i < len(lbs) {
			lbAdmins[i].name = lbs[i].name
		}
	}
	return config{
		generators: generators,
		backends:   backends,
		lbs:        lbs,
		lbAdmins:   lbAdmins,
		grafanaURL: envString("GRAFANA_URL", defaultGrafanaURL),
		dockerHost: envString("DOCKER_HOST", defaultDockerHost),
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
