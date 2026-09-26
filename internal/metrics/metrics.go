package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// CircuitState is the closed set of lb_circuit_state label values. It is a
// distinct type rather than a bare string so an unrecognized state is a
// deliberate conversion, not an easy accident. The values are snake_case and
// match internal/circuit's state vocabulary, but this package stays a leaf and
// never imports the circuit package (ADR-0013 decision 5).
type CircuitState string

const (
	CircuitStateClosed   CircuitState = "closed"
	CircuitStateOpen     CircuitState = "open"
	CircuitStateHalfOpen CircuitState = "half_open"
)

// circuitStates is the closed set of lb_circuit_state label values. The setter
// writes every one of them on every call so exactly one is 1 and the others
// are 0 (ADR-0013 decision 5).
var circuitStates = []CircuitState{CircuitStateClosed, CircuitStateOpen, CircuitStateHalfOpen}

// valid reports whether s is one of the three known states.
func (s CircuitState) valid() bool {
	for _, known := range circuitStates {
		if s == known {
			return true
		}
	}
	return false
}

// histogramBuckets are the provisional request-duration boundaries in seconds,
// reserved in doc.go and documented as provisional pending Sprint 5's real
// benchmark data (ADR-0013 decision 4).
var histogramBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// Collector owns a private Prometheus registry and exposes push methods for
// every load-balancer instrument. It is a leaf: it imports no other internal
// package, and callers push state in rather than the collector reading it
// (ADR-0013 decision 1).
//
// Concurrency: the underlying prometheus instruments are goroutine-safe, and
// the registry is never mutated after construction, so a Collector is safe for
// concurrent use. Each Collector has its own registry, so independent instances
// never collide and tests may run in parallel (ADR-0013 decision 2).
type Collector struct {
	registry *prometheus.Registry

	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	healthy  *prometheus.GaugeVec
	circuit  *prometheus.GaugeVec
	active   *prometheus.GaugeVec
	probes   *prometheus.CounterVec
}

// NewCollector returns a Collector over its own private registry. Nothing is
// registered on prometheus.DefaultRegisterer, so construction is side-effect
// free and parallel-safe (ADR-0013 decision 2).
//
// No error is returned: every instrument is registered at construction and a
// collision is impossible within a fresh registry, so MustRegister's panic is
// unreachable by construction.
func NewCollector() *Collector {
	reg := prometheus.NewRegistry()
	c := &Collector{
		registry: reg,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lb_requests_total",
			Help: "Total requests handled by the load balancer, by backend, method, and response status class.",
		}, []string{"backend", "method", "status_class"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lb_request_duration_seconds",
			Help:    "Whole-request duration in seconds, from dispatch to response, by backend, method, and status class.",
			Buckets: histogramBuckets,
		}, []string{"backend", "method", "status_class"}),
		healthy: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lb_backend_healthy",
			Help: "Whether a backend is healthy (1) or unhealthy (0).",
		}, []string{"backend"}),
		circuit: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lb_circuit_state",
			Help: "Circuit breaker state per backend as a label enum; exactly one of closed/open/half_open is 1.",
		}, []string{"backend", "state"}),
		active: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lb_active_connections",
			Help: "In-flight requests currently being served by a backend.",
		}, []string{"backend"}),
		probes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lb_health_probe_total",
			Help: "Total health-endpoint probe responses, by probe endpoint and response status class.",
		}, []string{"endpoint", "status"}),
	}
	reg.MustRegister(c.requests, c.duration, c.healthy, c.circuit, c.active, c.probes)
	return c
}

// Registry returns the private registry for exposition via
// promhttp.HandlerFor (ADR-0013 decision 8).
func (c *Collector) Registry() *prometheus.Registry {
	return c.registry
}

// ObserveRequest records one whole-request outcome: the request counter
// increments and the duration is observed on the same label set. d is the
// whole client-facing request duration, not the backend round trip
// (ADR-0013 decision 3).
func (c *Collector) ObserveRequest(backend, method, statusClass string, d time.Duration) {
	c.requests.WithLabelValues(backend, method, statusClass).Inc()
	c.duration.WithLabelValues(backend, method, statusClass).Observe(d.Seconds())
}

// SetBackendHealthy sets the per-backend healthy gauge to 1 when healthy and 0
// otherwise (ADR-0013 decision 6).
func (c *Collector) SetBackendHealthy(backend string, healthy bool) {
	value := 0.0
	if healthy {
		value = 1
	}
	c.healthy.WithLabelValues(backend).Set(value)
}

// DeleteBackendHealthy removes a backend's lb_backend_healthy series. Reload
// calls it when a backend leaves the fleet, so its dashboard entry stops
// rendering; deleting a backend with no series is a no-op.
func (c *Collector) DeleteBackendHealthy(backend string) {
	c.healthy.DeleteLabelValues(backend)
}

// DeleteCircuitState removes every lb_circuit_state series for a backend — all
// three state labels — for the same reason DeleteBackendHealthy exists. Neither
// deletion touches the active-connections series: a removed backend's in-flight
// requests still decrement it by name until its drain completes (S4.T4).
func (c *Collector) DeleteCircuitState(backend string) {
	for _, s := range circuitStates {
		c.circuit.DeleteLabelValues(backend, string(s))
	}
}

// SetCircuitState sets the per-backend circuit-state label enum, writing every
// known state on every call — the target to 1 and the other two to 0 — so the
// exactly-one-state-is-1 invariant holds even if a caller only knows the new
// state (ADR-0013 decision 5). An unrecognized state is ignored, leaving the
// previous series untouched rather than zeroing all three and breaking the
// invariant.
func (c *Collector) SetCircuitState(backend string, state CircuitState) {
	if !state.valid() {
		return
	}
	for _, s := range circuitStates {
		value := 0.0
		if s == state {
			value = 1
		}
		c.circuit.WithLabelValues(backend, string(s)).Set(value)
	}
}

// IncActiveConnections increments the per-backend active-connections gauge. It
// is called at the same call site as Backend.IncActive (ADR-0013 decision 7).
func (c *Collector) IncActiveConnections(backend string) {
	c.active.WithLabelValues(backend).Inc()
}

// DecActiveConnections decrements the per-backend active-connections gauge,
// mirroring Backend.DecActive.
func (c *Collector) DecActiveConnections(backend string) {
	c.active.WithLabelValues(backend).Dec()
}

// SetActiveConnections sets the per-backend active-connections gauge directly.
// Startup seeding uses it to materialize a fresh backend's series at 0 before
// any request has been served (ADR-0013 decision 9).
func (c *Collector) SetActiveConnections(backend string, n int64) {
	c.active.WithLabelValues(backend).Set(float64(n))
}

// RecordProbe records one health-endpoint probe response against the
// lb_health_probe_total counter. The status label is the same status-class
// vocabulary ObserveRequest uses ("2xx", "5xx", …), not the raw code, so the
// new counter stays visually consistent with lb_requests_total and its
// cardinality stays bounded (3 endpoints × ~2 classes) (ADR-0013 decision 3,
// ADR-0014 (S3.T12)).
func (c *Collector) RecordProbe(endpoint string, statusCode int) {
	c.probes.WithLabelValues(endpoint, statusClass(statusCode)).Inc()
}

// statusClass maps an HTTP status code to its class label ("2xx", "5xx"),
// matching the status_class vocabulary. It is a deliberate one-line duplicate
// of internal/proxy's unexported helper: this package is a leaf and may not
// import internal/proxy (ADR-0013 decision 1).
func statusClass(status int) string {
	return strconv.Itoa(status/100) + "xx"
}
