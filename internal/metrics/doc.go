// Package metrics registers and exposes Prometheus instruments for
// request counts, latency histograms, circuit breaker state, and active
// requests.
//
// Label name reservations. Metric labels MUST use these names:
//
//	backend       - backend name
//	method        - HTTP method
//	status_class  - "2xx" / "4xx" / "5xx" etc. (NOT status_code — cardinality)
//	protocol      - client wire protocol: "http/1.1" / "h2" / "h2c"
//	                (lb_requests_total only; closed at
//	                three values so cardinality stays bounded)
//	endpoint      - health-probe path (lb_health_probe_total only)
//	status        - health-probe status class, "2xx"/"5xx" etc.
//	                (lb_health_probe_total only; deliberately its own name,
//	                not status_class, since a probe has no method/backend)
//
// Metric name prefix reservations:
//
//	lb_requests_total
//	lb_request_duration_seconds
//	lb_backend_healthy
//	lb_circuit_state
//	lb_active_requests      - formerly lb_active_connections (ADR-0013 decision 7)
//	lb_health_probe_total   - ADR-0014
//
// See docs/architecture.md "Metric name and label
// reservations" for latency bucket boundaries. The Collector is
// described in ADR-0013.
package metrics
