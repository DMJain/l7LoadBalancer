# ADR-0014: Health endpoint contract and probe semantics

- **Status**: Accepted
- **Date**: 2026-09-22
- **Deciders**: Darshan Jain (project owner) + opencode agent (S3.T12, design session recorded in `.scratch/s3-t10-t13-demo-stack/spec.md`)

## Context

Sprint 3's resilience subsystems are observable inside the process (metrics on
`:9090`, structured transition logs) but the process answers no question a
container orchestrator can ask. There is no `GET` that says "I am alive", "I am
ready to accept traffic", or "I have finished starting up" — only the proxy
listener on `:8080` (which answers every path with a proxied response or a 503)
and the Prometheus listener on `:9090`. A Docker `HEALTHCHECK`, a Kubernetes
probe, or a Fly.io HTTP check has nothing in the orchestrator-native shape to
target, so `MILESTONES.md`'s Sprint 3 exit criterion "the health endpoint
returns structured JSON with liveness and readiness semantics" is unmet.

The load balancer already carries the signals such an endpoint needs. The active
checker (`health.Checker`, ADR-0011) knows whether the first full probe sweep
has completed; `backend.Registry.Selectable()` knows, live, whether any backend
is currently eligible (healthy and circuit-not-open); `config.Validate` has
already run before the process serves. What is missing is a contract that
exposes those signals, a listener to serve it on, and a counter so probe traffic
is visible without polluting the request-rate metrics.

This ADR records the decisions for the health-endpoint workstream (S3.T12)
before implementation begins, per `AGENTS.md` Step 2. It is written from the
design-session record in the spec above (spec decisions D2–D12).

## Decision

1. **The health endpoint is a separate listener, not a path on the proxy.**
   A third `http.Server` binds `health_endpoint.listen` (default `:8081`) in
   `main`, mirroring the metrics server exactly: constructed in `main`, started
   in its own goroutine after the client and metrics servers, sharing `sigCtx`,
   and joining the same `Shutdown` sequence. Sprint 4's future `Run(ctx, cfg)`
   seam will absorb all three listeners uniformly. Probes therefore never enter
   the proxy path.

2. **Three probe paths, named `/livez`, `/readyz`, `/startupz`.** The names are
   Kubernetes-native, so every orchestrator's probe field maps 1:1:
   - Docker `HEALTHCHECK` → `/livez`.
   - Fly.io HTTP check → `/readyz`.
   - Kubernetes maps all three to distinct probe fields
     (`livenessProbe`/`readinessProbe`/`startupProbe`).

3. **`/livez` returns 200 unconditionally** with body `{"status":"alive"}`. Go
   gives no general deadlock signal, but a deadlocked handler would not respond
   at all, so the probe *timeout* is the failure signal. A watchdog that tracks
   request-loop progress is speculative complexity with nothing to tune it
   against, and is deliberately not built.

4. **`/startupz` is a one-shot gate.** It returns 503 until *both*
   `config_loaded` and `initial_probe_complete` hold, transitions to 200 at that
   moment, and stays 200 for the process lifetime — including after a later
   fleet-wide eviction, since it does not gate on the selectable set.
   `config_loaded` is the fact that `config.Load`+`Validate` succeeded, which
   the handler carries as a construction-time boolean (the process exits before
   serving if it did not). `initial_probe_complete` is
   `Checker.ProbeRoundComplete()`. The handler re-evaluates the conjunction per
   request; its permanence is supplied by both inputs being monotone —
   `ProbeRoundComplete()` is a one-shot latch, and `configLoaded` is fixed at
   construction. A future reload path that makes config state dynamic must latch
   it at the source, or `/startupz` would regress 200→503 against this decision.

5. **`/readyz` gates on the startup conditions plus a live selectable-set
   check.** 200 only when all of `{config_loaded, initial_probe_complete,
   len(Registry.Selectable()) >= 1}` hold at query time; otherwise 503. The
   selectable-set check runs live per request so it reflects health and circuit
   state right now, not a startup snapshot. This is the same condition
   `ErrNoHealthyBackends` already surfaces to clients as a 503.

6. **An empty selectable set makes `/readyz` return 503, which enables upstream
   failover.** A fronting LB or a Kubernetes Service removes this instance from
   rotation during a full-cluster outage, so it does not receive traffic it can
   only answer with 503. `/startupz` deliberately does *not* share this gate —
   startup is a one-way transition, readiness is continuous.

7. **Structured JSON with a per-check breakdown, pinned exactly:**

   ```json
   {
     "status": "ready",
     "checks": {
       "config_loaded": true,
       "initial_probe_complete": true,
       "selectable_backends": 3
     }
   }
   ```

   On 503 the same envelope carries `"status": "not_ready"` and the failing
   check(s) stay visible (e.g. `"selectable_backends": 0`). `/startupz` uses the
   same envelope minus `selectable_backends` — it does not gate on that field.
   `/livez` returns `{"status": "alive"}` unadorned. The per-check body is what
   lets an operator see *which* readiness condition failed without correlating
   logs and metrics.

8. **`lb_health_probe_total{endpoint, status}` is a new counter on the existing
   private registry.** `Collector.RecordProbe(endpoint, statusCode)` mirrors
   `ObserveRequest`'s shape, registering the counter centrally in
   `NewCollector`. The `endpoint` label is the probe path (`/livez`/`/readyz`/
   `/startupz`) and `status` is the status-class vocabulary (`2xx`/`5xx`),
   deliberately its own label name, not `status_class`, since a probe has no
   method or backend. Cardinality is bounded (3 endpoints × ~2 classes). Every
   probe hit records its response, so the counter reflects real probe activity.

9. **`Config` gains a `health_endpoint: { listen }` block**, mirroring the
   existing `metrics: { listen }` block and its nil-means-omitted pointer
   pattern. `listen` is optional, defaults to `:8081`
   (`DefaultHealthEndpointListen`), and `Validate` rejects an explicitly-set
   value that is empty or not a `host:port`, with `KnownFields(true)` still
   rejecting nested typos. Always-on: no disable toggle, matching the
   `health:`/`circuit:`/`metrics:` precedent. No other tunables.

10. **New API on the active health checker: `HealthChecker.ProbeRoundComplete()
    bool`.** It reports whether every configured backend has answered at least
    one active probe, is `false` until the first full sweep finishes, and latches
    `true` for the checker's lifetime. Implementation note: the *reported* latch
    is a single `atomic.Bool`; a shared `atomic.Int32` counter detects when the
    sweep is complete (a single bool cannot know "every backend has been
    probed"), and the bool is stored once when that counter reaches the backend
    count. The one-shot semantic belongs to the bool — the detection counter is
    an implementation detail.

Probes are not counted as client traffic: because decision 1 makes the health
server a distinct listener, probe requests never touch `lb_requests_total` or
the latency histogram. `lb_health_probe_total` (decision 8) is the sole record
of probe activity in Prometheus.

### Orchestrator mapping (explicit)

| Orchestrator | Probe mechanism | Path |
|---|---|---|
| Docker | `HEALTHCHECK` (via the `probe` subcommand, S3.T10) | `/livez` |
| Fly.io | HTTP check | `/readyz` |
| Kubernetes | `livenessProbe` | `/livez` |
| Kubernetes | `readinessProbe` | `/readyz` |
| Kubernetes | `startupProbe` | `/startupz` |

## Consequences

- Positive: the process now answers orchestrator-native liveness/readiness/
  startup questions, satisfying the Sprint 3 exit criterion, and does so on a
  listener that leaves the request path and its metrics untouched.
- Positive: `/readyz`'s live selectable-set check lets an upstream LB or K8s
  Service route around an instance during a full-cluster outage.
- Positive: `config_loaded` and `initial_probe_complete` are existing facts, so
  the endpoint adds no new detection path — it only exposes signals the health
  subsystem already computes.
- Negative: a third listener is a third port a deployment must expose and a
  third `http.Server` to start and shut down; the Sprint 4 `Run(ctx, cfg)` seam
  is the intended convergence point.
- Negative: `config_loaded` is true by construction in production (the process
  exits before serving otherwise), so the field is only ever observed `false` in
  tests and in the API contract. It is kept because the envelope's purpose is to
  be legible to an operator, not to encode the current implementation's exit
  paths.
- Neutral: `lb_health_probe_total` amends the metric-name reservation in
  `internal/metrics/doc.go` and `docs/design/sprint-1-contracts.md` (same
  pattern as `lb_active_connections` in ADR-0013 decision 7).

## Alternatives considered

- **Serve the probe paths on the proxy listener:** rejected in decision 1 —
  conflates probe traffic with client traffic, pollutes `lb_requests_total`, and
  gives the orchestrator no way to reach a "not ready but listening" instance
  without the proxy in the way.
- **A watchdog-based `/livez` (request-loop-progress or similar):** rejected in
  decision 3 — a probe timeout already signals a non-responding handler, and
  there is nothing to tune a watchdog against.
- **A single `/healthz` path:** rejected in decision 2 — orchestrators
  distinguish liveness from readiness from startup, and collapsing them loses
  the exact semantics K8s and Fly.io consume.
- **Flat JSON (`{"config_loaded": …, "ready": …}`) instead of a `checks`
  envelope:** rejected in decision 7 — the pinned `status`+`checks` shape keeps
  the top-level status machine-readable and the per-check detail namespaced, and
  matches the shape the spec fixed.
- **A raw status-code `status` label on the probe counter:** rejected in
  decision 8 — unbounded cardinality, the same reason `lb_requests_total` uses
  `status_class` (ADR-0013 decision 3).
- **`/readyz` gating only on startup conditions:** rejected in decision 6 — a
  fully-started instance whose entire fleet has been evicted must report not
  ready so an upstream can route around it.
