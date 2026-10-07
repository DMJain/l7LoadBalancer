# ADR-0013: Observability: metrics collector, transition logging, and their integration

**Status:** Accepted

## Context

The health checker, the outlier detector and the circuit breaker isolate failures, but without metrics and logs nobody outside the process can see them. The package graph makes `internal/metrics` a leaf with no internal imports. So `metrics` cannot read `Backend` state, and the request-path packages must push values into it.

Two more facts shape the design. The `RoundTripObserver` fan-out runs only around a real backend round trip, so it misses the two 503 short circuits (no healthy backend, circuit denied). Also, the lazy Open to Half-Open promotion (ADR-0011, ADR-0012) has no single call site with a logger. A `Registry.Selectable()` scan can win the same CAS.

## Decision

### Metrics

1. `internal/metrics` is a push-only `Collector`. Other packages call its methods (`ObserveRequest`, `SetBackendHealthy`, `SetCircuitState`, `IncActiveRequests`, and so on). It never reads `backend`, `config` or `proxy` state and imports no internal package.
2. Each `Collector` owns a private `prometheus.NewRegistry()`, never `prometheus.DefaultRegisterer`. There is no global state, so tests can make independent instances and run with `t.Parallel()`. The `promhttp` handler uses the same private registry.
3. `lb_requests_total` and `lb_request_duration_seconds` come from one whole-request hook in `proxy.ServeHTTP`. The duration covers selection, the circuit check and the backend round trip. `RoundTripObserver` keeps its own backend-leg timing for internal use only (P2C-EWMA, passive detection, the circuit). So the metrics show one definition of "request duration".
   - `lb_request_duration_seconds` labels: `backend`, `method`, `status_class`.
   - `lb_requests_total` labels: the same three plus `protocol` (`http/1.1`, `h2`, `h2c`, the closed `metrics.ClientProtocol` set). A dashboard can then prove that HTTP/2 was negotiated. HTTP/1.0 is labelled `http/1.1`.
   - There is never a `status_code` label, because its cardinality is unbounded.
4. The histogram buckets are provisional: `.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10` seconds.
5. `lb_circuit_state` is a label-enum gauge (`backend`, `state` in `closed`/`open`/`half_open`). The setter writes all three series on every call: `1` for the current state, `0` for the others. So exactly one state is always `1`, whatever the caller does. A numeric single gauge was rejected because it needs an encoding table and cannot be queried by state name.
6. `lb_backend_healthy` is a plain per-backend 0/1 gauge.
7. `lb_active_requests` (label `backend`) counts in-flight requests. The proxy changes it at the same call sites as `Backend.IncActive()`/`DecActive()`, so it cannot drift from the real count.
8. `metrics.listen` (default `:9090`) serves `promhttp.HandlerFor` on its own `http.Server`, apart from client traffic. It is always on, with no disable switch, like `health` and `circuit`.
9. At startup, each backend's gauges are seeded through the normal setters: `SetBackendHealthy` (1), `SetCircuitState` (closed) and `SetActiveRequests(backend, 0)`. Prometheus `Vec` metrics show no series until the first write. Seeding makes the first scrape of a healthy system show a full dashboard.

### Transition logging

10. `Backend.CircuitFailure`, `CircuitSuccess` and `CircuitAllow` also return a `backend.CircuitTransition`. The values are:

    | Constant | Transition | Log reason |
    |---|---|---|
    | `CircuitNoChange` | none | none |
    | `CircuitOpened` | Closed to Open | `consecutive_failures` |
    | `CircuitReopened` | Half-Open to Open | `trial_failure` |
    | `CircuitClosed` | Half-Open to Closed | `trial_success` |
    | `CircuitHalfOpened` | Open to Half-Open | `cooldown_elapsed` |

    Each log reason maps to exactly one value. `internal/backend` gets no logging dependency. `circuit.Breaker` (`circuit.New` takes a `*slog.Logger`) logs when the value is not `CircuitNoChange`. The `CircuitGate` and `RoundTripObserver` interfaces do not change. `CircuitAllow` reports `CircuitHalfOpened` only when its own CAS did the promotion, so exactly one concurrent caller reports it.
11. The active checker logs a transition when a counter equals the threshold (`==`), not at `>=`. So a long failure or success streak logs once. The passive `OutlierDetector` logs at its existing once-per-episode `ejected` guard.
12. The canonical log fields include `event` and `reason`. Both are closed snake_case vocabularies in `internal/logger`:
    - `event`: `health_ejected`, `health_reinstated`, `circuit_opened`, `circuit_closed`, `circuit_half_opened`.
    - `reason`: `probe_failures`, `probe_recovered` (active), `outlier_window` (passive), `consecutive_failures`, `trial_success`, `trial_failure`, `cooldown_elapsed` (circuit).
    - `health_ejected` and `circuit_opened` log at WARN. `health_reinstated`, `circuit_closed` and `circuit_half_opened` log at INFO.
13. Known gap: a Half-Open promotion won by the `Registry.Selectable()` read path is never logged. `Backend.CircuitOpen()` returns `bool` and has no logger. This is an accepted limitation.

### Integration

14. The whole-request hook uses the existing `ServeHTTP` timer and deferred hook. The proxy holds a `*metrics.Collector`. There is no `RequestObserver` interface, because it would have only one implementation. A request with no healthy backend is counted with `backend=""`. A circuit-denied request is counted with the real backend name.
15. `health.Checker`, `health.OutlierDetector` and `circuit.Breaker` get a `*metrics.Collector` and a `*slog.Logger` through their constructors. They update gauges at the same edge-triggered points where they log. A log line and a gauge for one transition therefore always agree.
16. `internal/balancer` does not import `metrics`. The proxy sees `ErrNoHealthyBackends` and records it.

### Dashboard and compose stacks

17. `deployments/docker/observability/` holds Prometheus and Grafana with provisioned datasource and dashboard. Prometheus scrapes the load balancer every 15s. The dashboard shows request rate by `backend`/`status_class`, p50/p99 latency by `backend`, circuit state, backend health, active requests, and request share by backend. It has a `window` variable (default `5m`) and an `lb` variable. There are no alert rules and no TLS or auth on the metrics endpoint (ADR-0005).
18. The repo-root `docker-compose.yml` runs the LB image, three dummy backends, Prometheus and Grafana with one command. The compose files under `deployments/docker/` stay for the host-process flows (`make run`, the chaos scripts).
    - It uses `prometheus-stack.yml`, which differs from `prometheus.yml` only in the target `l7lb:9090`.
    - It mounts the shared Grafana provisioning and dashboard directories read-only, so there is one source of truth.
    - It publishes `8080` (client), `8081` (health) and `3000` (Grafana). Prometheus, `:9090` and the backends stay internal.
    - Prometheus waits for the LB to be healthy. Every service has `restart: unless-stopped`.
    - `./configs/docker.yaml` is mounted over `/etc/l7lb/config.yaml`, so config edits need no rebuild.

### Admin routes and the algorithm gauge

19. The metrics listener also serves `net/http/pprof` and the reload routes `POST /admin/reload` and `GET /admin/reload/last` (ADR-0015). Anyone who can reach this listener can replace the backend list. This is accepted because the listener is operational and unpublished. The repo-root, bench and demo compose files never publish it. An operator who runs the bare binary should bind `metrics.listen` to loopback. It is not a production control plane, and ADR-0005 keeps authentication out of scope. The last reload outcome is served as JSON, not as a metric, because its error text cannot be a label value.
20. `lb_algorithm_info{algorithm}` is 1 for the selection algorithm in use. `Build` seeds it, and a reload that changes the algorithm moves it (ADR-0015). `SetAlgorithm(previous, current)` sets the new series to 1 first and then deletes the old one. A scrape therefore never sees zero algorithms, and the `Collector` keeps no state. Cardinality is bounded by the four algorithms.

## Consequences

- The package graph does not change. `metrics` stays a leaf, and `balancer` does not change.
- There is one definition of request duration, one detection point for each transition (shared by the log and the gauge) and one seeding path. Each removes a source of drift.
- Private registries make the collector easy to test in parallel.
- `Backend`, `circuit.New` and the `health` constructors grow a parameter or a return value.
- Metric and log values are closed Go vocabularies. A new transition needs edits in `internal/logger` and in its producer.
- Half-Open promotions won by a `Selectable()` scan are not logged.
- Rejected alternatives:
  - Letting `metrics` import `backend` (breaks the leaf rule).
  - `DefaultRegisterer` (global state).
  - Exposing the backend-leg duration as the histogram (two meanings of latency).
  - Logging from `Backend` directly (`backend` must stay a plain state holder).
