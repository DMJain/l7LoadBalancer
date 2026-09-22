# Architecture

_This document describes the as-built architecture, sprint by sprint. See
`MILESTONES.md` for the plan, `PROGRESS.md` for live task state,
`docs/sprint-3-retro.md` for the Sprint 3 close-out, and
`docs/design/sprint-1-contracts.md` for the frozen Sprint 1 contracts.
Sprints 1–3 are complete. This document is the Sprint 1–3 reference._

## Overview

l7LoadBalancer is a Layer 7 HTTP load balancer built entirely on Go's
standard library (`net/http`, `net/http/httputil`) for the request path. It
layers a pluggable backend-selection policy (`balancer.Selector`) over
`httputil.ReverseProxy`:

- the **proxy** owns the request lifecycle and active-connection accounting,
- the **selector** owns "which backend",
- the **registry** owns backend identity and shared mutable state,
- **config** owns strict YAML loading and validation.

`httputil.ReverseProxy` is used rather than a hand-rolled proxy because it
already handles hop-by-hop header stripping, `X-Forwarded-For`, buffering,
and flushing. The interesting work is the selection layer and the
concurrency/resilience/lifecycle code wrapped around it. Resilience (health
checking, circuit breaking) and observability (Prometheus metrics) arrive in
Sprint 3; zero-downtime reload and connection-lifecycle hardening arrive in
Sprint 4. All non-trivial decisions are tracked as ADRs — see the
[decision index](#decision-index).

Scope claims are deliberately bounded; "production-grade" here means the
patterns are demonstrated and defensible, not that the project is hardened
for adversarial traffic. See [ADR-0005](adr/0005-scope-of-production-grade.md).

## Request path

The following is the request lifecycle as built through Sprint 2. It is the
same path for every algorithm; only the `selector.Select` call differs.

```
Client
  │
  ▼
http.Server  (listen address from cfg.Listen)
  │
  ▼
proxy.Proxy.ServeHTTP                              internal/proxy/proxy.go
  │
  ├─ selector.Select(ctx, r) ──────────────► balancer.RoundRobin
  │                                            or balancer.LeastConnections
  │                                               │ snapshots
  │                                               ▼
  │                                          backend.Registry.Selectable()
  │                                          (healthy AND circuit-not-open)
  │
  ├─ ErrNoHealthyBackends ─────────────────► 503 short-circuit
  │                                            (ReverseProxy never runs)
  ├─ other select error ───────────────────► 502
  │
  ├─ Registry.Allow(b) ────────────────────► 503 short-circuit when the
  │   (circuit gate: circuit.Breaker)         circuit denies (ADR-0012);
  │                                            no dispatch, no IncActive
  │
  ├─ backend.IncActive()
  ├─ attach reqState{backend, status, once, dispatchStart} to request context
  ▼
httputil.ReverseProxy.ServeHTTP
  │
  ├─ Director            reads state from context; sets req.URL.Scheme/Host
  │                      (scheme/host only — see ADR-0007); stamps
  │                      state.dispatchStart — the backend round-trip
  │                      window, distinct from latency_ms (ADR-0010)
  ├─ Transport           stdlib default; dispatches to the backend
  │
  ├─ ModifyResponse      records resp.StatusCode; fans
  │                      ObserveRoundTrip(since dispatchStart,
  │                      success = status < 500) out to every registered
  │                      RoundTripObserver (ADR-0011 decision 9);
  │                      wraps resp.Body in releaseBody
  │                        └─ releaseBody.Close()
  │                             └─ reqState.release()   (sync.Once)
  │                                  └─ backend.DecActive()
  │
  ├─ ErrorHandler        records 502; fans ObserveRoundTrip(false, 2s
  │                      failure penalty) out to every registered
  │                      RoundTripObserver; calls reqState.release(),
  │                      logs the transport error at WARN, writes 502
  │
  └─ deferred logRequest emits one "request complete" slog line
                         (canonical fields) on every path
  ▼
Client
```

Lifecycle notes (full rationale in
[ADR-0007](adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md)):

1. `ServeHTTP` calls `selector.Select` itself, **not** `Director` — `Director`
   has no `ResponseWriter` and cannot short-circuit a 503. On success it calls
   `IncActive()` and carries a per-request `reqState` in the request context.
2. `DecActive` runs through `reqState.release()`, guarded by `sync.Once`, so it
   fires **exactly once** whether the response body's `Close()` or
   `ErrorHandler` reaches it first. `LeastConnections` depends on this
   invariant: a leak or double-decrement corrupts its view of load.
3. The decrement lives on the response-body wrapper, not in `ModifyResponse`
   directly — a response may be streamed, and "request done" must mean the
   client has finished consuming (or abandoned) the body.
4. Status is captured from `resp.StatusCode` rather than wrapping the
   `ResponseWriter`, preserving `ReverseProxy`'s `ResponseController` flushing
   and hijacking behavior.
5. Every request emits exactly one `msg:"request complete"` line with the
   canonical field vocabulary (`backend`, `method`, `status`, `latency_ms`,
   `remote_addr`, `path`) on the success, 503, 502, and abort paths. The 502
   path additionally logs the transport cause at WARN (the vocabulary has no
   `err` field).
6. Both terminal hooks (`ModifyResponse`, `ErrorHandler`) fan every round
   trip's outcome out to every registered `RoundTripObserver`, unconditionally
   with respect to observer and circuit state — success and failure alike,
   including a half-open circuit trial's result. Recording is unconditional;
   gating is conditional: a request denied by the breaker's `Allow()` gate is
   answered before dispatch, so it never produces a round trip to record.
   Registration is additive (`Proxy.RegisterObserver`), so `New(reg, sel)`'s
   signature stays frozen; latency recording is the first observer
   (`NewLatencyObserver`), passive outlier detection the second
   (`health.NewOutlierDetector`), and the circuit breaker the third
   (`circuit.Breaker`, also the registry's `CircuitGate`). See
   [ADR-0011](adr/0011-health-passive-outlier-and-circuit-breaker-composition.md)
   decision 9 and
   [ADR-0012](adr/0012-circuit-gate-interface-and-backend-state-methods.md).

## Component map

Package dependency graph (acyclic — no cycles allowed):

```
cmd/l7LoadBalancer
      │
      ▼
   internal/proxy ──────┐
      │                 │
      ▼                 ▼
internal/balancer → internal/backend
      │                 │
      └───────┬─────────┘
              ▼
        internal/config

internal/logger   — leaf, no internal deps
internal/metrics  — leaf, no internal deps (Sprint 3)
internal/health   — depends on backend (Sprint 3)
internal/circuit  — depends on backend (Sprint 3)
```

As-built package status:

| Package | Status | Responsibility |
|---|---|---|
| `cmd/l7LoadBalancer` | Sprint 1, extended Sprint 3 | Thin wiring layer: `config.Load`/`Validate` → `backend.NewRegistry` → `balancer.NewFromConfig` → `proxy.New` → observers (`health.NewOutlierDetector`, `circuit.New`) → `health.New`/`Start` → three `http.Server`s — client traffic, `/metrics` (`metrics.listen`, ADR-0013 decision 8), and the health endpoint (`health_endpoint.listen`, ADR-0014 decision 1). Seeds every backend's gauges after `NewRegistry`, registers the `probe` subcommand and the `version`/`commit` startup line, and shares one `signal.NotifyContext` across the checker goroutines and all three servers for SIGINT/SIGTERM graceful shutdown. Fatal + exit 1 on any startup failure (no silent fallback). |
| `internal/proxy` | Sprint 1 | Wraps `httputil.ReverseProxy`; owns the request lifecycle, 503/502 short-circuits, active-connection accounting, the per-request log line, and the optional whole-request metrics push. |
| `internal/balancer` | Sprint 1–2 | `Selector` interface + `ErrNoHealthyBackends` (the only exported sentinel), `RoundRobin`, `LeastConnections`, `ConsistentHashBoundedLoads` over an unexported ring, and `PowerOfTwoChoicesEWMA` over per-backend EWMA latency, plus the `NewFromConfig` factory. |
| `internal/backend` | Sprint 1–3 | `Backend` (identity + unexported `atomic` health/active/EWMA-latency state and a CAS-guarded circuit snapshot, methods-only access) and `Registry` (ordered, immutable until Sprint 4's hot-reload; `Selectable()` plus the `CircuitGate`/`Allow` admission seam). |
| `internal/config` | Sprint 1, extended Sprint 3 | Strict YAML loading (`KnownFields(true)`) and fail-fast validation; algorithm identifier constants. Sprint 3 adds optional global `health:` (probe interval/timeout) and `circuit:` (cooldown) duration sections, defaulted in `Validate` and rejected when explicitly non-positive (ADR-0011 decision 10), plus always-on `metrics:` (default `:9090`, ADR-0013 decision 8) and `health_endpoint:` (default `:8081`, ADR-0014 decision 9) listen blocks. Immutable after init in Sprint 1. |
| `internal/logger` | Sprint 1, extended Sprint 3 | `log/slog` JSON setup and the frozen canonical field vocabulary (the six request-scoped fields plus the transition-scoped `event`/`reason` closed vocabularies, ADR-0013 decision 12). Leaf. |
| `internal/metrics` | Sprint 3 | The `Collector` over a private `prometheus.Registry`: request counter and whole-request latency histogram (`backend`/`method`/`status_class`), backend-healthy and active-connections gauges, a `lb_circuit_state` label-enum gauge whose setter unconditionally zeroes the non-target states, and the `lb_health_probe_total{endpoint,status}` probe counter. Push-only and leaf — it imports no other internal package (ADR-0013, ADR-0014). |
| `internal/health` | Sprint 3 | Active health checking: `Checker` owns one probe goroutine per backend (started by `main` on the shared `sigCtx`), probes each backend's configured URL with a dedicated `http.Client` (its own timeout, no redirect following), drives `Backend.MarkHealthy`/`MarkUnhealthy` through an N-consecutive-failure / M-consecutive-success state machine whose thresholds are Go constants, and exposes `ProbeRoundComplete()` (ADR-0011 decisions 2, 10, 11, 13; ADR-0014 decision 10). Passive outlier detection: `OutlierDetector` implements `proxy.RoundTripObserver` (structurally, without importing `proxy`), keeps a count-based sliding window of recent outcomes per backend, and ejects via `MarkUnhealthy` after N failures within the window — recovering only when a later active probe is observed to have reinstated the backend (ADR-0011 decisions 3, 8, 9, 12). Health endpoint: `NewHandler` serves `/livez`, `/readyz`, `/startupz` (ADR-0014). |
| `internal/circuit` | Sprint 3 | `Breaker`: the circuit policy (consecutive-failure-to-open constant, config cooldown). Drives `Backend`'s circuit-state methods, implements `backend.CircuitGate` and `proxy.RoundTripObserver` structurally, and is installed by `main` as both the registry gate and an observer. Logs and writes `lb_circuit_state` from the shared `CircuitTransition` (ADR-0013 decision 10). |

**Dependency rule**: `internal/backend` does **not** import `internal/balancer`.
A `Backend` has no notion of how it is selected; adding that import is a sign
the abstraction is leaking.

The full concurrency-ownership table (which field is written by whom, under
which sync primitive) lives in
[`docs/design/sprint-1-contracts.md`](design/sprint-1-contracts.md#concurrency-ownership-table).

### Where new selection algorithms plug in

- The seam is `balancer.Selector` (`internal/balancer/selector.go`):
  `Select(ctx context.Context, r *http.Request) (*backend.Backend, error)`.
  It is defined in `balancer`, not `proxy`, because all implementations live
  there — see [ADR-0002](adr/0002-interface-placement-and-schema-freeze.md).
- `balancer.NewFromConfig(cfg, reg)` maps a config algorithm string to a
  selector type; `main.go` only calls it.
- The config-string → constant → selector-type mapping is the **algorithm
  identifier table** in
  [`docs/design/sprint-1-contracts.md`](design/sprint-1-contracts.md#algorithm-identifier-table).
  `config.Validate` accepts only the implemented set (all four Sprint
  1–2 identifiers: `round_robin`, `least_conn`, `consistent_hash`,
  `p2c_ewma`) — see
  [ADR-0004](adr/0004-reject-unimplemented-algorithms-in-validate.md).
- Every selector carries a compile-time assertion
  `var _ Selector = (*X)(nil)`.

The consistent-hash selectors (Sprint 2) share an unexported `ring`
primitive in `internal/balancer`: a placement-only, immutable mapping from a
hash key to a backend via 150 virtual nodes per backend, exposed as an
ordered `iter.Seq[*backend.Backend]` candidate walk that each selector
filters with its own inline condition. Its hash pipeline, vnode key format,
and vnode count are recorded in
[ADR-0008](adr/0008-consistent-hash-ring-pipeline-and-vnode-layout.md).

`consistent_hash` maps to `ConsistentHashBoundedLoads`: the walk from the
client-IP hash key admits the first candidate that is both healthy and within
`max(1, ceil(avg_active * 1.25))` (`avg_active` over healthy backends), so a
hot key is rehashed past a backend that has reached its share of the load.
The deliberately unwired `naiveConsistentHash` comparator exists only to
prove that property in the checked-in hot-key test; ε, the load metric,
capacity formula, and the evidence are recorded in
[ADR-0009](adr/0009-consistent-hash-bounded-loads-capacity-and-evidence.md).

`p2c_ewma` maps to `PowerOfTwoChoicesEWMA`: it snapshots the healthy set,
draws two distinct backends, and returns the one with the lower
`Backend.EWMALatency()`. The proxy records that latency on every request's
backend round trip — `director()` to `modifyResponse` — unconditionally and
regardless of the configured selector, with failures recording a fixed 2s
penalty. The latency state, cold-start rule, and penalty are recorded in
[ADR-0010](adr/0010-p2c-ewma-backend-latency-state-cold-start-and-failure-penalty.md).

### Active health checking (Sprint 3)

`health.New(reg, interval, timeout)` builds a `Checker`; `main` calls
`Checker.Start(sigCtx)`, which launches one goroutine per backend. Each
goroutine owns a ticker and a per-backend prober holding consecutive-success /
consecutive-failure counters; the probe-cycle logic is a method
(`prober.probeOnce`) separate from the `for { select }` loop, so tests drive
cycles directly with no ticker. A probe is a plain GET to the backend's
already-configured URL — no separate health path — and only a 2xx response
counts as success: the dedicated client returns `http.ErrUseLastResponse` on
`CheckRedirect`, because `httputil.ReverseProxy` forwards redirects to clients
verbatim, so a redirecting backend is unusable even though it answered.
`probeFailuresBeforeUnhealthy` (3) consecutive failures call `MarkUnhealthy`;
`probeSuccessesBeforeHealthy` (2) consecutive successes call `MarkHealthy`
(the recovery path, by convention, for passive detection too). Both thresholds
are Go constants, not config. `internal/health` depends only on
`internal/backend` and shares no transport with `internal/proxy` — see
[ADR-0011](adr/0011-health-passive-outlier-and-circuit-breaker-composition.md)
decisions 2, 10, 11, and 13.

The reinstatement gate compares `successes >= probeSuccessesBeforeHealthy`
(not `==`), because passive outlier detection can eject a backend whose
accumulator has already crossed M — an `==` gate then never fires again and
the gauge stays pinned at `0` while `IsHealthy()` reads `true`. This was a
shipped-code drift fixed in S3.T6.5 and recorded as the
[ADR-0011 2026-09-22 amendment](adr/0011-health-passive-outlier-and-circuit-breaker-composition.md#amendment-2026-09-22-reinstatement-gate-uses--not-).

The checker's transitions are observable: an equality-gated
(`counter == threshold`) genuine-state guard emits exactly one
`event=health_ejected`/`reason=probe_failures` (WARN) or
`event=health_reinstated`/`reason=probe_recovered` (INFO) `slog` line and
writes `lb_backend_healthy` to `0`/`1` from the same signal, so the log line
and the gauge cannot disagree (ADR-0013 decisions 11 and 15). `Checker`
also exposes `ProbeRoundComplete() bool` — a one-shot `atomic.Bool` latch
flipped after the first sweep in which every configured backend has answered
at least one probe — which the health endpoint's startup/readiness gates
consume (ADR-0014 decision 10).

### Passive outlier detection (Sprint 3)

`health.NewOutlierDetector(log, collector)` builds an `OutlierDetector` that
implements `proxy.RoundTripObserver` structurally — `internal/health` never
imports `internal/proxy`; the interface is satisfied by method shape. It
keeps a count-based (not time-based) sliding window of the last
`outlierWindowSize` (10) round-trip outcomes per backend under a single
mutex, and ejects via `MarkUnhealthy()` when `outlierFailuresBeforeEject`
(5) failures fall within the window — both unexported Go constants. The
failure signal is the union of 5xx responses and `errorHandler` transport
failures, and the two mix freely in one window. Ejection fires exactly once
per episode (an `ejected` guard), emitting one
`event=health_ejected`/`reason=outlier_window` (WARN) line and one
`lb_backend_healthy=0` write from that same guard.

Recovery has no timer of its own: the detector never calls `MarkHealthy`.
An ejected backend observed healthy again (by the next successful active
probe) resets the episode, keeping the state machine single-path — one way
in via either subsystem, one way out via active checks only. `main`
registers the detector as a second `RoundTripObserver` alongside
`NewLatencyObserver` and the circuit breaker. See
[ADR-0011](adr/0011-health-passive-outlier-and-circuit-breaker-composition.md)
decisions 3, 8, 9, and 12, and
[ADR-0013](adr/0013-observability-metrics-logging-and-integration.md)
decision 15.

### Circuit breaker (Sprint 3)

`circuit.New(cooldown, log, collector)` builds a `Breaker` holding only
policy: the unexported `circuitFailuresBeforeOpen` (3) constant and the
configured cooldown. Circuit *state* — the closed/open/half-open enum,
consecutive-failure count, half-open trial flag, and opened-at timestamp —
lives on `Backend` as one immutable `atomic.Pointer[circuitSnapshot]`
replaced by `CompareAndSwap`, so every compound transition is a single
atomic step and `internal/backend` needs no import from `internal/circuit`
(ADR-0012 decisions 3–4).

`Breaker` plays two roles, both wired in `main`: it is the registry's
`CircuitGate` (ADR-0012 decision 1) and a registered `RoundTripObserver`.
`Registry.Selectable()` filters on `IsHealthy() && !gate.Open(b)`, and
`ServeHTTP` calls `Registry.Allow(b)` after `Select()` and before
`IncActive()`; a denial is a 503 with a distinct WARN line and no
active-connection accounting (ADR-0011 decision 7). A half-open backend stays
`Selectable()` — only `Allow`'s CAS on the trial flag admits the single
in-flight trial. Outcomes while a circuit is `Open` are ignored, so a stale
in-flight response cannot bypass the cooldown; a single half-open success
closes the circuit and a single failure reopens it, with no inner threshold.
The consecutive-failure counter is reset by any success, distinct from passive
detection's sliding window (ADR-0011 decision 8). See ADR-0011 and ADR-0012.

Every genuine transition is reported once by `Backend.CircuitFailure`,
`CircuitSuccess`, and `CircuitAllow`, which return a
`backend.CircuitTransition` (`NoChange`/`Opened`/`Reopened`/`Closed`/
`HalfOpened`). `circuit.Breaker` logs and writes `lb_circuit_state` from
that same value at its `ObserveRoundTrip`/`Allow` call sites — a sustained
failure run while already `Open` produces `NoChange` and neither logs nor
re-writes — so the line and the gauge cannot disagree
([ADR-0013](adr/0013-observability-metrics-logging-and-integration.md)
decisions 10, 15, and its 2026-09-21 amendment). A promotion whose CAS is
won by the `Registry.Selectable()` read path is never logged and leaves the
gauge at `open`: the accepted, permanent gap ADR-0013 decision 13 records.

### Observability pipeline (Sprint 3)

`internal/metrics` is a leaf-only, push-only `Collector` over its own private
`prometheus.NewRegistry()` (never the default registerer), so tests build
independent instances and run `t.Parallel()`. It holds the whole-request
counter and histogram (`backend`/`method`/`status_class`, never a raw
`status_code`; provisional `doc.go` buckets), the `lb_backend_healthy` and
`lb_active_connections` gauges, the label-enum `lb_circuit_state` gauge whose
setter unconditionally zeroes the two non-target states, and the
health-endpoint counter `lb_health_probe_total{endpoint,status}`. `main`
serves `/metrics` via `promhttp.HandlerFor` on a dedicated `http.Server` on
`metrics.listen` (default `:9090`, always-on), sharing `sigCtx`.

Traffic and state push into the collector rather than it reading anything:
the whole-request hook in `proxy.ServeHTTP` counts and times every exit path
(no-healthy 503 with `backend=""`, circuit-denied 503 and `ErrorHandler` 502
with the real backend label); the transition subsystems write their gauges
at the exact edge-triggered sites their log lines fire from; and `main` seeds
every backend's series (`healthy=1`, `circuit_state=closed`,
`active_connections=0`) immediately after `backend.NewRegistry` succeeds, so
a never-trafficked system renders a complete dashboard on first scrape. The
two new canonical log fields, `event` and `reason`, are closed snake_case
Go-constant vocabularies in `internal/logger`. `internal/balancer` is
untouched and imports nothing new. See
[ADR-0013](adr/0013-observability-metrics-logging-and-integration.md)
decisions 1–16, and
[`docs/sprint-3-retro.md`](sprint-3-retro.md).

### Health endpoint (Sprint 3)

A third always-on `http.Server` on `health_endpoint.listen` (default
`:8081`) serves orchestrator-native probes, mirroring `metricsSrv`: started
in `main` on the shared `sigCtx` and joined to the graceful-shutdown
sequence. `health.NewHandler(checker, reg, configLoaded, collector)` returns
a `ServeMux` with three paths, all structured JSON and all recorded via
`lb_health_probe_total`:

- `/livez` — unconditional 200 `{"status":"alive"}`; a deadlock shows up as
  a probe timeout, not a special response.
- `/readyz` — 200 only while `config_loaded` ∧
  `Checker.ProbeRoundComplete()` ∧ live `Registry.Selectable() >= 1`; else
  503 with the failing check visible. Served live per request, so a
  full-fleet eviction reports not-ready and an upstream LB or K8s Service
  can route around the instance.
- `/startupz` — 503 until the first two hold, then permanently 200
  (one-way; it does not gate on the selectable set).

Because the endpoint is a distinct listener, probes never enter the proxy
path and never touch `lb_requests_total` or the latency histogram. The
orchestrator mapping: Docker `HEALTHCHECK` → `/livez`, Fly.io HTTP check →
`/readyz`, Kubernetes → all three. See
[ADR-0014](adr/0014-health-endpoint-contract-and-probe-semantics.md).

### Container demo stack (Sprint 3)

The demo stack is additive and does not settle the deployment target
([ADR-0005](adr/0005-scope-of-production-grade.md) and its 2026-09-22
amendment).

- **`Dockerfile`** — multi-stage: `go build` with `CGO_ENABLED=0` in a
  Debian Go image, the static binary copied into a digest-pinned
  `gcr.io/distroless/static-debian12:nonroot`. It exposes `8080`, `8081`,
  and `9090`, carries OCI labels, bakes `configs/docker.yaml` to
  `/etc/l7lb/config.yaml`, and self-probes via a native
  `HEALTHCHECK … CMD ["/l7lb", "probe", "http://127.0.0.1:8081/livez"]` —
  no shell or `curl` in the runtime image, which is why the `probe`
  subcommand exists ([ADR-0014](adr/0014-health-endpoint-contract-and-probe-semantics.md)
  decision 2). A strict `.dockerignore` allowlist keeps the build context
  minimal.
- **Repo-root `docker-compose.yml`** — one command brings up the LB image,
  the three dummy backends, Prometheus, and Grafana. It is additive: the two
  composes under `deployments/docker/` (backends-only for the host-process
  chaos flow; observability-only) remain canonical and untouched, and the
  chaos scripts keep targeting the host-process LB. Scrape config lives in a
  separate `prometheus-stack.yml` (`l7lb:9090`); Grafana provisioning is
  bind-mounted from the existing observability tree so there is one source
  of truth. Only `8080`, `8081`, and `3000` are published; the dependency
  chain is mixed (backends started, LB healthy via its own `HEALTHCHECK`,
  Prometheus waiting on `service_healthy`, Grafana on `service_started`).
  See
  [ADR-0013 decision 18](adr/0013-observability-metrics-logging-and-integration.md#repo-root-demo-stack-docker-composeyml).

## Decision index

All non-trivial decisions are recorded in `docs/adr/`. Accepted:

| ADR | Title | Status |
|-----|-------|--------|
| [0001](adr/0001-record-adrs.md) | Record architecture decisions | Accepted |
| [0002](adr/0002-interface-placement-and-schema-freeze.md) | Interface placement and Sprint 1 schema freeze | Accepted |
| [0003](adr/0003-pin-unused-deps-with-tools-go.md) | Pin not-yet-imported dependencies with a build-tagged tools.go | Accepted |
| [0004](adr/0004-reject-unimplemented-algorithms-in-validate.md) | Reject unimplemented algorithms in Validate | Accepted |
| [0005](adr/0005-scope-of-production-grade.md) | Scope of "production-grade" | Accepted |
| [0006](adr/0006-backend-sethealthy-amends-adr-0002.md) | Add Backend.SetHealthy, amending ADR-0002 decision 5 | Accepted |
| [0007](adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md) | Proxy request lifecycle and exactly-once active-connection decrement | Accepted |
| [0008](adr/0008-consistent-hash-ring-pipeline-and-vnode-layout.md) | Consistent-hash ring hash pipeline, vnode key order, and vnode count | Accepted |
| [0009](adr/0009-consistent-hash-bounded-loads-capacity-and-evidence.md) | Consistent-hash bounded loads: epsilon, load metric, capacity formula, and hot-key evidence | Accepted |
| [0010](adr/0010-p2c-ewma-backend-latency-state-cold-start-and-failure-penalty.md) | P2C-EWMA: Backend-owned latency state, cold-start semantics, and the failure penalty | Accepted |
| [0011](adr/0011-health-passive-outlier-and-circuit-breaker-composition.md) | Health, passive-outlier, and circuit-breaker composition | Accepted |
| [0012](adr/0012-circuit-gate-interface-and-backend-state-methods.md) | Circuit breaker gate interface, Registry-mediated admission, and Backend state API | Accepted |
| [0013](adr/0013-observability-metrics-logging-and-integration.md) | Observability: metrics collector, transition logging, and their integration | Accepted |
| [0014](adr/0014-health-endpoint-contract-and-probe-semantics.md) | Health endpoint contract and probe semantics | Accepted |

Tracked but not yet written (each decides in the sprint that delivers the
feature):

| Sprint | Decision |
|--------|----------|
| 4 | Reload architecture: atomic pointer swap vs SO_REUSEPORT |
| 4 | Deployment target decision (deferred from Sprint 1 per ADR-0005) |
| 4 | Retry policy (or deliberate absence) |

## Deviations from plan

Six items differ between the plan and the as-built state — two from
Sprint 1, four from Sprint 2 (recorded by S2.T8, the sprint's ad-hoc
closing task). None required a new ADR, and the reasoning for that is
recorded with each.

### 1. S1.T7 removed the scaffold's `-addr` CLI flag — owner signed off

The initial scaffold exposed a `-addr` flag for the listen address. S1.T7
removed it: `cfg.Listen`, part of the frozen YAML schema and checked by
`config.Validate`, is the single validated source of truth, so a second,
unvalidated address source was removed rather than reconciled.

This was flagged during the S1.T7 code review as needing explicit owner
sign-off (`docs/sessions/2026-09-18-opencode.md`, "S1.T7 — Code-review
follow-up" → *Flagged for owner awareness*). It is recorded here as
**signed off by the project owner during the S1.T10 retro**. No ADR: the
change was evaluated against the project's three-part ADR bar (hard to
reverse / surprising without context / real trade-off) and, while it is
arguably surprising and is a real trade-off, it is trivially reversible — a
contained one-flag change with no ripples — so it fails the bar on the
reversibility leg. Original implementation reasoning:
`docs/sessions/2026-09-18-opencode.md` (S1.T7 → Decisions).

### 2. `MILESTONES.md`'s "custom `Transport` pattern" wording — pre-implementation scope clarification

`MILESTONES.md:10` lists, as a Sprint 1 deliverable, an
"`httputil.ReverseProxy` wrapper using `Director` + custom `Transport`
pattern". No custom `http.Transport`/`RoundTripper` was ever built, or
intended to be built, in Sprint 1. The Transport work was reassigned to
Sprint 4 **before** S1.T6 was implemented:

- `.scratch/s1-t3-t9-backend-selector-proxy/spec.md:115` states, for the
  proxy phase: "No `http.Transport` tuning (`MaxIdleConnsPerHost`, timeouts,
  etc.) in this phase — stock defaults; connection pool tuning is explicitly
  Sprint 4 per AGENTS.md." See also `:171`.
- `AGENTS.md:291` and `MILESTONES.md:63` both place connection-pool tuning
  (`MaxIdleConnsPerHost`, `IdleConnTimeout`, `DialContext` timeout,
  `ResponseHeaderTimeout`) in Sprint 4.
- The frozen contracts doc never specifies a custom Transport for Sprint 1;
  its proxy contract (`Director` sets only scheme/host) is consistent with
  the stock transport.
- "RoundTripper" appears nowhere in the project's source or git history prior
  to this task.

So this is a **pre-implementation scope clarification**, not a
mid-implementation deviation: `MILESTONES.md`'s original scaffold wording
(unchanged since the initial commit) simply was never edited to match the
scope the frozen contract had already settled. No ADR — it is a wording
mismatch in a planning document, not a design decision.

### 3. Two planned Sprint 2 ADR topics became three per-task ADRs — scoped ahead of implementation

`MILESTONES.md`'s Sprint 2 deliverables name two ADR topics
(bounded-loads-over-naive; P2C-over-least-connections under skew). As built
there are three ADRs: the bounded-loads topic split into
[ADR-0008](adr/0008-consistent-hash-ring-pipeline-and-vnode-layout.md)
(ring pipeline, vnode key order, vnode count — closing with S2.T1.1) and
[ADR-0009](adr/0009-consistent-hash-bounded-loads-capacity-and-evidence.md)
(ε, load metric, capacity formula, hot-key evidence — closing with S2.T2),
and [ADR-0010](adr/0010-p2c-ewma-backend-latency-state-cold-start-and-failure-penalty.md)
closes S2.T3. The split was decided before any Sprint 2 code, in the
S2.T1/T2 spec (`.scratch/s2-t1-t2-consistent-hash-bounded-loads/spec.md`,
"Two ADRs, not one"), following the per-task-ADR pattern ADR-0006/0007
established so no task closes with an open decision waiting on another.
No ADR for the split itself: a documentation-organization choice,
trivially reversible.

### 4. S2.T1 split into S2.T1.1 / S2.T1.2 — ticket-level tracking

The Sprint 2 spec groups the work as S2.T1 (ring primitive + unexported
`naiveConsistentHash` comparator) and S2.T2 (bounded-loads selector +
config wiring), but `PROGRESS.md` tracks the finer-grained `.scratch`
tickets (T1.1 = ring, T1.2 = comparator) so each closes independently.
Documented in `PROGRESS.md`'s Sprint 2 intro at split time. No ADR: task
granularity, not design.

### 5. The MILESTONES evidence/ADR bullets were folded into S2.T2/S2.T3 — owner-ratified after the fact

`MILESTONES.md`'s Sprint 2 deliverables list the two distribution-property
tests and the two ADRs as separate bullets, which the project owner
tracked mentally as "S2.T4–T7". Those IDs never existed in the repo: the
hot-key comparative test shipped inside S2.T2, the load-skew convergence
test inside S2.T3, and each ADR closed with its implementation task, per
the specs' folding. The owner ratified the folded deliverables on
2026-09-20 after a code-level review of the tests, selector
implementations, and ADRs against their claims — all verified correct,
with live runs reproducing the published evidence. No ADR: a
tracking-shape choice plus verification, not a design decision.

### 6. Sprint 2 planned no retro task; S2.T8 was added ad hoc — this entry is the record of why

Sprint 1 closed with S1.T10 (retro / architecture doc) as a scoped task;
Sprint 2's plan contained no equivalent, and the sprint was declared
complete by S2.T3's session without one. S2.T8 closes the gap for
consistency: the header and diagram updates above, this deviations audit,
an ADR sweep (result stated explicitly in the session log), and the closing
session log. It appears in `PROGRESS.md` without a `MILESTONES.md` bullet
by design. No ADR: process, not design.

## Later amendments to Sprint 1 contracts

`docs/design/sprint-1-contracts.md` is **frozen** and is intentionally not
edited. Four ADRs accepted after the freeze amend rows of its
concurrency-ownership table:

- [ADR-0006](adr/0006-backend-sethealthy-amends-adr-0002.md) amended the
  `Backend.healthy` row by adding `SetHealthy(bool)` alongside `IsHealthy()`,
  the permanent contract the Sprint 3 health checker would call. The Sprint 3
  health checker is the caller.
- [ADR-0007](adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md)
  amends the `Backend.active` row / proxy lifecycle: `DecActive` is driven
  through a `sync.Once`-guarded `reqState.release()` so it runs exactly once
  across the body-wrapper and `ErrorHandler` paths.
- [ADR-0011](adr/0011-health-passive-outlier-and-circuit-breaker-composition.md)
  amends two rows. Decision 2 splits `SetHealthy(bool)` into
  `MarkHealthy()`/`MarkUnhealthy()` over the same `atomic.Bool`, making the
  active-only-recovery asymmetry legible at the call site. Decision 1 renames
  `Registry.Healthy()` to `Registry.Selectable()` (eligible = healthy AND
  circuit-not-open) when circuit state first exists, in Sprint 3's
  circuit-breaker task. **Landed in S3.T3.**
- [ADR-0012](adr/0012-circuit-gate-interface-and-backend-state-methods.md)
  adds the mechanism decision 1 needed: the `CircuitGate` interface and
  `Registry.SetCircuitGate`/`Allow` admission path, plus the CAS-guarded
  `Backend` circuit-state method API. The Sprint 1 contracts doc's circuit row
  ("likely mutex; decide in the Sprint 3 ADR") is superseded by ADR-0011
  decision 6 and ADR-0012 decision 3 — an immutable-snapshot
  `CompareAndSwap`, not a mutex.

Read those four ADRs alongside the contracts doc's
[concurrency-ownership table](design/sprint-1-contracts.md#concurrency-ownership-table).

## Deliberately not here yet

A future agent should not assume any of the following exist. Each names its
owning sprint:

- **Hot-reload (SIGHUP, `atomic.Pointer[Config]`)** — Sprint 4.
- **Connection-lifecycle hardening** (client cancellation, backend death
  mid-response, slow-loris timeouts) — Sprint 4.
- **Connection-pool tuning** (`http.Transport` settings) — Sprint 4.
- **Retry policy** (or its deliberate absence) — Sprint 4.
- **Deployment-target decision** (bare binary vs. Docker vs. Kubernetes) —
  Sprint 4 (`MILESTONES.md:66`; [ADR-0005](adr/0005-scope-of-production-grade.md)
  frames the window as Sprint 4/5).
- **HTTP/2 (client-facing and to backends)** — Sprint 5.
- **The benchmark rig and published numbers** — Sprint 5.
