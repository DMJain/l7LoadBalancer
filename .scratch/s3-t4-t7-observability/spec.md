# Spec: S3.T4–T7 — Metrics, Logging, Integration, and the Grafana Dashboard

Status: ready-for-agent

---

## Problem Statement

Sprint 3's resilience subsystems (S3.T1–T3: active health checks, passive
outlier detection, circuit breaker) are fully implemented and correctly
enforce failure isolation, but they are completely invisible from outside the
process. `internal/metrics` is a stub — its `doc.go` only reserves metric
names and label vocabulary, `metrics.go` implements nothing. `internal/logger`
has zero test coverage. More importantly, `internal/health` and
`internal/circuit` never emit a single `slog` line for the events they exist
to produce: a backend can be ejected, reinstated, or have its circuit open,
close, or half-open, and nothing outside the process — no log line, no
metric — records that it happened. `MILESTONES.md`'s Sprint 3 exit criteria
("Grafana shows per-backend latency histograms and circuit state," "chaos
test: injecting 500s on one backend eventually opens its circuit") describe
outcomes an operator can observe; today there is no way to observe any of it
except by attaching a debugger to `Backend`'s in-memory state.

Closing this gap safely is harder than wiring in a Prometheus client. The
frozen package dependency graph makes `internal/metrics` a leaf with no
internal imports. The existing `RoundTripObserver` fan-out (S3.T0.2) only
fires around an actual backend round trip, and silently misses two of the
three ways a request can end in a 503 — no healthy backend, and
circuit-denied — because both short-circuit before dispatch. And the circuit
breaker's lazy, read-triggered Half-Open promotion (ADR-0011/ADR-0012) has no
single reliable call site to hang a log line or gauge update off: the same
CAS that performs the promotion is just as likely to be won by a
`Registry.Selectable()` scan (which has no logger reachable from it) as by a
live request through `Allow()` (which does).

## Solution

Four cooperating pieces of work, built in dependency order:

1. **Metrics package (S3.T4)** — `internal/metrics` becomes a real,
   leaf-only Prometheus `Collector` wrapping `client_golang`, exposing plain
   push methods for the four already-reserved metric names plus one new one
   (`lb_active_connections`), served via a new, always-on `metrics.listen`
   HTTP endpoint separate from client traffic.
2. **Logger (S3.T5)** — `internal/logger` gets the test coverage it never
   had. `internal/health` and `internal/circuit` gain their first-ever
   `slog` lines, for exactly the transitions this sprint's subsystems can
   produce, edge-triggered so a sustained failure or success streak logs
   once, not once per probe or request. This requires giving `Backend`'s
   circuit methods a way to report whether a given call actually changed
   the circuit's state, without giving `internal/backend` a logging
   dependency it doesn't have today.
3. **Integration (S3.T6)** — `proxy` and `internal/health`/`internal/circuit`
   are wired to push into the now-real `metrics.Collector`, reusing — not
   duplicating — the exact edge-triggering machinery S3.T5 already builds
   for logging. `internal/balancer` needs no changes at all: the request
   counter's completeness comes from a proxy-level hook that already sees
   every exit path, not from balancer gaining a metrics dependency.
4. **Grafana dashboard + demo stack (S3.T7)** — a five-panel dashboard
   covering every reserved metric, plus a new docker-compose stack
   (Prometheus + Grafana, kept separate from S1.T9's dummy-backends-only
   compose) so the exit criteria are demonstrable from a clean checkout, not
   just declared in a JSON file nobody can load.

After this lands: every health/circuit state transition produces exactly one
log line and one gauge update from the same signal; every request — success,
no-healthy-backend 503, circuit-denied 503, and backend error alike — is
counted and timed; and `docker compose up` on the new stack shows a correct,
complete dashboard from the moment the stack comes up, before any traffic has
flowed.

## User Stories

### S3.T4 — Metrics package

1. As a project maintainer, I want `internal/metrics` to remain a true leaf
   package (no internal imports), with `proxy`/`health`/`circuit` pushing
   into it instead of `metrics` reading their state, so the frozen package
   dependency graph in `AGENTS.md` doesn't need amending.
2. As a project maintainer, I want a single `Collector` type wrapping
   `github.com/prometheus/client_golang`, owning its own private
   `prometheus.NewRegistry()` rather than the global `DefaultRegisterer`, so
   tests can construct independent instances without cross-test collisions
   and can use `t.Parallel()` freely.
3. As a project maintainer, I want the request counter and latency histogram
   implemented under the `lb_requests_total` / `lb_request_duration_seconds`
   names already frozen in `doc.go`, with labels `backend`, `method`,
   `status_class` exactly as reserved — never `status_code`, to avoid
   unbounded cardinality.
4. As a project maintainer, I want `lb_request_duration_seconds` to measure
   the whole client-facing request — selection, circuit-check, and backend
   round trip together — not just the backend leg, because that is the
   conventional meaning of "request duration" for a load balancer and what
   an operator reading the dashboard will assume it means.
5. As a project maintainer, I want the histogram buckets to be the
   provisional set already proposed in `doc.go`
   (`.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10` seconds), documented
   as provisional pending Sprint 5's real benchmark numbers, rather than
   inventing new numbers with no data behind them.
6. As a project maintainer, I want a new `lb_active_connections` gauge
   (label `backend`), amending the frozen metric-name reservations in
   `internal/metrics/doc.go` and `docs/design/sprint-1-contracts.md` the
   same lightweight way S3.T0.3's config additions were — no new ADR
   number, since this fills an acknowledged `MILESTONES.md` gap rather than
   reversing a locked decision.
7. As a project maintainer, I want `lb_circuit_state` implemented as a
   label-enum (`state="closed"/"open"/"half_open"`, each backend having
   exactly one of the three series at `1` and the other two at `0`), not a
   numeric-encoded single gauge, so there is no encoding table to keep in
   sync with `internal/circuit`'s state type and the metric is directly
   alertable/queryable by state name.
8. As a project maintainer, I want the setter behind `lb_circuit_state` to
   unconditionally zero the two non-target states for that backend on
   **every** call, not only the first — so the exactly-one-state-is-`1`
   invariant can never be silently broken by a later code path that sets
   the new state without clearing the old one.
9. As a project maintainer, I want `lb_backend_healthy` implemented as a
   plain per-backend `0`/`1` gauge, since it carries none of
   `lb_circuit_state`'s enum-mutual-exclusion concern.
10. As a project maintainer, I want every backend's `lb_backend_healthy` and
    `lb_circuit_state` seeded to their known Sprint-1/ADR-0011 initial
    values (`healthy=1`, `state=closed`) and `lb_active_connections` seeded
    to `0`, at startup, using the exact same `Collector` methods real
    transitions use — not a separate seeding code path — so a freshly
    started, never-degraded system renders a complete, correct dashboard on
    the very first scrape instead of blank panels (Prometheus `Vec` metrics
    don't materialize a series until first written).
11. As a load balancer operator, I want a new `metrics.listen` YAML config
    field, always-on and defaulting to `:9090` when omitted, so metrics are
    reachable without extra configuration but the port stays tunable per
    deployment.
12. As a project maintainer, I want `metrics.listen` validated the same way
    S3.T0.3's `health`/`circuit` fields were — `KnownFields(true)`
    strictness preserved, and an explicit test asserting the default fires
    when the field is omitted, not just a happy-path test with the value
    set.
13. As a load balancer operator, I want `/metrics` served via
    `promhttp.HandlerFor(registry, ...)` on its own `http.Server`, separate
    from the client-traffic listener, so metrics scraping is never mixed
    with, or exposed alongside, real request traffic.
14. As a project maintainer, I want the metrics HTTP server started and shut
    down alongside the main server in `main.go`, sharing the existing
    `sigCtx`, so no second shutdown primitive is introduced — mirroring
    ADR-0011 decision 13's health-checker precedent.
15. As a project maintainer, I want an explicit test asserting the full
    reserved-plus-new metric name and label set is present and correctly
    shaped in `/metrics`'s scraped text output (via `promhttp` served
    against `httptest.NewServer`), so a typo'd metric or label name is
    caught here, not discovered later in Grafana.

### S3.T5 — Logger

16. As a project maintainer, I want `internal/logger/logger_test.go` to exist
    for the first time, covering `logger.New`'s level threshold behavior and
    JSON output shape, since it has shipped since Sprint 1 with zero test
    coverage.
17. As a load balancer operator, I want a distinct, structured log line every
    time a backend is ejected, reinstated, or its circuit opens, closes, or
    half-opens, so I can correlate a dashboard state change to an
    operator-readable log line without inferring it from gauge deltas
    alone.
18. As a project maintainer, I want three new canonical log fields —
    `backend` (reused from the existing request-scoped vocabulary),
    `event`, and `reason` — added to the frozen field vocabulary in
    `internal/logger/doc.go`, since none of the six existing fields (all
    request-scoped: `backend`, `method`, `status`, `latency_ms`,
    `remote_addr`, `path`) fit a state-transition line.
19. As a project maintainer, I want `event` and `reason` values drawn from
    closed, Go-constant, snake_case vocabularies — matching the convention
    already established for algorithm identifiers (`round_robin`, etc.) —
    rather than free-form strings, so a future agent grepping logs for
    `circuit_opened` can't be defeated by a typo'd variant.
20. As a project maintainer, I want the `event` vocabulary to be exactly
    `health_ejected`, `health_reinstated`, `circuit_opened`,
    `circuit_closed`, `circuit_half_opened` — one value per transition this
    sprint's subsystems can produce, no more, no fewer.
21. As a project maintainer, I want the `reason` vocabulary to be exactly
    `probe_failures` / `probe_recovered` (active health), `outlier_window`
    (passive detection), `consecutive_failures` / `trial_success` /
    `trial_failure` / `cooldown_elapsed` (circuit) — covering every trigger
    currently defined in ADR-0011/ADR-0012, with `probe_recovered` added
    specifically because `health_reinstated` had no symmetric `reason`
    value in the original design-session proposal.
22. As a project maintainer, I want `health_ejected`, `circuit_opened`, and
    `circuit_half_opened`-via-`trial_failure` logged at WARN, and
    `health_reinstated`, `circuit_closed`, and `circuit_half_opened`-via-
    `cooldown_elapsed` logged at INFO — matching the existing 5xx-is-WARN /
    else-INFO convention `logRequest` and the circuit-denial line already
    use — with `circuit_half_opened` specifically at INFO because admitting
    a trial is a routine, expected lifecycle event, not an adverse one.
23. As a project maintainer, I want the active health checker's transition
    log line gated on an exact-equality check against its existing
    consecutive success/failure counters (`== threshold`), not the `>=`
    check the `Mark*` calls themselves already use, so a sustained failure
    or success streak logs exactly once per genuine transition instead of
    once per probe for the rest of the streak — reusing the counters
    `prober` already owns, with no new field added to it.
24. As a project maintainer, I want the passive outlier detector's ejection
    log line to reuse its existing `ejected`-per-episode guard (the same
    one that already makes `MarkUnhealthy` fire exactly once per episode),
    so no new edge-triggering logic is needed there.
25. As a project maintainer, I want `Backend.CircuitFailure`,
    `Backend.CircuitSuccess`, and `Backend.CircuitAllow` to each return a
    new exported `backend.CircuitTransition` value (`NoChange` / `Opened` /
    `Closed` / `HalfOpened`) alongside their existing return value, so a
    caller can tell whether this specific call was the one that changed the
    circuit's state — with `internal/backend` gaining **no** new
    dependency, since `CircuitTransition` is a plain type, not a logging
    call.
26. As a project maintainer, I want `internal/circuit.Breaker` to gain a
    `*slog.Logger` (via a new `circuit.New(cooldown, logger)` constructor
    parameter) and to log a transition line at its own
    `ObserveRoundTrip`/`Allow` call sites whenever the new
    `CircuitTransition` value is not `NoChange`, so the log line lands
    exactly where the state actually changed — without changing the
    `backend.CircuitGate` or `proxy.RoundTripObserver` interface signatures
    `Breaker` already implements (those wrapper methods absorb the extra
    return value internally).
27. As a project maintainer, I want it explicitly documented — not silently
    implied to self-correct — that a Half-Open promotion whose CAS is won
    by a `Registry.Selectable()` scan (the common case, since scans vastly
    outnumber any one backend's own selection/trial events) is **never**
    logged: `Backend.CircuitOpen()`, the method `Selectable()` calls, has
    no path to a logger without a cross-package plumbing change out of
    scope this sprint. This is an accepted, permanent, named gap — same
    shape as ADR-0012's stale-while-half-open limitation — not a
    transition that surfaces late.

### S3.T6 — Integration

28. As a project maintainer, I want a new whole-request observation added at
    the exact place `proxy.ServeHTTP`'s existing `start := time.Now()` and
    deferred `logRequest` already sit, so `lb_requests_total` and
    `lb_request_duration_seconds` are driven by the same timer and the same
    four-exit-path coverage (success, no-healthy-backend 503, circuit-denied
    503, `ErrorHandler`) the existing per-request log line already has —
    with no new hook mechanism invented.
29. As a load balancer operator, I want a request that never reaches a
    backend (no healthy backend found) counted in `lb_requests_total` with
    `backend=""` — a real, distinct label value, not silently dropped or
    merged into an existing backend's count — so "the balancer had nothing
    to route to" is visible as its own signal on the dashboard.
30. As a load balancer operator, I want a request denied by the circuit
    breaker counted in `lb_requests_total` with the **real** backend label
    (`Select()` already identified one before `Allow()` denied it), distinct
    from the no-healthy-backend case, so "this specific backend is tripping
    its circuit" and "no backend exists at all" are never conflated into
    one bucket.
31. As a project maintainer, I want `RoundTripObserver`'s existing duration
    measurement to remain purely internal (feeding P2C-EWMA/health/circuit
    exactly as it already does) and never itself exposed as
    `lb_request_duration_seconds`, so there is exactly one definition of
    "request duration" in the metrics surface.
32. As a project maintainer, I want `internal/balancer` to require **zero**
    code changes and zero new imports for this sprint — `ErrNoHealthyBackends`
    is already visible to `proxy` (the whole-request hook's owner), so
    "integrate metrics into balancer" is satisfied by the request counter
    correctly reflecting balancer-level selection failures, not by balancer
    importing `metrics`.
33. As a load balancer operator, I want `lb_active_connections`
    incremented/decremented at the exact same call sites `proxy` already
    uses for `Backend.IncActive()`/`DecActive()`, so the gauge can never
    drift from the real active-connection count those methods track.
34. As a load balancer operator, I want `lb_backend_healthy` and
    `lb_circuit_state` updated from the same edge-triggered signals S3.T5's
    log lines already consume (the active checker's `==` gate, the outlier
    detector's `ejected`-episode guard, and `circuit.Breaker`'s
    `CircuitTransition != NoChange` check) — not a second, independently
    written detection path — so the log line and the gauge for a given
    transition can never disagree about whether or when it happened.
35. As a project maintainer, I want `internal/health` (`Checker` and
    `OutlierDetector`) and `internal/circuit.Breaker` to each receive a
    `*metrics.Collector` alongside the `*slog.Logger` S3.T5 already added,
    via the same constructors, so both observability surfaces are wired at
    the same construction sites in `main.go` rather than metrics wiring
    becoming a second, parallel set of constructor changes.
36. As a project maintainer, I want no new fan-out interface (no
    `RequestObserver` mirroring `RoundTripObserver`) introduced for the
    whole-request hook — `metrics` is its only consumer, unlike
    `RoundTripObserver`'s three, so `proxy` calling directly into a held
    `*metrics.Collector` (or a small consumer-defined interface if a test
    seam is wanted, at the implementer's discretion) is sufficient and
    avoids an abstraction with only one real implementation.
37. As a project maintainer, I want `main.go`'s startup gauge-seeding loop
    (story 10) to be the only place that calls the `Collector`'s
    health/circuit setters outside of a real transition, and for it to call
    the exact same methods real transitions use, so there is never a
    second, divergent "initial state" code path to keep in sync.
38. As a project maintainer, I want every existing `proxy`/`health`/`circuit`
    test that currently asserts on log output or `Backend` state to keep
    passing unchanged, with new tests added alongside for the metrics
    assertions — not existing tests rewritten to accommodate the metrics
    wiring — so this integration is proven additive, not a silent behavior
    change to already-shipped Sprint 3 subsystems.
39. As a load balancer operator, I want `docker stop`-ing a backend to
    produce a coordinated set of signals within one probe interval — a WARN
    log line, `lb_backend_healthy` dropping to `0`, and (if the failure
    continues) `lb_circuit_state` moving to `open` — all attributable to the
    same underlying transition, satisfying `MILESTONES.md`'s Sprint 3 exit
    criteria end-to-end for the first time.

### S3.T7 — Grafana dashboard + demo stack

40. As a load balancer operator, I want a Grafana dashboard JSON file
    covering exactly the reserved-metric set — request rate by backend and
    `status_class`, latency p50/p99 per backend, a per-backend circuit-state
    panel, a per-backend healthy panel, and a per-backend active-connections
    panel — five panels, nothing invented beyond what S3.T4 actually
    instruments.
41. As a load balancer operator, I want the circuit-state panel to read the
    `lb_circuit_state` label-enum directly (e.g. a state-timeline or table
    querying `lb_circuit_state == 1`), so "which state is this backend in
    right now" is answerable from the panel without a numeric-encoding
    lookup table.
42. As a load balancer operator, I want a new docker-compose stack under
    `deployments/docker/observability/` adding Prometheus and Grafana
    services (with dashboard/datasource provisioning) and a Prometheus
    scrape config pointed at the load balancer's `metrics.listen` port, so
    the dashboard is actually loadable and the exit criteria are
    demonstrable from a clean `docker compose up`, not just a JSON file
    with no way to view it.
43. As a project maintainer, I want this new compose file kept entirely
    separate from S1.T9's dummy-backends-only compose and from Sprint 5's
    future bench compose, continuing the documented separation of concerns
    each of those already established, rather than growing S1.T9's file
    with unrelated services.
44. As a load balancer operator, I want the chaos-test exit criterion
    ("injecting 500s on one backend eventually opens its circuit") to be
    manually reproducible against this stack — stop or fault-inject a
    backend, watch `lb_backend_healthy` and `lb_circuit_state` move on the
    dashboard within the configured thresholds — documented as a repeatable
    smoke-test procedure, not asserted only by unit tests.
45. As a project maintainer, I want this task to require no Go unit tests of
    its own (the dashboard JSON and compose files carry no application
    logic), matching the precedent S1.T9's docker-compose task already set,
    with a documented manual/scripted smoke test recorded in the session
    log instead.
46. As a project maintainer, I want the Prometheus scrape interval and job
    naming to be reasonable, undocumented-as-a-decision defaults (e.g. a
    15s scrape interval) rather than something requiring its own design
    discussion, since neither has a real trade-off behind it worth
    recording.

## Implementation Decisions

Full rationale for every decision below belongs in a new ADR-0013, to be
written before implementation begins per `AGENTS.md` Step 2 — it does not
exist yet; this spec is the pre-ADR design-session record. This section
translates the design session's conclusions into buildable units.

### Metrics package (`internal/metrics`)

- A single `Collector` type, constructed with its own private
  `prometheus.Registry` (not `prometheus.DefaultRegisterer`).
- Instruments: `lb_requests_total` (`CounterVec`, labels `backend`,
  `method`, `status_class`), `lb_request_duration_seconds`
  (`HistogramVec`, same labels, buckets per `doc.go`'s provisional set),
  `lb_backend_healthy` (`GaugeVec`, label `backend`), `lb_circuit_state`
  (`GaugeVec`, labels `backend`, `state`), `lb_active_connections`
  (`GaugeVec`, label `backend` — new, amends `doc.go` +
  `sprint-1-contracts.md`, no new ADR number).
- The `lb_circuit_state` setter (name at the implementer's discretion, e.g.
  `SetCircuitState(backend, state)`) unconditionally sets the target
  state's series to `1` and the other two to `0` for that backend, on
  every call — this is a correctness requirement on the method itself, not
  an incidental detail of how callers happen to use it.
- `Config` gains a new `metrics:` section (`listen *string` or equivalent,
  following the `HealthConfig`/`CircuitConfig` nil-means-omitted pattern),
  defaulting to `:9090` when omitted, always-on (no disable toggle).
  `Validate()` gains the corresponding default-and-check logic;
  `KnownFields(true)` strictness is preserved.
- Exposition: `promhttp.HandlerFor(registry, promhttp.HandlerOpts{})` served
  on its own `http.Server`, started/stopped in `main.go` alongside the
  existing server, sharing `sigCtx`.
- New `go.mod` dependency: `github.com/prometheus/client_golang` (and its
  transitive deps).

### Logger (`internal/logger`, `internal/backend`, `internal/health`,
`internal/circuit`)

- `internal/logger/doc.go`'s canonical field vocabulary gains `event`,
  `reason` (new Go-constant, snake_case closed vocabularies — see User
  Stories 20–21 for the exact value sets).
- `backend.CircuitTransition`: new exported type (`NoChange`, `Opened`,
  `Closed`, `HalfOpened`), returned by `Backend.CircuitFailure`,
  `Backend.CircuitSuccess`, `Backend.CircuitAllow` alongside their existing
  return values. No new import added to `internal/backend`.
- `circuit.New` gains a `*slog.Logger` parameter; `circuit.Breaker` logs
  `circuit_opened`/`circuit_closed`/`circuit_half_opened` at its
  `ObserveRoundTrip`/`Allow` call sites whenever the transition value is
  not `NoChange`. The `backend.CircuitGate` and `proxy.RoundTripObserver`
  interfaces `Breaker` implements are untouched.
- `internal/health`'s active checker (`prober`) gains an equality-based
  (`== threshold`) log-emission gate alongside its existing `>=`-gated
  `Mark*` calls, using the counters it already owns — no new field.
  `OutlierDetector` logs at its existing `ejected`-guarded eject call site.
  Both packages gain a `*slog.Logger` (via their existing constructors, new
  parameter).
- The documented, permanent gap: a Half-Open promotion whose CAS is won by
  `Registry.Selectable()`'s read path (via `Backend.CircuitOpen()`) is
  never logged. Recorded explicitly in ADR-0013, not left implicit.

### Integration (`internal/proxy`, `internal/health`, `internal/circuit`)

- `proxy.Proxy` holds a metrics-observing reference (concrete
  `*metrics.Collector`, or a small consumer-defined interface at the
  implementer's discretion — no new fan-out interface). It is called from
  the same deferred-hook location as `logRequest`, using the same `state`/
  `start`/`r` already assembled there, deriving `backend=""` the same way
  `logRequest` already does when `state.backend` is nil.
- `health.Checker`, `health.OutlierDetector`, and `circuit.Breaker` each
  gain a `*metrics.Collector` parameter alongside the `*slog.Logger` S3.T5
  adds, via their existing constructors. Gauge updates happen at the exact
  same edge-triggered call sites the new log lines use — the same
  `CircuitTransition`/`==`-gate/`ejected`-guard signals drive both.
- `main.go` seeds every backend's `lb_backend_healthy=1`,
  `lb_circuit_state=closed`, `lb_active_connections=0` immediately after
  `backend.NewRegistry` succeeds, using the same `Collector` methods real
  transitions use.
- `internal/balancer`: no changes.

### Grafana dashboard + demo stack (`deployments/docker/observability/`,
new dashboard JSON)

- New docker-compose file (Prometheus + Grafana + provisioning), separate
  from `deployments/docker/docker-compose.yml` (S1.T9) and Sprint 5's
  future bench compose.
- Prometheus scrape config targets the load balancer's `metrics.listen`
  port; reasonable default scrape interval (e.g. 15s), not a designed
  decision.
- One dashboard JSON, five panels: request rate (by `backend`,
  `status_class`), latency p50/p99 (by `backend`), circuit state (label-enum
  read), backend healthy, active connections.

## Testing Decisions

### What makes a good test here

Tests exercise external, observable behavior: scraped `/metrics` text
output, `Backend`/`Breaker` method return values, HTTP responses and log
lines a real client/operator would see. Never internal mechanics (a CAS
retry count, a `prometheus` internal representation) directly. Table-driven
where the input space is enumerable; `testify/require` for setup,
`testify/assert` for value checks, matching every existing package.
Prometheus-specific assertions use `github.com/prometheus/client_golang/
prometheus/testutil` (`ToFloat64`, `CollectAndCompare`) rather than
string-matching scraped text where a typed helper exists.

### Seams

1. **`metrics.Collector`'s own method API** — new seam. Direct construction
   against a private registry, calling `IncActiveConnections`,
   `ObserveRequest`, `SetBackendHealthy`, `SetCircuitState`, etc. directly,
   asserted via `testutil`. Mirrors how `Backend`'s own method API is
   tested directly.
2. **The metrics HTTP exposition seam** — new. `httptest.NewServer`
   wrapping `promhttp.HandlerFor(registry, ...)` directly (no full binary
   needed), scraping and asserting the text output after driving the
   `Collector` through its methods.
3. **The whole-request hook, layered onto the existing `proxy.Proxy`
   `ServeHTTP` seam** (the `httptest`-backed harness from S1.T6/ADR-0007,
   extended by S2.T3, S3.T2, S3.T3). Reuses the existing fixture set (2xx,
   5xx, no-healthy-backend, circuit-denied, connection-refused) to assert
   the counter and histogram fire on every exit path with the correct
   labels, including `backend=""`.
4. **`Backend`'s own method API for circuit transitions** (existing seam
   from S1.T3/S3.T3, extended) — direct assertions on the new
   `CircuitTransition` return values from `CircuitFailure`/`CircuitSuccess`/
   `CircuitAllow`, no HTTP needed.
5. **`circuit.Breaker`'s own method API** (existing seam from S3.T3,
   extended) — asserts logging (captured via a buffered `slog.JSONHandler`,
   the same pattern `proxy_test.go` already uses) and confirms a repeated
   identical outcome (e.g. two consecutive failures while already `Open`)
   produces **no** additional log line, mirroring the "exactly once, not
   once per failure" property already proven for passive outlier ejection.
6. **`internal/health`'s existing direct seams** (active checker's
   probe-cycle seam, outlier detector's `ObserveRoundTrip` seam) — extended
   for the `==`-gated log emission, same fixture sets S3.T1/T2 already use.
7. **`config.Load`/`Validate` seam** (existing, from S1.T2/S3.T0.3) —
   extended for `metrics.listen`: omission → default, explicit → kept,
   nested-typo rejected (`KnownFields`), matching S3.T0.3's precedent case
   shape exactly.

### Test groupings and cases

- **`metrics.Collector`**: each instrument's basic Inc/Observe/Set behavior;
  `SetCircuitState` zeroes the other two states on every call (not just the
  first) — an explicit test asserting all three series after two sequential
  transitions, not just the latest one; `/metrics` exposition contains every
  reserved-plus-new name and label combination after driving the collector.
- **Logger**: level threshold behavior; JSON shape and canonical field
  presence for both request-scoped and transition-scoped log lines.
- **Active health checker**: existing S3.T1 fixture set, now also asserting
  exactly one `health_ejected`/`health_reinstated` log line per genuine
  transition — a sustained multi-probe failure streak logs once, not once
  per probe.
- **Passive outlier detector**: existing S3.T2 fixture set, now also
  asserting exactly one `health_ejected` log line per ejection episode.
- **Circuit breaker**: existing S3.T3 fixture set, now also asserting
  `CircuitTransition` return values match the actual state change (or
  `NoChange` when repeated), and that `circuit_opened`/`circuit_closed`/
  `circuit_half_opened` log lines fire exactly at those transitions and
  nowhere else.
- **Whole-request metrics hook**: 2xx → counter incremented with real
  backend/method/2xx label, non-zero histogram observation; no-healthy-
  backend → counter incremented with `backend=""`, `5xx` (503) class;
  circuit-denied → counter incremented with the **real** backend label,
  `5xx` class; backend error via `ErrorHandler` → counter incremented with
  real backend label, `5xx` class.
- **Startup seeding**: after `backend.NewRegistry` + seeding, `/metrics`
  shows every backend at `healthy=1`, `state=closed` (all three series
  present, only `closed=1`), `active_connections=0` — before any request is
  served.
- **Config**: `metrics.listen` omitted → `:9090`; explicit value → kept;
  nested unknown-field typo under `metrics:` → rejected.

### Prior art

- `internal/backend/backend_test.go`, `internal/circuit/circuit_test.go`
  (S1.T3, S3.T3) — the direct-method seam for `Backend`'s atomics and
  circuit state, including concurrent `-race` cases.
- `internal/proxy/proxy_test.go`'s `captureLogger`/log-line assertion
  pattern (S1.T6 onward) — the buffered-`slog.JSONHandler` seam this spec's
  new transition-log tests reuse verbatim.
- `internal/health/outlier_test.go`'s "exactly once, not once per failure"
  ejection test (S3.T2) — the precedent this spec's circuit/health
  transition-log tests mirror.
- `internal/config/config_validate_test.go` (S1.2, S3.T0.3) — the
  omitted/explicit/rejected-typo table-case shape `metrics.listen`'s tests
  follow.
- `deployments/docker/docker-compose.yml` + `dummy-backend/README.md`
  (S1.T9) — the precedent for a docs-only, no-Go-tests deliverable with a
  documented manual smoke test instead.

## Out of Scope

- **A second, backend-round-trip-only latency histogram.** Nothing in
  `doc.go`/`MILESTONES.md` reserves one; `RoundTripObserver`'s duration
  stays an internal signal only.
- **A config toggle to disable metrics exposition.** Always-on, matching
  the `health:`/`circuit:` precedent of no per-subsystem disable switch.
- **A `RequestObserver` fan-out interface** mirroring `RoundTripObserver`.
  `metrics` is the whole-request hook's only consumer; a fan-out
  abstraction with one real implementation isn't justified.
- **Remediating the Half-Open-promotion-via-`Selectable()`-scan logging
  gap.** Documented as a permanent, accepted limitation in ADR-0013, not
  fixed this sprint — fixing it is a cross-package plumbing change with no
  approved ticket.
- **Real, benchmark-derived histogram bucket boundaries.** Deferred to
  Sprint 5, which owns real benchmarking.
- **Per-backend overrides** for `metrics.listen` or any Sprint 3 tunable —
  global config only, consistent with ADR-0011 decision 10.
- **Alerting rules or Grafana alert configuration.** Dashboard panels only.
- **TLS or authentication on the metrics endpoint.** Explicitly out of
  "production-grade" scope per ADR-0005.
- **Writing ADR-0013 itself** is a prerequisite for implementation (per
  `AGENTS.md` Step 2) but is not a deliverable of this spec — this document
  is the pre-ADR design-session record the eventual ADR-0013 is written
  from.
- **Sprint 4/5 work generally** — hot-reload, connection lifecycle, HTTP/2,
  real benchmark numbers, the design-decisions/what-I'd-do-differently
  docs.

## Further Notes

### This task's design session

This spec is the direct product of a `grill-with-docs` session (`grilling` +
`domain-modeling`) spanning four rounds of questions, following an initial
orientation pass that found no trace of an earlier, similarly-scoped design
session referenced only in stale cross-session memory (no ADR-0013/0014, no
prior spec file, and no matching commits existed in this repo — the design
in this spec was produced fresh, verified directly against the current code
rather than assumed from that memory). Two points were resolved by reading
the actual implementation rather than reasoning abstractly: `ServeHTTP`'s
`start := time.Now()` was confirmed to already sit before `Select()` (making
the whole-request hook a non-issue, not a new mechanism to invent), and the
Half-Open-promotion-logging gap was corrected mid-session from "delayed
until the next `Allow()` call" to "permanently unlogged in the common case"
after walking through what a won-vs-lost CAS race actually leaves each
caller able to observe.

### Ticket sequencing

- **S3.T4 — Metrics package**, no blockers (parallel with S3.T5). Fully
  self-contained: new package, new config field, new exposition seam, all
  testable via `metrics.Collector`'s own method API without touching any
  other package.
- **S3.T5 — Logger**, no blockers (parallel with S3.T4). Owns the
  `CircuitTransition` `Backend` API addition and the active-checker `==`
  gate — both needed to solve S3.T5's own logging problem, and both then
  reused as-is by S3.T6.
- **S3.T6 — Integration**, blocked by S3.T4 (needs the `Collector` to push
  into) and S3.T5 (needs the edge-triggering signals — `CircuitTransition`,
  the `==` gate, the `ejected` guard — S3.T5 already introduces; reusing
  them rather than writing a second, parallel detection path is the whole
  point of this sequencing).
- **S3.T7 — Grafana dashboard + demo stack**, blocked by S3.T4 (the
  instrument set must be final to build correct panels) and S3.T6 (the
  "demonstrable, not just declared" requirement needs real integration
  producing real data for the stack to show).

This mirrors the S3.T1–T3 spec's prefactor pattern (small, independently
verifiable pieces unblocking parallel work) without introducing new
prefactor ticket IDs: S3.T5's own scope already includes the mechanism
S3.T6 needs, so no separate `S3.T*.0`-style ticket is warranted here.

### `CONTEXT.md`

No new entries. This session's vocabulary (`event`, `reason`, metric names,
`CircuitTransition`) is implementation/logging vocabulary documented in
`internal/logger/doc.go` and `internal/metrics/doc.go`, not the
algorithm/routing domain vocabulary `CONTEXT.md` scopes itself to.
