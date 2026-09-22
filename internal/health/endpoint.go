package health

import (
	"encoding/json"
	"net/http"

	"github.com/DMJain/l7LoadBalancer/internal/backend"
	"github.com/DMJain/l7LoadBalancer/internal/metrics"
)

// Probe paths. K8s-native names, so every orchestrator's probe field maps 1:1:
// Docker HEALTHCHECK targets /livez, Fly.io's HTTP check targets /readyz, and
// Kubernetes maps all three to distinct probe configs (ADR-0014 decisions 2 and
// 3).
const (
	pathLivez    = "/livez"
	pathReadyz   = "/readyz"
	pathStartupz = "/startupz"
)

// liveBody is /livez's unadorned response. It carries no checks: liveness is
// unconditional 200, and a deadlocked handler would not answer at all, so the
// probe timeout is the failure signal (ADR-0014 decision 3).
type liveBody struct {
	Status string `json:"status"`
}

// statusBody is the shared /readyz and /startupz envelope: a top-level status
// plus a per-check breakdown, so an operator can see *which* condition failed
// without correlating logs and metrics (ADR-0014 decision 7).
type statusBody struct {
	Status string     `json:"status"`
	Checks checksBody `json:"checks"`
}

// checksBody is the per-check breakdown. SelectableBackends is a pointer so
// /startupz can omit it (it does not gate on the selectable set) while /readyz
// always includes it, even at zero — an omitted field and a zero count must not
// look the same to the orchestrator.
type checksBody struct {
	ConfigLoaded         bool `json:"config_loaded"`
	InitialProbeComplete bool `json:"initial_probe_complete"`
	SelectableBackends   *int `json:"selectable_backends,omitempty"`
}

// endpoint serves the three orchestrator probe paths. It is immutable after
// construction and holds only references to goroutine-safe collaborators (the
// checker's atomic latch, the registry's snapshot reads, the collector's
// instruments), so all three handlers are safe for concurrent use.
type endpoint struct {
	checker      *Checker
	reg          *backend.Registry
	collector    *metrics.Collector
	configLoaded bool
}

// NewHandler returns the orchestrator probe handler for the load balancer's own
// health listener: a mux serving /livez, /readyz, and /startupz (ADR-0014
// decision 1). Each hit is recorded on lb_health_probe_total, and no hit enters
// the proxy path, so probe traffic never reaches lb_requests_total or the
// request-latency histogram (decisions 1 and 8).
//
// configLoaded is the fact that config.Load and config.Validate succeeded. The
// process exits before serving if they did not, so this is true in production;
// it is a parameter rather than a constant so the contract can be exercised and
// so a future reload path can report it honestly.
func NewHandler(checker *Checker, reg *backend.Registry, configLoaded bool, collector *metrics.Collector) http.Handler {
	e := &endpoint{checker: checker, reg: reg, collector: collector, configLoaded: configLoaded}
	mux := http.NewServeMux()
	mux.HandleFunc(pathLivez, e.livez)
	mux.HandleFunc(pathReadyz, e.readyz)
	mux.HandleFunc(pathStartupz, e.startupz)
	return mux
}

// livez always answers 200 {"status":"alive"} (ADR-0014 decision 3).
func (e *endpoint) livez(w http.ResponseWriter, _ *http.Request) {
	e.writeJSON(w, pathLivez, http.StatusOK, liveBody{Status: "alive"})
}

// startupz is a one-shot gate: 503 until config is loaded and the first active
// probe round is complete, then permanently 200. It deliberately does not gate
// on the selectable set — startup is a one-way transition, readiness is
// continuous (ADR-0014 decisions 4 and 6). Permanence follows from both inputs
// being monotone: ProbeRoundComplete latches true for the checker's lifetime,
// and configLoaded is fixed at construction. If a future reload path ever makes
// config state dynamic, it must be latched at the source, or this gate would
// regress 200→503 against decision 4's intent.
func (e *endpoint) startupz(w http.ResponseWriter, _ *http.Request) {
	initialProbeComplete := e.checker.ProbeRoundComplete()
	e.writeStatus(w, pathStartupz, e.configLoaded && initialProbeComplete, checksBody{
		ConfigLoaded:         e.configLoaded,
		InitialProbeComplete: initialProbeComplete,
	})
}

// readyz gates on the startup conditions plus a live selectable-set check, so
// 200 means this instance can actually serve client traffic right now. An empty
// selectable set is 503, which lets a fronting LB or K8s Service route around a
// fully-evicted instance (ADR-0014 decisions 5 and 6).
func (e *endpoint) readyz(w http.ResponseWriter, _ *http.Request) {
	initialProbeComplete := e.checker.ProbeRoundComplete()
	count := len(e.reg.Selectable())
	e.writeStatus(w, pathReadyz, e.configLoaded && initialProbeComplete && count >= 1, checksBody{
		ConfigLoaded:         e.configLoaded,
		InitialProbeComplete: initialProbeComplete,
		SelectableBackends:   &count,
	})
}

// writeStatus writes the shared ready/not_ready envelope and records the probe.
func (e *endpoint) writeStatus(w http.ResponseWriter, endpoint string, ready bool, checks checksBody) {
	code := http.StatusServiceUnavailable
	status := "not_ready"
	if ready {
		code = http.StatusOK
		status = "ready"
	}
	e.writeJSON(w, endpoint, code, statusBody{Status: status, Checks: checks})
}

// writeJSON writes body as JSON with the given status code and records the
// probe response. The encode error can only be a write failure after the status
// line is already committed, so there is nothing left to do but drop it.
func (e *endpoint) writeJSON(w http.ResponseWriter, endpoint string, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
	e.collector.RecordProbe(endpoint, code)
}
