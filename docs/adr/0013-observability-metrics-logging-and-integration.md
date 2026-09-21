# ADR-0013: Observability — metrics collector, transition logging, and their integration

- **Status**: Accepted
- **Date**: 2026-09-20
- **Deciders**: Darshan Jain (project owner) + opencode agent (S3.T4, design session recorded in `.scratch/s3-t4-t7-observability/spec.md`)

## Context

Sprint 3's resilience subsystems (active health checks, passive outlier
detection, the per-backend circuit breaker) are implemented and enforce
failure isolation, but they are invisible from outside the process:
`internal/metrics` is a stub that only reserves metric names, `internal/logger`
has no transition vocabulary, and neither `internal/health` nor
`internal/circuit` emits a single `slog` line. `MILESTONES.md`'s Sprint 3 exit
criteria ("Grafana shows per-backend latency histograms and circuit state",
"chaos test: injecting 500s on one backend eventually opens its circuit")
describe observable outcomes that nothing currently produces.

The frozen package dependency graph makes `internal/metrics` a leaf with no
internal imports, so observability cannot be installed by having `metrics` read
`Backend` state; the traffic-path packages must push into it. Two further
constraints shape the design: the existing `RoundTripObserver` fan-out only
fires around an actual backend round trip, so it misses the two 503 short
circuits (no healthy backend; circuit-denied) entirely; and the circuit
breaker's lazy, read-triggered Half-Open promotion (ADR-0011/ADR-0012) has no
single call site that always observes the promotion — the same CAS can be won
by a `Registry.Selectable()` scan, which has no logger.

This ADR records the decisions for the whole observability workstream
(S3.T4–T7) before implementation begins, per `AGENTS.md` Step 2. It is written
from the design-session record in the spec above.

## Decision

### Metrics package (`internal/metrics`)

1. **`internal/metrics` stays a true leaf: a push-only `Collector`.** Other
   packages call exported methods (`ObserveRequest`, `SetBackendHealthy`,
   `SetCircuitState`, `IncActiveConnections`/`DecActiveConnections`, etc.);
   `metrics` never reads `backend`/`config`/`proxy` state and adds no internal
   import. This preserves the frozen dependency graph in `AGENTS.md` unchanged.

2. **The `Collector` owns its own private `prometheus.NewRegistry()`, never
   `prometheus.DefaultRegisterer`.** A collector is a value an owner constructs
   and hands around, with no package-level `sync.Once` and no global mutable
   state, so tests construct independent instances, run `t.Parallel()`, and
   assert through `prometheus/testutil` without cross-test collisions. The
   `promhttp` handler is built from that same private registry.

3. **`lb_requests_total` and `lb_request_duration_seconds` are driven from one
   whole-request hook in `proxy.ServeHTTP`, at the existing `start :=
   time.Now()` / deferred `logRequest` location.** The duration is the whole
   client-facing request — selection, circuit check, and backend round trip
   together — which is the operator's conventional meaning. `RoundTripObserver`'s
   own duration measurement stays purely internal (feeding P2C-EWMA, passive
   detection, and the circuit), so there is exactly one definition of "request
   duration" in the metrics surface. Labels are exactly `backend`, `method`,
   `status_class` — never `status_code` (unbounded cardinality).

4. **The histogram buckets are `doc.go`'s provisional set** (`.005, .01, .025,
   .05, .1, .25, .5, 1, 2.5, 5, 10` seconds), annotated as provisional pending
   Sprint 5's real benchmark data rather than invented numbers.

5. **`lb_circuit_state` is a label-enum gauge (`backend`, `state` ∈
   `closed`/`open`/`half_open`), and its setter unconditionally writes all
   three series on every call** — target state `1`, the other two `0`. A
   numeric-encoded single gauge was rejected: it needs an encoding table kept in
   sync with `internal/circuit`'s state type and is not directly queryable by
   state name. The unconditional zeroing is a correctness requirement on the
   setter itself, so a later caller that only sets the new state cannot silently
   break the exactly-one-state-is-`1` invariant.

6. **`lb_backend_healthy` is a plain per-backend 0/1 gauge**, carrying none of
   the enum-mutual-exclusion concern of `lb_circuit_state`.

7. **A new `lb_active_connections` gauge (label `backend`) is added,
   amending the metric-name reservations in `internal/metrics/doc.go` and
   `docs/design/sprint-1-contracts.md`.** This fills an acknowledged
   `MILESTONES.md` gap (Sprint 3's deliverable list names an active-connections
   gauge); it does not reverse a locked decision, so it is recorded here rather
   than in a separate ADR. It is incremented/decremented at the exact call sites
   `proxy` already uses for `Backend.IncActive()`/`DecActive()`, so the gauge
   cannot drift from the real count.

8. **`Config` gains a `metrics.listen` field (always-on, default `:9090` when
   omitted), served by `promhttp.HandlerFor` on its own `http.Server` separate
   from client traffic, started and shut down in `main.go` on the existing
   `sigCtx`.** It follows S3.T0.3's nil-means-omitted pointer pattern and
   `KnownFields(true)` strictness; there is no disable toggle, matching the
   `health:`/`circuit:` precedent of no per-subsystem disable switch.

9. **Startup seeding of every backend's initial gauge values
   (`healthy=1`, `state=closed`, `active_connections=0`) goes through the
   `Collector`'s own setter methods** — `SetBackendHealthy`,
   `SetCircuitState`, and `SetActiveConnections(backend, 0)` — not a separate
   seeding code path, because Prometheus `Vec` metrics materialize no series
   until first written and a never-degraded system must render a complete
   dashboard on its first scrape. Where a gauge has a transition method that
   is already a setter (healthy, circuit) seeding calls exactly that method;
   the active-connections gauge's real transitions use
   `IncActiveConnections`/`DecActiveConnections`, so seeding its initial `0`
   uses `SetActiveConnections`. (Implemented in S3.T6; recorded here as the
   design.)

### Transition logging

10. **`backend.CircuitTransition`
    (`NoChange`/`Opened`/`Closed`/`HalfOpened`/`Reopened`) is returned by
    `Backend.CircuitFailure`, `CircuitSuccess`, and `CircuitAllow` alongside
    their existing return values.** It is a plain exported type in
    `internal/backend`, so a caller can tell whether *this* call changed the
    circuit's state, and `internal/backend` gains no logging dependency.
    `internal/circuit.Breaker` (`circuit.New` gains a `*slog.Logger`) logs at
    its `ObserveRoundTrip`/`Allow` call sites whenever the transition is not
    `NoChange`. The `backend.CircuitGate` and `proxy.RoundTripObserver`
    interfaces `Breaker` implements are untouched — those wrapper methods
    absorb the extra return value internally.

    `Reopened` (added when this decision was implemented, see Amendment below)
    distinguishes a Half-Open trial failure (`HalfOpen→Open`) from the
    `Opened` (`Closed→Open`) case. Both move the circuit to Open, but they map
    to different log reasons (`trial_failure` vs `consecutive_failures`), and a
    four-value enum carrying only the resulting state cannot tell them apart.
    One value per logged reason keeps the mapping bijective.

    Transitions: `Opened` = `Closed→Open` (consecutive failures reached the
    threshold), `Closed` = `HalfOpen→Closed` (trial success), `HalfOpened` =
    `Open→HalfOpen` (cooldown elapsed), `Reopened` = `HalfOpen→Open` (trial
    failure), `NoChange` = the call changed nothing.

11. **`internal/health`'s active checker emits its transition line behind an
    equality gate (`counter == threshold`), not the `>=` gate the `Mark*` calls
    use**, reusing the counters `prober` already owns and adding no field — so a
    sustained failure or success streak logs once per genuine transition, not
    once per probe. The passive `OutlierDetector` logs at its existing
    `ejected`-per-episode guard, which already makes `MarkUnhealthy` fire once
    per episode. Both packages gain a `*slog.Logger` via their existing
    constructors.

12. **Two new canonical log fields, `event` and `reason`, are added to the
    frozen vocabulary in `internal/logger/doc.go`.** Both are closed, snake_case
    Go-constant vocabularies (matching the algorithm-identifier convention).
    `event` is exactly `health_ejected`, `health_reinstated`,
    `circuit_opened`, `circuit_closed`, `circuit_half_opened`; `reason` is
    exactly `probe_failures`/`probe_recovered` (active), `outlier_window`
    (passive), and `consecutive_failures`/`trial_success`/`trial_failure`/
    `cooldown_elapsed` (circuit). `health_ejected` and `circuit_opened` (via
    either `consecutive_failures` or `trial_failure`) are logged at WARN;
    `health_reinstated`, `circuit_closed`, and `circuit_half_opened` at INFO,
    matching the existing 5xx-is-WARN convention. A Half-Open trial admission
    is a routine lifecycle event, so it is INFO.

13. **Known, permanent gap: a Half-Open promotion whose CAS is won by
    `Registry.Selectable()`'s read path is never logged.** `Backend.CircuitOpen()`
    has no path to a logger without a cross-package plumbing change out of
    scope. This is recorded explicitly as an accepted limitation (the same shape
    as ADR-0012's stale-while-half-open note), not left implicit. Remediating it
    is not approved this sprint.

### Integration (`proxy`, `health`, `circuit`)

14. **The whole-request metrics hook reuses the existing `ServeHTTP` timer and
    deferred-hook location; no new fan-out interface is introduced.** `metrics`
    is the hook's only consumer (unlike `RoundTripObserver`'s three), so the
    proxy holds a `*metrics.Collector` (or a small consumer-defined interface if
    a test seam is wanted) rather than a `RequestObserver` abstraction with one
    implementation. A request that finds no healthy backend is counted with
    `backend=""`; a circuit-denied request is counted with the real backend
    label `Select()` already identified — the two are never conflated.

15. **`health.Checker`, `health.OutlierDetector`, and `circuit.Breaker` each
    receive a `*metrics.Collector` alongside the `*slog.Logger`, via their
    existing constructors.** Gauge updates happen at the exact edge-triggered
    call sites the log lines use (the `CircuitTransition != NoChange` check, the
    `== threshold` gate, the `ejected`-per-episode guard), so a log line and a
    gauge for the same transition can never disagree.

16. **`internal/balancer` requires zero changes and zero new imports.** A
    balancer-level selection failure (`ErrNoHealthyBackends`) is visible to the
    proxy — the whole-request hook's owner — so "metrics reflect balancer
    failures" is satisfied without `balancer` importing `metrics`.

### Grafana dashboard and demo stack (`deployments/docker/observability/`)

17. **A new docker-compose stack (Prometheus + Grafana with datasource and
    dashboard provisioning) is kept entirely separate from S1.T9's
    dummy-backends compose and Sprint 5's future bench compose.** One dashboard
    JSON covers exactly the instrumented set — five panels: request rate by
    `backend`/`status_class`, latency p50/p99 by `backend`, circuit state (the
    label-enum read directly), backend healthy, and active connections.
    Prometheus scrapes the load balancer's `metrics.listen` port at a
    reasonable default interval (15s). No alerting rules, no TLS/auth on the
    endpoint (the latter out of scope per ADR-0005).

## Consequences

- Positive: the frozen package graph is unchanged; `metrics` stays a leaf and
  `balancer` stays untouched, so this workstream adds no cycle risk.
- Positive: one definition of request duration (whole-request), one detection
  signal per transition shared by the log line and the gauge, and one seeding
  code path — each removes a class of drift.
- Positive: private registries make the collector trivially testable in
  isolation and `t.Parallel()`-safe.
- Negative: `Backend`, `circuit.New`, and the `health` constructors each grow a
  parameter or return value (API churn that is additive, not breaking).
- Negative: metric and log values are closed Go vocabularies, so adding a
  transition requires editing the vocabulary constants in two places
  (`internal/logger/doc.go` and its producer) rather than logging a free string.
- Neutral: `lb_active_connections` amends a reservation rather than reversing
  a decision; the reservation doc is updated in the same change.
- Negative (known, permanent): Half-Open promotions won by a `Selectable()`
  scan are never logged (decision 13).

## Alternatives considered

- **Let `internal/metrics` import `backend` and scrape state itself:**
  rejected — breaks the frozen leaf rule and inverts the push direction the
  package graph requires.
- **Use `prometheus.DefaultRegisterer`:** rejected — process-global mutable
  state makes independent, parallel test instances collide.
- **A numeric-encoded `lb_circuit_state` gauge:** rejected in decision 5 — an
  encoding table to maintain and not queryable by state name.
- **A `RequestObserver` fan-out mirroring `RoundTripObserver`:** rejected in
  decision 14 — an abstraction with one real implementation, and no second
  consumer in sight.
- **Exposing `RoundTripObserver`'s backend-leg duration as the latency
  histogram:** rejected in decision 3 — two definitions of "request duration"
  invites operators to read the wrong one.
- **Having `Backend` import `log/slog` directly to log transitions:** rejected
  in decision 10 — `CircuitTransition` keeps `backend` a plain state holder and
  keeps policy/logging in `circuit`.
- **Fixing the Half-Open-via-`Selectable()` logging gap now:** rejected in
  decision 13 — a cross-package plumbing change with no approved ticket;
  documented as an accepted limitation instead.
- **A metrics disable toggle or per-backend `metrics.listen` overrides:**
  rejected in decision 8 — always-on, global config only, consistent with
  ADR-0011 decision 10.

## Amendment — 2026-09-21: `CircuitTransition` gains a fifth value

**Supersedes the four-value list in decision 10.** Decision 10 originally
named `NoChange`/`Opened`/`Closed`/`HalfOpened`, but decision 12 requires four
distinct circuit log reasons (`consecutive_failures`, `trial_success`,
`trial_failure`, `cooldown_elapsed`). Two of those — `consecutive_failures`
(`Closed→Open`) and `trial_failure` (`HalfOpen→Open`) — both result in an Open
circuit, so a four-value enum carrying only the resulting state cannot tell
`circuit.Breaker` which reason to log. Decision 12's phrase
"`circuit_half_opened`-via-`trial_failure`" was the symptom of this
inconsistency: a failed trial reopens the circuit (`circuit_opened`), it does
not half-open it.

Resolved with the project owner during S3.T5.4 by adding **`Reopened`** for the
`HalfOpen→Open` trial-failure transition, so each logged reason maps 1:1 to a
transition value. This amends decision 10 in place (five values) and corrects
decision 12's WARN/INFO list. The Go constants are exported as
`CircuitNoChange`/`CircuitOpened`/`CircuitReopened`/`CircuitClosed`/
`CircuitHalfOpened` — the `Circuit` prefix avoids generic names like
`backend.Opened`; the bare names in decision 10 are shorthand.

The Half-Open-promotion logging gap in decision 13 is unchanged: a promotion
whose CAS is won by `Registry.Selectable()`'s `Backend.CircuitOpen()` read is
still never logged, because only `CircuitAllow` can report a promotion it
performed and has a logger. `CircuitAllow` reports `CircuitHalfOpened` when
*it* performed the promotion (the promotion CAS has exactly one winner, so
exactly one concurrent caller reports it — tying the report to the promotion
rather than to winning the trial slot keeps the exactly-once guarantee under a
race). `CircuitOpen` continues to return `bool`, not a transition.
