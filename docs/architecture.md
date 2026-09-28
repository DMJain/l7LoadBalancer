# Architecture

_This document describes the as-built architecture, sprint by sprint. See
`MILESTONES.md` for the plan, `PROGRESS.md` for live task state,
`docs/sprint-3-retro.md` and `docs/sprint-4-retro.md` for the sprint
close-outs, and `docs/design/sprint-1-contracts.md` for the frozen Sprint 1
contracts. Sprints 1–4 are complete. This document is the Sprint 1–4
reference._

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
checking, circuit breaking) and observability (Prometheus metrics) arrived in
Sprint 3; Sprint 4 adds the `internal/app` wiring seam, zero-downtime SIGHUP
reload with draining, connection-lifecycle hardening, backend transport
tuning, and config env interpolation. All non-trivial decisions are tracked as
ADRs — see the [decision index](#decision-index).

Scope claims are deliberately bounded; "production-grade" here means the
patterns are demonstrated and defensible, not that the project is hardened
for adversarial traffic. See [ADR-0005](adr/0005-scope-of-production-grade.md).

## Request path

The following is the request lifecycle as built through Sprint 4. It is the
same path for every algorithm; only the `selector.Select` call differs.

```
Client
  │
  ▼
http.Server  (listen address from cfg.Listen; ReadTimeout = server.read_timeout,
  │           S4.T7 — the slow-loris body bound; WriteTimeout deliberately unset)
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
  ├─ derive outbound ctx  context.WithCancelCause; context.AfterFunc joins the
  │                      backend's retired context, cancelling the outbound
  │                      request with its cause on drain (S4.T4.0, ADR-0016)
  ├─ attach reqState{backend, status, once, dispatchStart, clientCtx}
  ▼
httputil.ReverseProxy.ServeHTTP
  │
  ├─ Director            reads state from context; sets req.URL.Scheme/Host
  │                      (scheme/host only — see ADR-0007); stamps
  │                      state.dispatchStart — the backend round-trip
  │                      window, distinct from latency_ms (ADR-0010)
  ├─ Transport           the app-built tuned *http.Transport (S4.T8), replacing
  │                      the stdlib default; dispatches to the backend
  │
  ├─ ModifyResponse      records resp.StatusCode; fans
  │                      ObserveRoundTrip(since dispatchStart,
  │                      success = status < 500) out to every registered
  │                      RoundTripObserver (ADR-0011 decision 9) unless the
  │                      backend is removed (ADR-0015 decision 8);
  │                      wraps resp.Body in releaseBody
  │                        ├─ releaseBody.Read() counts bytes; a non-EOF read
  │                        │    error logs WARN backend_died_mid_response and
  │                        │    does not un-ring the recorded success (S4.T6)
  │                        └─ releaseBody.Close()
  │                             └─ reqState.release()   (sync.Once)
  │                                  └─ backend.DecActive()
  │
  ├─ ErrorHandler        three-tier failed-round-trip classification (S4.T5,
  │                      ADR-0017): drain cancellation → WARN window_expired,
  │                      no observer; client-gone (clientCtx.Err() != nil) →
  │                      INFO client_canceled, 499, no observer, no EWMA,
  │                      RearmTrial(); else genuine transport failure → WARN,
  │                      observers get the 2s penalty, 502. Calls
  │                      reqState.release() on every tier.
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
   `remote_addr`, `path`) on the success, 503, 502, 499, and abort paths. The
   502 and 499 paths additionally log a cause at WARN and INFO respectively
   (the vocabulary has no `err` field).
6. Both terminal hooks (`ModifyResponse`, `ErrorHandler`) fan every round
   trip's outcome out to every registered `RoundTripObserver`, unconditionally
   with respect to observer and circuit state — success and failure alike,
   including a half-open circuit trial's result. The one exception is a
   reload-removed backend: the whole fan-out is skipped for it (ADR-0015
   decision 8), so no observer learns a backend left the fleet. Recording is
   unconditional;
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
7. A failed round trip is classified into exactly one of three tiers, in
   order — drain cancellation, client-gone, genuine transport failure — and
   the client's own request context (`reqState.clientCtx`) is the
   discriminator. Client-gone feeds no observer, records no EWMA latency,
   releases its slot, and is recorded as 499 / `status_class="4xx"`, so
   client churn never reads as a backend failure; a response-header timeout
   still reaches the observers as a failure. A cancelled half-open trial is
   re-armed via `Backend.RearmTrial()` so a live backend cannot be wedged
   behind a taken trial. See
   [ADR-0017](adr/0017-client-gone-classification-and-trial-rearm.md).

## Component map

Package dependency graph (acyclic — no cycles allowed):

```
cmd/l7LoadBalancer → app, config, logger
internal/app       → proxy, health, circuit, metrics, balancer, backend, config, logger
internal/proxy     → backend, balancer, logger, metrics
internal/balancer  → backend, config
internal/health    → backend, logger, metrics
internal/circuit   → backend, logger, metrics
internal/backend   → config
internal/logger    — leaf, no internal deps
internal/metrics   — leaf, no internal deps
```

As-built package status:

| Package | Status | Responsibility |
|---|---|---|
| `cmd/l7LoadBalancer` | Sprint 1, extended Sprint 3–4 | Thin entry point: parse the `-config` flag, `config.Load`/`Validate`, hand the config to `app.Build`, `app.Run` on a `signal.NotifyContext` for SIGINT/SIGTERM. Owns the `probe` subcommand (`runProbe`) and the `version`/`commit` startup line, and installs SIGHUP on its own channel (separate from shutdown) driving `reloadLoop`. Fatal + exit 1 on any startup failure (no silent fallback). |
| `internal/app` | Sprint 4 | Owns the whole wiring graph. `Build` assembles the collector, registry + seeded gauges, circuit breaker (registry gate and observer), selector, proxy with its transport/metrics/observers, active checker, and the health handler, and starts the three `http.Server`s — client traffic, `/metrics` + pprof (ADR-0013 decision 8; S4.T9), and the health endpoint (ADR-0014 decision 1). Holds the loaded config as one `atomic.Pointer` (the reload baseline) and exposes `Reload`, which diffs, rejects non-backend changes, applies the registry snapshot swap, hooks up checker/outlier/metrics, and starts one drain goroutine per removed backend. `Run` serves until its context is cancelled and shuts all three servers down in order. Production, the chaos harness, and tests assemble the same graph through it (S4.T0, ADR-0015, ADR-0016). |
| `internal/proxy` | Sprint 1–4 | Wraps `httputil.ReverseProxy`; owns the request lifecycle, 503/502 short-circuits, active-connection accounting, the per-request log line, and Sprint 4's connection-lifecycle hardening: the three-tier failed-round-trip classification and 499 client-gone tier (ADR-0017), mid-body death reporting, and the per-backend retired-context join (ADR-0016). |
| `internal/balancer` | Sprint 1–2 | `Selector` interface + `ErrNoHealthyBackends` (the only exported sentinel), `RoundRobin`, `LeastConnections`, `ConsistentHashBoundedLoads` over an unexported ring, and `PowerOfTwoChoicesEWMA` over per-backend EWMA latency, plus the `NewFromConfig` factory. In Sprint 4 the consistent-hash selector rebuilds its ring on demand, keyed on the registry snapshot version (ADR-0015 decision 9). |
| `internal/backend` | Sprint 1–4 | `Backend` (identity + unexported `atomic` health/active/EWMA-latency state, a CAS-guarded circuit snapshot, a removed flag, and a retired context, methods-only access) and `Registry` (ordered; Sprint 4 replaces its backend set through an immutable versioned snapshot behind one atomic pointer, with `Selectable()` and the `CircuitGate`/`Allow` admission seam). |
| `internal/config` | Sprint 1, extended Sprint 3–4 | Strict YAML loading (`KnownFields(true)`) and fail-fast validation; algorithm identifier constants. Sprint 3 adds optional global `health:`/`circuit:` duration sections and always-on `metrics:`/`health_endpoint:` listen blocks. Sprint 4 adds `reload.drain_window` (ADR-0016 decision 1), `server.read_timeout` (S4.T7), the four-knob `transport:` section (S4.T8), `${VAR}` interpolation of backend URLs after decode (S4.T16), and the pure `DiffBackends`/`NonBackendChanges` reload comparison (ADR-0015 decisions 2–4). |
| `internal/logger` | Sprint 1 | `log/slog` JSON setup and the frozen canonical field vocabulary. Leaf. |
| `internal/metrics` | Sprint 3 | The `Collector` over a private `prometheus.Registry`: request counter and whole-request latency histogram (`backend`/`method`/`status_class`), backend-healthy and active-connections gauges, a `lb_circuit_state` label-enum gauge whose setter unconditionally zeroes the non-target states, and the `lb_health_probe_total{endpoint,status}` probe counter. Push-only and leaf — it imports no other internal package (ADR-0013, ADR-0014). Sprint 4 adds series deletion for removed backends and seeding for added ones. |
| `internal/health` | Sprint 3–4 | Active health checking: `Checker` owns one probe goroutine per backend (started by `app.Run` on the shared context), probes each backend's configured URL with a dedicated `http.Client` (its own timeout, no redirect following), drives `Backend.MarkHealthy`/`MarkUnhealthy` through an N-consecutive-failure / M-consecutive-success state machine whose thresholds are Go constants, and exposes `ProbeRoundComplete()` (ADR-0011 decisions 2, 10, 11, 13; ADR-0014 decision 10). Sprint 4 adds `Add`/`Remove` so reload starts and stops a backend's prober (ADR-0015 decision 10). Passive outlier detection: `OutlierDetector` implements `proxy.RoundTripObserver` (structurally, without importing `proxy`), keeps a count-based sliding window of recent outcomes per backend, and ejects via `MarkUnhealthy` after N failures within the window — recovering only when a later active probe is observed to have reinstated the backend; Sprint 4 adds `Forget` for a removed backend (ADR-0015). Health endpoint: `NewHandler` serves `/livez`, `/readyz`, `/startupz` (ADR-0014). |
| `internal/circuit` | Sprint 3 | `Breaker`: the circuit policy (consecutive-failure-to-open constant, config cooldown). Drives `Backend`'s circuit-state methods, implements `backend.CircuitGate` and `proxy.RoundTripObserver` structurally, and is installed by `app.Build` as both the registry gate and an observer. Logs and writes `lb_circuit_state` from the shared `CircuitTransition` (ADR-0013 decision 10). |

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

## Sprint 4 — Hard subsystems

Sprint 4's subsystems surround the request path built in Sprints 1–3 without
changing its shape, and they are assembled only through `internal/app`
(ADR-0015, ADR-0016, ADR-0017, ADR-0018, ADR-0019). The delivery record and the
exit-criteria evidence are in
[`docs/sprint-4-retro.md`](sprint-4-retro.md).

### Application seam (Sprint 4)

The whole wiring graph lives behind `app.Build(cfg, log)` and `app.Run(ctx)`.
`Build` constructs the metrics collector; the registry, and seeds every
backend's gauge series; installs the circuit breaker as both the registry's
`CircuitGate` and an observer; builds the selector; builds the proxy with the
configured transport, the metrics collector, and the latency, outlier, and
circuit observers; builds the active checker; and builds the health-endpoint
handler. `Run` starts the checker and the three servers on the shared process
context and shuts them down in order under one timeout.

`App` also owns the one piece of mutable wiring state: the loaded config, held
as `atomic.Pointer[config.Config]`, which is the baseline each reload diffs
against (ADR-0015 decision 12). `main` keeps only flags, file I/O, and signals;
`make run`, the chaos harness, and integration tests all build the same graph.
This is why every Sprint 4 test could drive the real system without signals or
temp files, and why the OS-boundary e2e (below) had a seam to observe.

### Zero-downtime reload and drain (Sprint 4)

An operator edits the backend list and sends SIGHUP. `main` installs SIGHUP on
its own channel, separate from the SIGINT/SIGTERM shutdown context, and
`reloadLoop` reads it: it drains any further queued SIGHUPs (so a burst
collapses to one reload), then `config.Load` → `Validate` → `App.Reload`; a
parse or validation failure is logged and the previous config keeps serving.
`App.Reload` is called by one goroutine at a time (ADR-0015 decision 6).

- **The diff is pure and identity is `(name, URL)`.** `config.DiffBackends`
  reports added/unchanged in the new file's order and removed in the old
  file's; a name whose URL changed is one removed plus one added, never an
  "updated" backend, so state learned about the old host is never applied to
  the new one. `config.NonBackendChanges` names every changed non-backend
  field — `listen`, `algorithm`, `health`, `circuit`, `metrics`,
  `health_endpoint`, `reload`, `server`, `transport` — and any one of them
  rejects the reload whole, logged with the fields named (ADR-0015 decision 4).
- **The registry swaps an immutable versioned snapshot behind one atomic
  pointer.** `Registry.Apply` is the single writer: it reuses the existing
  instance for each unchanged identity (keeping health, circuit, active
  connections, and EWMA latency) and constructs a fresh one for each added
  identity. Removed backends are marked removed *before* the swap, and the
  proxy skips its whole observer fan-out for them, so no observer learns a
  backend left the fleet; active-connection accounting is deliberately not
  observer-driven and still releases (ADR-0015 decisions 5–8).
- **Admission is asymmetric.** A backend added by reload starts unhealthy and
  is admitted by one successful probe (a reinstatement with reason
  `initial_probe`); a backend present at startup starts healthy. A blue/green
  reload that replaces every backend therefore has a brief empty-selectable
  window, logged at WARN because `unchanged=0` (ADR-0015 decisions 10–11).
- **Draining bounds a removed backend's in-flight requests.** `App.Reload`
  starts one goroutine per removed backend. Phase one waits — polling
  `ActiveConns()` every 100ms — for the count to reach zero within
  `reload.drain_window` (default 30s). On expiry, phase two calls
  `Backend.Retire()`, which cancels the backend's retired context; the proxy
  joined that context per request with `context.AfterFunc`, so the in-flight
  requests are cancelled with cause `ErrDrainWindowExpired` and finish with a
  502, and the drain waits for their slots to release before logging one
  `backend drained` line. The join costs the request path no goroutine and no
  lock. A drain is keyed to the removed instance, not its name, and runs
  independently of later reloads (ADR-0016).
- **The consistent-hash ring rebuilds on demand**, keyed on the snapshot
  version, so no reload path has to know a ring exists (ADR-0015 decision 9).

### Connection lifecycle (Sprint 4)

The proxy's failed-round-trip handling classifies each failure into exactly
one tier, in order, using the client's own request context
(`reqState.clientCtx`) as the discriminator (ADR-0017):

1. **Drain cancellation** — the outbound context was cancelled by a retired
   backend: WARN with reason `window_expired`, no observer (the backend is
   already removed).
2. **Client-gone** — `clientCtx.Err() != nil`: INFO with reason
   `client_canceled`, recorded as **499** (`status_class="4xx"`), no observer
   and no EWMA latency, the active-connection slot released, and
   `Backend.RearmTrial()` called so a cancelled half-open trial cannot wedge a
   live backend.
3. **Genuine transport failure** — the fallback: WARN, the 2s failure penalty
   fanned out to every observer, **502**.

A backend that dies after the response headers have arrived is handled
separately: the body wrapper counts copied bytes and logs a non-EOF read error
at WARN with reason `backend_died_mid_response`, the success recorded when the
headers arrived stands, and no second observer outcome is fed (S4.T6). The
known limitation — a mid-body death does not itself eject the backend — is
recorded in the session log.

Slow-loris clients are bounded by `server.read_timeout` (default 60s), wired to
the client server's `ReadTimeout`; `WriteTimeout` is deliberately omitted
because it would span the whole response copy and trip on a slow-but-healthy
upstream (S4.T7).

### Transport tuning (Sprint 4)

A `transport:` section supplies four knobs — `dial_timeout` (default 5s),
`response_header_timeout` (default 30s), `max_idle_conns_per_host` (default
100), and `idle_conn_timeout` (default 90s) — and `app.Build` installs an
`*http.Transport` built from them in place of `http.DefaultTransport`. The
total `MaxIdleConns` is sized as `max_idle_conns_per_host × backend count`, so
the per-host knob is not silently capped by the stdlib default of 100 (S4.T8).

### Env interpolation (Sprint 4)

`config.Load` is no longer pure deserialization: after YAML decode and before
returning, it expands `${VAR}` references in every **backend URL** from the
process environment, so the resolved URL is what `Validate` checks and a secret
can live only in the environment. `VAR` must match `[A-Za-z_][A-Za-z0-9_]*`;
there is deliberately no `${VAR:-default}` syntax and no escape hatch. An
unset or empty variable, an unterminated `${`, an empty `${}`, or an invalid
name fails the load naming the backend (and the variable); the resolved value
is never included in an error, and Sprint 4's redaction change made
`validateBackendURL` name only the backend and the `url` field across all five
of its branches (S4.T16, S4.T17). Non-backend fields pass through untouched.
Interpolation re-runs on every `Load`, so an environment change between
reloads resolves to the new value and — by the `(name, URL)` identity rule —
diffs as one removed plus one added backend. `configs/example.yaml` documents
the feature; `configs/docker.yaml` stays interpolation-free.

### Soak, pprof, and the OS-boundary e2e (Sprint 4)

- **pprof** is mounted on the metrics listener beside `/metrics` — no second
  listener, no new config. A goroutine-leak audit (S4.T9) drives cancellations
  and backend deaths through the app seam and asserts the goroutine count
  settles back to its baseline within a delta of 10.
- **Soak test.** `TestChaosSoakConnectionLifecycle`, run by `make soak`
  (both tolerances) or `make soak-race` (goroutine tolerance only; `-race`
  makes the heap assertion meaningless), cycles warmup, steady load, client
  cancellations, backend deaths, SIGHUP reloads, and a quiet phase for a
  flag-gated duration (default 1h, `-soak-duration` to shorten). Committed
  tolerances: goroutines ≤ warmup baseline + 10 and post-GC `HeapAlloc` ≤
  baseline + 8 MB. The full one-hour pass (the source of truth is the
  `PROGRESS.md` S4.T10 entry): **59.66M requests, 7,175 cancellations, 2,789
  failures, 12 reloads; goroutines 35→30; post-GC heap 930 KB→804 KB** — the
  production-resilience claim has numbers.
- **OS-boundary e2e.** `TestChaosSighupReloadZeroDrop1000`, run by `make e2e`
  (flag-gated by `-e2e`, skips cleanly without a Go toolchain), is the
  **OS-boundary sibling** of the in-process
  `TestChaosReloadDrainExitCriterion1000`. It builds the real binary, spawns
  it with a temp config, holds 1000 concurrent requests in flight through
  gated counting backends, rewrites the config file atomically, sends a real
  `syscall.Kill(pid, SIGHUP)`, proves live traffic shifts to the added backend
  and never touches the removed one (whose counter stays frozen), then asserts
  all 1000 return 200 and the process exits 0 on `SIGTERM`. The division of
  labor is explicit: registry-view assertions (instance identity, EWMA/circuit
  state survival) stay in-process; the e2e asserts externally visible outcomes
  only (statuses, counts, exit status) (S4.T14).

## Decision index

All non-trivial decisions are recorded in `docs/adr/`. Accepted:

| ADR | Title | Status |
|-----|-------|--------|
| [0001](adr/0001-record-adrs.md) | Record architecture decisions | Accepted |
| [0002](adr/0002-interface-placement-and-schema-freeze.md) | Interface placement and Sprint 1 schema freeze | Accepted |
| [0003](adr/0003-pin-unused-deps-with-tools-go.md) | Pin not-yet-imported dependencies with a build-tagged tools.go | Superseded (tools.go deleted) |
| [0004](adr/0004-reject-unimplemented-algorithms-in-validate.md) | Reject unimplemented algorithms in Validate | Accepted |
| [0005](adr/0005-scope-of-production-grade.md) | Scope of "production-grade" | Accepted |
| [0006](adr/0006-backend-sethealthy-amends-adr-0002.md) | Add Backend.SetHealthy, amending ADR-0002 decision 5 | Superseded by ADR-0011 d2 |
| [0007](adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md) | Proxy request lifecycle and exactly-once active-connection decrement | Accepted |
| [0008](adr/0008-consistent-hash-ring-pipeline-and-vnode-layout.md) | Consistent-hash ring hash pipeline, vnode key order, and vnode count | Accepted |
| [0009](adr/0009-consistent-hash-bounded-loads-capacity-and-evidence.md) | Consistent-hash bounded loads: epsilon, load metric, capacity formula, and hot-key evidence | Accepted |
| [0010](adr/0010-p2c-ewma-backend-latency-state-cold-start-and-failure-penalty.md) | P2C-EWMA: Backend-owned latency state, cold-start semantics, and the failure penalty | Accepted |
| [0011](adr/0011-health-passive-outlier-and-circuit-breaker-composition.md) | Health, passive-outlier, and circuit-breaker composition | Accepted |
| [0012](adr/0012-circuit-gate-interface-and-backend-state-methods.md) | Circuit breaker gate interface, Registry-mediated admission, and Backend state API | Accepted |
| [0013](adr/0013-observability-metrics-logging-and-integration.md) | Observability: metrics collector, transition logging, and their integration | Accepted |
| [0014](adr/0014-health-endpoint-contract-and-probe-semantics.md) | Health endpoint contract and probe semantics | Accepted |
| [0015](adr/0015-reload-architecture.md) | Zero-downtime reload architecture: in-process snapshot swap, backend identity, and admission | Accepted |
| [0016](adr/0016-drain-lifecycle.md) | Drain lifecycle: retired-context join, two-phase drain, and cancel-at-window | Accepted |
| [0017](adr/0017-client-gone-classification-and-trial-rearm.md) | Client-gone classification and half-open trial re-arm | Accepted |
| [0018](adr/0018-no-retry-ever.md) | No retry, ever: a failed round trip is classified and surfaced, never repeated | Accepted |
| [0019](adr/0019-deployment-target.md) | Deployment target — the Docker container as the packaging artifact | Accepted |

Every Sprint 4 decision that had a tracking row is now written: reload
architecture (ADR-0015), the deployment target ADR-0005 deferred (ADR-0019,
S4.T19), and the retry policy (ADR-0018, S4.T11). No decision remains "tracked
but not yet written".

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
edited. Seven ADRs accepted after the freeze amend rows of its
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
- [ADR-0015](adr/0015-reload-architecture.md) amends ADR-0002 decision 5 once
  more: `Backend` gains an unexported atomic removed flag with a methods-only
  accessor, and the registry's backend set becomes an immutable versioned
  snapshot behind one atomic pointer (ADR-0015 decisions 5 and 7).
- [ADR-0016](adr/0016-drain-lifecycle.md) amends ADR-0002 decision 5 with
  `Backend.Retire`/`RetiredContext`, and amends the frozen error-handling
  convention by exporting a second value, `backend.ErrDrainWindowExpired` —
  a cross-package `context.Cause` comparison value, not a branchable sentinel,
  so `ErrNoHealthyBackends` remains the only one of those (ADR-0016 decision 2,
  AGENTS.md key decision 7).
- [ADR-0017](adr/0017-client-gone-classification-and-trial-rearm.md) amends
  ADR-0002 decision 5 with `Backend.RearmTrial()`, amends ADR-0007 decisions
  4–5 for the client-gone tier (499 at INFO instead of 502 at WARN), and
  amends ADR-0012 to re-arm a half-open trial a client-gone request held.

Read those seven ADRs alongside the contracts doc's
[concurrency-ownership table](design/sprint-1-contracts.md#concurrency-ownership-table).

## Deliberately not here yet

A future agent should not assume any of the following exist. Each names its
owning sprint:

- **HTTP/2 (client-facing and to backends)** — Sprint 5.
- **The benchmark rig and published numbers** — Sprint 5.
- **SO_REUSEPORT socket handoff**, the mechanism that would make a *binary or
  image* change zero-downtime by starting a replacement process on the same
  port — Post-Sprint 5 (`MILESTONES.md` "Optional extensions"; ADR-0019 records
  why the container target does not need it for backend-list changes).
- **gRPC pass-through, rate limiting, request retries with an
  idempotency-aware policy, WebSocket upgrade validation** — Post-Sprint 5
  (`MILESTONES.md` "Optional extensions"). Request retry in particular is
  deliberately absent by design — see
  [ADR-0018](adr/0018-no-retry-ever.md).
