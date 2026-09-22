// Package metrics registers and exposes Prometheus instruments for
// request counts, latency histograms, circuit breaker state, and active
// connections.
//
// Label name reservations. Metric labels MUST use these names:
//
//	backend       - backend name
//	method        - HTTP method
//	status_class  - "2xx" / "4xx" / "5xx" etc. (NOT status_code — cardinality)
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
//	lb_active_connections   - added in S3.T4 (ADR-0013 decision 7)
//	lb_health_probe_total   - added in S3.T12 (ADR-0014)
//
// See docs/design/sprint-1-contracts.md "Metric name and label
// reservations" for latency bucket boundaries. Implemented in Sprint 3;
// the Collector is built in S3.T4 (ADR-0013).
package metrics
