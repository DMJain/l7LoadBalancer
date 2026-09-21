# Spec: S3.T6.5 + S3.T8–T9 — Reinstatement Gate Fix and Chaos Tests

Status: ready-for-agent

---

## Problem Statement

`MILESTONES.md`'s Sprint 3 exit criteria demand a `docker stop`'d backend
be ejected within a health threshold and routing recover automatically once
it's restarted, and demand that repeated 5xx injection eventually open a
backend's circuit. `PROGRESS.md` today declares Sprint 3 "complete" after
S3.T7, but neither of those exit criteria is actually exercised end-to-end:
the only chaos-adjacent coverage in the tree is a unit-scope proxy test
(`TestProxyCircuitOpensOnRepeated5xxAndStopsRouting`) plus a passive-ejection
proxy test — both catch wiring regressions in milliseconds, but neither
boots the active health checker's goroutine, exercises the circuit's
cooldown → half-open trial → close/reopen state machine over real (shortened)
time, or verifies that the Sprint-3 observability layer (gauges + transition
log lines) actually moves at the right instants under a real chaos scenario.

During the grilling that produced this spec, one further defect surfaced: in
the passive-eject → active-recover scenario ADR-0011 decision 3 codified,
the active checker's reinstatement gate compares its consecutive-success
accumulator with `==` rather than `>=`, so a passively-ejected backend whose
accumulator has already crossed `M` (e.g. because passive detection ejected
under load while active probes were still succeeding in the background)
never fires `MarkHealthy`. `lb_backend_healthy` stays at `0`, and
`IsHealthy()` stays at `false`, until the accumulator wraps or the process
restarts. This is a shipped-code drift from the stated decision — not a new
design decision, a bug fix on an existing one.

## Solution

Three tickets ship together as one bundle:

1. **S3.T6.5 — Active checker reinstatement gate: `==` → `>=`.** One-line
   behavioural fix in the active checker with a targeted unit test at the
   checker layer, plus an amendment section appended to
   [ADR-0011](../../docs/adr/0011-health-passive-outlier-and-circuit-breaker-composition.md)
   dating the drift and the fix. Ships first, standalone, so T8's gauge
   assertion in the combined-signal arc becomes the verification of the
   fix rather than a test that flushes the bug out and gets amended
   mid-ticket.

2. **S3.T8 — Chaos test: backend eviction and recovery.** Owns the
   `Backend.healthy` axis: both the active checker path (backend goes
   silent, ECONNREFUSED, active probes drive `MarkUnhealthy`; then
   restart, active probes drive `MarkHealthy`) and the combined-signal
   path (backend returns 500, passive detection and active checker both
   eject on the same signal, then a flip back to 200 drives active
   reinstatement — while the circuit stays tripped, proving the two-gate
   orthogonality ADR-0011 decision 1 asserts).

3. **S3.T9 — Chaos test: circuit breaker trip and half-open recovery.**
   Owns the circuit-gate axis: trip via repeated 5xx crossing
   `circuitFailuresBeforeOpen`, wait through cooldown, half-open trial
   admitted on the first request after cooldown, both resolutions
   exercised (trial success closes; trial failure reopens), plus a
   zero-traffic-during-cooldown assertion that no premature promotion
   ever fires without a real request driving the lazy read-time check
   ADR-0011 decision 6 specified.

Both chaos tickets ship a **hybrid harness**: Go integration tests in a new
`test/chaos/` package own the correctness + timing + observability
assertions and run under `make test`/`make test-race`; a documented,
shell-scripted docker-compose smoke per ticket closes MILESTONES.md's
literal "`docker stop` a backend" wording (`[MANUAL VERIFICATION PENDING]`
footnote per S3.T7 precedent).

After this bundle lands, Sprint 3 is genuinely complete against every
exit criterion in MILESTONES.md, the ADR-0011-declared observability of
the health/circuit state machines is verified moving under a real chaos
scenario, and the reinstatement drift is closed.

## User Stories

**S3.T6.5 — reinstatement gate fix**

1. As a load balancer operator, I want a backend that was ejected by
   passive outlier detection to become healthy again once the active
   checker sees M consecutive successful probes, so backends recover from
   transient upstream misbehaviour without a process restart.
2. As a project maintainer, I want the reinstatement gate to fire whenever
   the consecutive-successes accumulator has *reached* M — not only at the
   exact instant it *equals* M — so an accumulator that already crossed M
   during background probing while passive detection ejected the backend
   under load still drives `MarkHealthy` on the very next probe cycle.
3. As a project maintainer, I want the `lb_backend_healthy` gauge to track
   `Backend.IsHealthy()` at all times, with no scenario in which the gauge
   reads `0` while `IsHealthy()` reads `true`, so the observability layer
   never disagrees with the state it observes.
4. As a project maintainer, I want the drift between ADR-0011 decision 3
   (stated intent) and the shipped code (actual gate) recorded in ADR-0011
   itself as a dated amendment, so the ADR text and the code stay in
   agreement without spawning a new ADR for a one-line correction.

**S3.T8 — backend eviction and recovery chaos test**

5. As a load balancer operator, I want `docker stop backend-a` to remove
   backend-a from the routing pool within a bounded number of active probe
   intervals, so traffic stops hitting a dead backend without manual
   intervention.
6. As a load balancer operator, I want `docker start backend-a` after a
   `docker stop` to reintroduce backend-a to the routing pool within a
   bounded number of active probe intervals, so recovery is automatic and
   requires no operator action.
7. As a load balancer operator, I want a backend that starts returning
   pure 500s to be ejected by both passive detection and the active
   checker on the same signal, so a backend that is up-but-broken is
   removed from rotation as fast as either mechanism can detect it.
8. As a load balancer operator, I want the `lb_backend_healthy` gauge for
   an evicted backend to be `0` while the backend is unhealthy and `1`
   again once it recovers, so Grafana panels and alert rules can reflect
   the real routing state at all times.
9. As a load balancer operator, I want one `event=health.transition` log
   line per real health transition, with the frozen S3.T5.2 vocabulary in
   the `reason` field, so alerting on log streams is precise and doesn't
   spam duplicates.
10. As a project maintainer, I want the eviction-recovery Go integration
    test to survive `go test -race` under CI's slowest executor, so
    timing-sensitive assertions don't flake under race-detector overhead.
11. As a project maintainer, I want the combined-signal recovery arc to
    assert that `lb_backend_healthy` flips back to `1` **while
    `lb_circuit_state{state="open"}` stays at `1`** on the same backend,
    so the two-gate orthogonality claim of ADR-0011 decision 1 is
    mechanically verified rather than merely documented.
12. As a project maintainer, I want a documented shell smoke script that
    runs the eviction scenario against real docker-compose backends and
    asserts through the Prometheus HTTP API, so MILESTONES.md's literal
    "`docker stop`" wording is closed with a reproducible artifact even
    when the daemon isn't available in this session.

**S3.T9 — circuit trip and half-open recovery chaos test**

13. As a load balancer operator, I want a backend whose responses cross
    the consecutive-failure threshold to have its circuit tripped, so
    dispatching requests to a clearly-broken backend stops immediately.
14. As a load balancer operator, I want a tripped circuit to remain open
    for exactly the configured cooldown duration, so a backend gets a
    real recovery window rather than being retried on every request.
15. As a load balancer operator, I want the first request that arrives
    after cooldown expiry to be treated as a half-open trial, and its
    outcome alone (success → closed, failure → open) to decide the next
    state, so recovery is decisive rather than gradual.
16. As a load balancer operator, I want zero traffic during a cooldown
    window to leave the circuit strictly in the Open state — no premature
    Half-Open promotion — so a backend nothing is currently routing to
    isn't spuriously observed to recover.
17. As a load balancer operator, I want the `lb_circuit_state` gauge to
    reflect the current state (closed, open, half-open) at all times, so
    dashboards show the real gate state rather than a stale value.
18. As a load balancer operator, I want one `event=circuit.transition`
    log line per real transition with the frozen S3.T5.2 vocabulary in
    the `reason` field, so log alerts on circuit trips and closes are
    unambiguous.
19. As a project maintainer, I want the half-open-trial-fails-reopens arc
    tested alongside the happy-path close, so both branches of the
    resolution transition ADR-0011 decision 6 specifies are exercised.
20. As a project maintainer, I want a documented shell smoke script that
    runs the circuit trip scenario against a real docker-compose backend
    (via `FAIL_RATE=1` injection) and asserts through the Prometheus HTTP
    API, so MILESTONES.md's literal chaos-test wording is closed with a
    reproducible artifact.

**Cross-cutting**

21. As a project maintainer, I want the existing unit-level chaos coverage
    (`TestProxyCircuitOpensOnRepeated5xxAndStopsRouting` and the
    passive-ejection proxy test) to survive this bundle untouched, so the
    fast unit-suite regression signal is preserved.
22. As a future agent session, I want the design conversation, the
    prerequisite fix, and the two chaos tests to live in one scratch
    directory with a shared spec, so the causal narrative — "design
    surfaced a prerequisite bug, prerequisite shipped first, then the
    chaos tests landed" — is self-evident without reconstructing commit
    timestamps.
23. As a Sprint-4-first-agent-session, I want `PROGRESS.md` to reflect
    that Sprint 3 is in progress until T9's completion commit flips it,
    so the strategic contract in MILESTONES.md isn't misrepresented by
    a premature "complete" status.

## Implementation Decisions

### Bundle sequencing

- **S3.T6.5 ships first**, alone, before either chaos ticket opens. Its
  completion is the prerequisite that lets T8's combined-signal recovery
  assertion pass without special-casing.
- **Sprint 3 status wording** in PROGRESS.md updates to "Sprint 3 in
  progress — T1–T7 done, T8–T9 chaos tests remaining" the moment S3.T6.5
  is claimed. Flips to "Sprint 3 complete" inline on T9's completion
  commit. No pre-scoped S3.T10 close-out ticket; added lazily only if
  T8/T9 implementation surfaces a deviation.

### S3.T6.5 fix

- The behavioural change is confined to the active checker's
  reinstatement gate: the consecutive-successes accumulator is compared
  with `>=` against `M`, not `==`.
- The ADR-0011 amendment is a **new "Amendment" section** appended after
  "Alternatives considered," dated 2026-09-22. One paragraph: the scenario
  that exposes the bug (passive eject → accumulator crosses `M` in
  background probes → `==` gate misses), the one-line fix, no tradeoff
  discussion. ADR-0006-style — a decision that stayed correct, an
  implementation that drifted, corrected in-place. **Not** a new ADR-0014.

### Chaos test harness

- New **`test/chaos/`** package at repo root, files in **`package
  chaos_test`** (external test package), so its import graph is exactly a
  consumer's and the acyclic guarantee stays visible. No prod code
  outside `internal/health` is edited by either chaos ticket.
- A **local `assemble(t, cfg)`** helper duplicates `main.go`'s wiring
  (registry + factory + proxy + `health.Checker` + outlier + `circuit.Breaker`
  + `metrics.Collector` + observer registration + `Registry.SetCircuitGate`).
  No `main.go` refactor into `Run(ctx, cfg)` in this bundle — that seam
  belongs to Sprint 4's SIGHUP reload work. Convergence note in the
  session log so Sprint 4's first orient step sees it.
- **Timing config** for the fast test path: `probeInterval = 20ms`,
  `probeTimeout = 100ms`, `circuitCooldown = 200ms`. Thresholds
  (consecutive-success/failure for active, sliding-window size and
  failure count for passive, `circuitFailuresBeforeOpen`) stay at their
  compile-time constants — the fast path lives entirely in the
  config-driven knobs.
- **All timed assertions use `require.Eventually`** with deadlines ≥ 2s,
  never `time.Sleep(exact)`. Deadlines are generous enough to absorb
  `-race` overhead on the slowest CI executor: where the arithmetic says
  "reinstatement completes in ~100ms wall-clock," the `require.Eventually`
  deadline is 2s+.

### Failure injection helper (`flippableBackend`)

- One shared helper in `test/chaos/helpers_test.go` under `package
  chaos_test`. Methods: `Serve200()`, `Serve500()`, `Kill()`, `Restart()`,
  `URL() string`.
- **`Kill()`** closes the underlying listener, so a subsequent probe or
  proxy dispatch gets `ECONNREFUSED` — the real "backend process died"
  failure mode T8 arc (i) needs. It is not a handler swap to HTTP 503.
- **`Restart()`** reopens the listener on the same URL.
- **`Serve200()` / `Serve500()`** swap the served handler. Because the
  handler is invoked from health-checker and proxy goroutines while the
  test goroutine flips state, the handler field is an
  **`atomic.Pointer[http.Handler]`** (or equivalent) so the swap
  serializes correctly without a mutex around every request. This is the
  race-detector surface the helper was designed to keep clean.

### Observability assertion contracts

- **Gauge assertions** use `prometheus/client_golang/prometheus/testutil.ToFloat64`
  against the `metrics.Collector`'s registered gauges. S3.T6.3 precedent.
  Every backend is looked up by its `backend` label; circuit-state
  assertions include the `state` label.
- **Log assertions** use a captured `slog.Handler` at logger construction
  time. A small helper — `assertTransitionLogged(t, captured, event,
  backend, reason)` — decodes each `slog.Record`'s attributes and
  performs a **field-allowlist match** on exactly `event`, `backend`,
  and `reason`. Timestamps, request IDs, latencies, and any other
  volatile fields are ignored. **No** substring matching, **no** full
  record-equality, **no** regex.
- The `reason` string is matched against the exact frozen vocabulary
  strings from S3.T5.2, so vocabulary drift is caught by these tests
  from the moment they ship.

### Test arcs (locked)

**S3.T8 arc (i) — active-only kill/restart**

- Baseline: three healthy `flippableBackend`s all `Serve200()`;
  `lb_backend_healthy{backend=X}=1` for each; no `health.transition`
  log lines yet.
- `Kill()` on backend X → within `require.Eventually` deadline: gauge
  flips to `0`; exactly one `event=health.transition, backend=X, reason=<vocab>`
  log line; selector no longer chooses X for a burst of requests.
- `Restart()` on backend X → within deadline: gauge flips to `1`;
  exactly one reinstatement log line; selector resumes choosing X.

**S3.T8 arc (iii) — combined 500 signal, health-only recovery**

- Baseline as above.
- `Serve500()` on backend X → within deadline: `lb_backend_healthy=0`,
  `lb_circuit_state{state="open"}=1`; one health transition log, one
  circuit transition log.
- `Serve200()` on backend X → assert only the health-bit axis
  recovers: gauge → `1`, one reinstatement log. **Also assert** that
  `lb_circuit_state{state="open"}` stays at `1` (cross-gate
  independence per ADR-0011 decision 1). Do **not** assert HTTP-traffic
  restoration — that is T9's axis.

**S3.T9 arc (iv) — happy path**

- Baseline: backend X healthy, `lb_circuit_state{state="closed"}=1`.
- `Serve500()`, blast at least `circuitFailuresBeforeOpen` requests →
  circuit opens; assert `lb_circuit_state{state="open"}=1`, one
  transition log.
- One request during cooldown → 503 with the `Registry.Allow` denial
  path (no `IncActive` accounting).
- Wait through `circuitCooldown`, then `Serve200()`.
- Next single request is admitted as the half-open trial → 200 → circuit
  closes; assert `lb_circuit_state{state="closed"}=1`, transition log
  lines fire for open→half-open and half-open→closed with the frozen
  reason strings.

**S3.T9 arc (v) — trial failure reopens**

- Setup through Open as in (iv), but leave the backend at `Serve500()`
  through cooldown.
- Wait through `circuitCooldown`, send one request → half-open trial
  admitted → 500 → circuit back to Open.
- Assert: no `state="closed"` transition log; gauge back to
  `state="open"=1`.

**S3.T9 arc (vi) — zero traffic during cooldown**

- Trip the circuit as in (iv).
- During `circuitCooldown`, send **no** requests. Use `require.Never`
  (or equivalent bounded-time non-occurrence assertion) to confirm no
  half-open transition log fires and the gauge stays at
  `state="open"=1`.
- After cooldown + margin, send one request → the transition fires
  **on that request**, mechanically proving the "lazy, timer-free"
  property of ADR-0011 decision 6.

### Docker-compose smokes

- New directory `deployments/docker/chaos/` with:
  - `eviction.sh` (S3.T8), `circuit.sh` (S3.T9), and a shared `README.md`.
- Each script starts with `set -euo pipefail`, passes `bash -n` and
  `shellcheck` — the mechanical `[DONE]` bar for the shell half.
- LB runs as a host process (`go build -o ./l7-lb ./cmd/l7LoadBalancer
  && ./l7-lb -config …`); backends come from the existing S1.T9 compose
  at `deployments/docker/docker-compose.yml` (`docker compose -f
  deployments/docker/docker-compose.yml up -d`). No new compose file, no
  containerized LB — S3.T10's Dockerfile will optionally upgrade this
  later.
- Assertions in each smoke go through the LB's Prometheus HTTP API on
  `metrics.listen` (default `:9090`) — S3.T7 precedent — not by eyeballing
  Grafana. Each script's README section spells out the exact query and
  the expected result.
- **`eviction.sh`** exercises `docker stop backend-a` → observed
  `lb_backend_healthy{backend="backend-a"}=0` via Prometheus query →
  `docker start backend-a` → observed gauge back to `1`.
- **`circuit.sh`** flips a backend's `FAIL_RATE` to `1` (via `docker exec`
  environment override or a `docker compose up -d` restart with the env
  override), blasts requests until Prometheus reports
  `lb_circuit_state{state="open"}=1`, restores the backend, and asserts
  eventual recovery to `state="closed"=1`.
- End-to-end `docker compose up` execution is **`[MANUAL VERIFICATION
  PENDING]`** in the session log per S3.T7 precedent.

### Not touched by this bundle

- `internal/proxy`'s existing `TestProxyCircuitOpensOnRepeated5xxAndStopsRouting`
  and passive-ejection unit tests stay untouched.
- `main.go` is not refactored into `Run(ctx, cfg)`.
- The `deployments/docker/dummy-backend/` program is not extended with
  new toggles.
- No new ADR beyond the ADR-0011 amendment section.
- `CONTEXT.md` is not edited — every term used (eviction, trip, half-open
  trial, reinstatement gate, orthogonal gates, cross-gate independence)
  already carries the intended meaning in AGENTS.md, ADR-0011, and
  ADR-0012.

## Testing Decisions

**What makes a good test here.** Every assertion is over externally
observable behaviour: HTTP response codes at the proxy boundary, gauge
values through the Prometheus `testutil` API, and structured slog
records through a captured handler. No test reaches into unexported
fields of `Backend`, `health.Checker`, or `circuit.Breaker`, and no
test relies on wall-clock sleeps for correctness — all timed assertions
go through `require.Eventually` with `-race`-generous deadlines. The
`flippableBackend` helper's methods are the only mutation surface: no
test peeks at its internal handler pointer.

**Modules under test.** The Go integration tests live in `test/chaos/`
(`package chaos_test`) and cover the *composition* of `internal/backend`,
`internal/balancer`, `internal/proxy`, `internal/health`, `internal/circuit`,
`internal/metrics`, and `internal/logger` as `main.go` wires them. The
S3.T6.5 unit test lives in `internal/health/checker_test.go` and covers
the active-checker reinstatement gate in isolation — no proxy, no
metrics collector, no observer fan-out.

**Prior art in the codebase.**
- S3.T6.3's gauge assertions using `prometheus/client_golang/prometheus/testutil.ToFloat64`
  set the pattern for reading gauge values in-process (see
  `internal/backend`/`internal/health` metrics tests).
- `internal/logger`'s existing `slog` tests and S3.T5.2's frozen
  event/reason vocabulary set the shape of the structured-field
  assertion helper.
- `internal/proxy/proxy_test.go`'s existing chaos-adjacent tests
  (`TestProxyCircuitOpensOnRepeated5xxAndStopsRouting`, passive-ejection
  under 5xx) prove the correctness of the seams at unit scope; the
  chaos-package tests layer on top rather than duplicating.
- S3.T7's `smoke.sh` is the shape template for both `eviction.sh` and
  `circuit.sh` — the same `set -euo pipefail` header, the same
  Prometheus HTTP API query pattern for assertions, the same
  README-documented "what success looks like" contract.
- S3.T3's tests use `50ms` cooldown / `200ms` sleep as the demonstrated-
  working timing shape; the chaos tests hold to those values in shape
  but replace fixed sleeps with `require.Eventually`.

**S3.T6.5 test setup precisely.** A backend is seeded with `healthy =
false` (simulating a prior passive-detection ejection). The active
checker's probe loop is driven against a `flippableBackend`-style server
returning 200, for at least `M` consecutive successful cycles. The
assertions are: `Backend.IsHealthy()` reads `true` after the `M`th
cycle, exactly one `event=health.transition` slog record fires with the
correct frozen `reason` string, and the accumulator's post-transition
state does not stall further reinstatements. No metrics collector, no
gauge — the fix is at the checker layer, and S3.T6.3 already verifies
the observer→gauge wiring below it.

## Out of Scope

- Containerizing the LB (multi-stage Dockerfile — this is Sprint 3's
  proposed S3.T10, not part of this bundle).
- Refactoring `main.go` into a `Run(ctx, cfg)` seam — belongs to Sprint 4's
  SIGHUP reload work.
- Extending `deployments/docker/dummy-backend/` with a probe-only path
  or a UA-sniff toggle to isolate passive-only ejection from active
  checking. ADR-0011 decision 11 explicitly forbids a `health_path`
  field; a UA sniff would be test-only infrastructure in production
  code. The combined-signal arc (iii) already exercises the passive
  path.
- Migrating the existing unit-level chaos tests in
  `internal/proxy/proxy_test.go` into the new chaos package.
- A per-backend, per-parameter chaos-config surface — every timing knob
  the chaos tests use is either a global `Config` field per ADR-0011
  decision 10 or a compile-time constant.
- Full-system "both gates recovered, HTTP traffic flows again"
  end-to-end assertion in either Go integration test — that story
  belongs to the docker-compose smoke, not to the ticket-scoped Go
  tests. In-process, T8 asserts health recovery only; T9 asserts
  circuit recovery only.
- Any ADR beyond the ADR-0011 amendment section. The circuit gate's
  contract (ADR-0012) and the observability contract (ADR-0013) are
  unchanged.
- A Sprint 3 close-out ticket (S3.T10 style) — added lazily only if T8/T9
  implementation surfaces a real deviation.

## Further Notes

- **Sprint 4 convergence.** The local `assemble(t, cfg)` helper in
  `test/chaos/` duplicates roughly twenty lines of `main.go`'s wiring.
  When Sprint 4 introduces a programmatic `Run(ctx, cfg)` seam (needed
  for S4.T3 SIGHUP orchestration and the S4 zero-drop reload test),
  `assemble()` either converges with that seam or becomes dead code.
  The session log for this bundle records the pointer so the next
  orient step sees it.
- **Narrative handoff between T8 and T9.** T8 arc (iii) leaves the
  circuit in the Open state with health recovered. That state is the
  natural entry to T9's arcs — but T9 does not depend on T8 running
  first; each ticket sets up its own baseline in `assemble()` and each
  test is independent. The narrative is documentation-level, not a test
  ordering constraint.
- **`[MANUAL VERIFICATION PENDING]` posture.** Both shell smokes ship
  as documented + scripted, matching S3.T7's precedent. `[DONE]` on the
  shell half is gated on the script existing, passing `bash -n` and
  `shellcheck`, and its README specifying the exact Prometheus HTTP API
  queries and expected values. Actual `docker compose up` execution is
  deferred to the owner when Docker daemon is available.
- **The three issue files under `issues/`** carry the ticket-scoped
  work-plan checkboxes:
  - `01-s3-t6-5-reinstatement-fix.md`
  - `02-s3-t8-eviction-chaos.md`
  - `03-s3-t9-circuit-chaos.md`
