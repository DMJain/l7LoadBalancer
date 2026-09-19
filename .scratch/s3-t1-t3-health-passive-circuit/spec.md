# Spec: S3.T1–T3 — Active Health Checks, Passive Outlier Detection, and Circuit Breaker

Status: ready-for-agent

---

## Problem Statement

Sprint 1 and Sprint 2 shipped four selection algorithms and `Backend.SetHealthy`
(ADR-0006), but nothing in the codebase has ever called `SetHealthy` in
production. A backend that dies, hangs, or starts erroring keeps receiving
traffic indefinitely — the load balancer has no way to notice, react, or
recover. `MILESTONES.md`'s Sprint 3 exit criteria require a `docker stop`'d
backend to be ejected within a health threshold, its circuit to trip, and
routing to resume automatically once it's restarted — none of which is
possible today.

Closing this gap safely is harder than adding one health-check loop: three
separate mechanisms are needed (fast active detection of a dead backend, fast
passive detection of a backend that's up but erroring, and a stricter
per-backend circuit that stops routing to a clearly-broken backend and tests
its own recovery), and — as the design session for this spec found — two of
the three failure modes only appear once you try to make them cooperate
without racing each other or making a circuit's own recovery path
unreachable.

## Solution

Three cooperating subsystems, composed per
[ADR-0011](../../docs/adr/0011-health-passive-outlier-and-circuit-breaker-composition.md):

1. **Active health checks (S3.T1)** — one goroutine per backend, periodic GET
   probes against the backend's existing URL, an N-consecutive-failure /
   M-consecutive-success state machine driving `Backend.MarkHealthy()` /
   `Backend.MarkUnhealthy()` (a split of today's `SetHealthy`, making the
   active/passive recovery asymmetry visible at the call site).
2. **Passive outlier detection (S3.T2)** — a count-based sliding window per
   backend, ejecting (`MarkUnhealthy()`) after N failures (5xx or transport
   failures) within the window, recovering only via the next successful
   active probe. Delivering this requires generalizing the proxy's existing
   single hardcoded `RecordLatency` call at `modifyResponse`/`errorHandler`
   into a small `RoundTripObserver` fan-out, built to hold three listeners
   from the start even though only two are wired until S3.T3 lands.
3. **Circuit breaker (S3.T3)** — a per-backend closed/open/half-open state
   machine, state living directly on `Backend` (mirroring the EWMA-latency
   precedent), gating dispatch via a pre-`IncActive()` `Allow()` check rather
   than removing the backend from selection outright — which is what makes a
   half-open trial reachable at all. `Registry.Healthy()` is renamed to
   `Registry.Selectable()` to reflect that eligibility is now `healthy AND
   circuit-not-open`, landing with this task since that's the first point
   circuit state exists.

After this lands, the load balancer detects a dead or erroring backend
through two independent paths on two different timescales, stops routing to
a backend whose circuit has tripped, and recovers automatically once the
backend proves — via a real probe or a real trial request — that it's
answering again.

## User Stories

**Active health checks (S3.T1)**

1. As a load balancer operator, I want each backend probed periodically and
   independently of live traffic, so a dead backend is detected even with
   zero real requests reaching it.
2. As a load balancer operator, I want a probe to be a plain GET against the
   backend's already-configured URL, with no separate health-path field to
   configure, so backends with no dedicated health endpoint still work.
3. As a project maintainer, I want only a 2xx response to count as a healthy
   probe, with any 3xx treated as a failure, so a backend that starts
   redirecting (misconfigured HTTP→HTTPS, or anything else) isn't marked
   healthy while `httputil.ReverseProxy` forwards that redirect verbatim to
   every real client.
4. As a project maintainer, I want the health checker to use its own
   dedicated `http.Client` with its own timeout, independent of the proxy's
   transport, so probe timeout tuning never couples to live-request
   transport settings.
5. As a project maintainer, I want the consecutive-failure and
   consecutive-success thresholds as Go constants, not config fields,
   matching the "constant, not config" posture already set for ε (ADR-0009)
   and α (ADR-0010).
6. As a project maintainer, I want probe interval and probe timeout as new
   **global** YAML config fields (not per-backend), so operators can tune
   probing cadence per deployment without a rebuild, while today's schema
   stays small — every currently-committed backend is homogeneous.
7. As a project maintainer, I want one goroutine per backend, started after
   the registry is built and stopped via `main.go`'s existing
   `signal.NotifyContext` (SIGINT/SIGTERM), so no second shutdown primitive
   is introduced.
8. As a project maintainer, I want each probe cycle's logic factored as a
   function separate from the goroutine's `for { select }` loop wiring, so
   it's unit-testable without a real ticker or context cancellation.
9. As a project maintainer, I want `Backend.SetHealthy(bool)` split into
   `MarkHealthy()` and `MarkUnhealthy()`, with `MarkHealthy()` reserved by
   convention for the active-check subsystem, so the fact that only active
   checks can prove a backend healthy again (story 14) is visible at the
   call site, not just documented in an ADR.
10. As a load balancer operator, I want a backend I `docker stop` excluded
    from selection within the configured failure threshold, and to resume
    receiving traffic once restarted and answering again — the `MILESTONES.md`
    Sprint 3 exit criterion for active health checks.

**Passive outlier detection + round-trip observer fan-out (S3.T2)**

11. As a load balancer operator, I want a backend returning a burst of 5xx
    responses, or repeatedly refusing connections, ejected without waiting
    for the next scheduled active probe, so an already-broken backend
    doesn't keep receiving traffic for up to a full probe interval.
12. As a project maintainer, I want passive detection's failure signal to be
    the union of 5xx responses **and** `errorHandler` transport failures
    (connection refused, timeout, etc.), so the primary "backend refuses
    connections" failure mode isn't missed on the technicality that it never
    produces an HTTP status code.
13. As a project maintainer, I want the sliding window to be count-based
    (last N outcomes per backend), not time-based, matching this project's
    existing preference for count/consecutive state machines over
    clock-based ones.
14. As a project maintainer, I want a passively-ejected backend to recover
    only via the next successful active probe, with no independent
    timer-based reinstatement in passive detection itself, so there is
    exactly one recovery path rather than two that could disagree about a
    backend's state.
15. As a project maintainer, I want the existing hardcoded
    `state.backend.RecordLatency(...)` call at `modifyResponse`/`errorHandler`
    replaced by a generic `RoundTripObserver` fan-out, registered through an
    additive `Proxy.RegisterObserver(...)` method rather than a change to
    `New(reg, sel)`'s frozen two-argument signature, so a third listener (the
    circuit breaker, S3.T3) can be added later with zero further changes to
    `proxy.go`'s hook logic.
16. As a project maintainer, I want the fan-out interface and mechanism
    designed for three listeners from the start (latency recording,
    passive-outlier, circuit) even though only two are wired until S3.T3
    lands, so it is never hardcoded for "the two callers that currently
    exist" and then need mid-flight refactoring.
17. As a project maintainer, I want a test that proves each registered
    observer is invoked **exactly once** per request per hook path — not
    zero, not twice — asserting invocation count and not merely invocation
    content, so an accidental double-registration in `main.go`'s wiring
    (an easy mistake with three separate registration calls at startup) is
    caught here rather than silently doubling every downstream counter in
    production.
18. As a project maintainer, I want that invocation-count test run against
    **both** terminal hooks — a 5xx-response fixture exercising
    `modifyResponse`, and a connection-refused fixture exercising
    `errorHandler` — so the unconditional-recording guarantee the circuit
    breaker's trial resolution depends on is proven at this tier, not left
    to be discovered later by a Sprint 3 chaos test.
19. As a load balancer operator, I want a backend ejected by passive
    detection to stop receiving traffic immediately on its next selection
    attempt, consistent with how every other health-driven exclusion already
    behaves.

**Circuit breaker (S3.T3)**

20. As a load balancer operator, I want each backend's circuit breaker fully
    independent of every other backend's, so one backend's failures never
    affect routing to a healthy one.
21. As a project maintainer, I want circuit state (closed/open/half-open,
    consecutive-failure count, opened-at timestamp, half-open-trial-in-flight
    flag) to live directly on `Backend` as unexported, method-only,
    atomic/CAS-guarded fields — mirroring the EWMA-latency precedent
    (ADR-0010) — rather than a `circuit`-package-owned map, since `circuit`
    depends on `backend` and never the reverse, and `Backend` cannot import a
    `circuit`-defined type without a cycle.
22. As a project maintainer, I want the circuit's failure counter to be
    consecutive (reset to zero on any success) — distinct from passive
    detection's sliding window — so the two mechanisms are genuinely
    different in kind (circuit: strict and fast; passive: tolerant and slow),
    not the same counting method wearing two thresholds.
23. As a project maintainer, I want the circuit to watch the same underlying
    failure signal as passive detection (5xx responses and `errorHandler`
    transport failures), so `MILESTONES.md`'s chaos-test exit criterion
    ("injecting 500s on one backend eventually opens its circuit") is
    actually satisfiable.
24. As a project maintainer, I want the circuit's consecutive-failure-to-open
    threshold as a Go constant, and its cooldown duration as a new global
    YAML config field, matching the same config/constant split already
    applied to active health checks.
25. As a project maintainer, I want the Open→Half-Open transition to be a
    lazy, timer-free check — `time.Since(openedAt) >= cooldown`, evaluated
    (and CAS-promoted) on read — rather than a background goroutine, so
    `circuit` never needs a goroutine lifecycle of its own, unlike `health`.
26. As a project maintainer, I want the half-open trial slot guarded by a
    real `CompareAndSwap`, not a plain bool, so two requests that land in the
    same instant the circuit becomes eligible for half-open can't both
    believe they are the trial.
27. As a load balancer operator, I want a half-open backend to remain fully
    selectable by every configured algorithm, with the breaker's own
    per-request gate — not pool membership — deciding whether a given
    selected request is admitted as the trial, so the trial is a real
    request rather than a synthetic probe that would duplicate S3.T1's job.
28. As a project maintainer, I want the breaker's `Allow(b)` check run
    immediately after `Select()` returns and before `IncActive()` — the same
    position `ServeHTTP` already uses for the `ErrNoHealthyBackends`
    short-circuit — so a denied request never touches active-connection
    accounting and needs no new decrement path.
29. As a project maintainer, I want `Registry.Healthy()` renamed to
    `Registry.Selectable()`, filtering on `healthy AND circuit-not-open`, so
    the name doesn't quietly mean something broader than what it actually
    filters on now that circuit state exists — landing with this task, since
    the rename's justification doesn't exist before circuit state does.
30. As a load balancer operator, I want a circuit's trial to resolve to
    Closed on a single success and back to Open on a single failure, with no
    additional threshold inside the half-open state, matching the state
    machine `AGENTS.md` already describes.
31. As a load balancer operator, I want injecting 500s on one backend to
    eventually open its circuit and stop routing to it, and recovering that
    backend to eventually close the circuit again — the `MILESTONES.md`
    Sprint 3 exit criterion for the circuit breaker.

## Implementation Decisions

Full rationale for every decision below is recorded in
[ADR-0011](../../docs/adr/0011-health-passive-outlier-and-circuit-breaker-composition.md);
this section translates it into buildable units.

### Active health checks (`internal/health`)

- A new type (name at the implementer's discretion, e.g. `health.Checker`)
  constructed from the registry plus probe interval/timeout, owning one
  goroutine per backend.
- Probe: GET to the backend's configured URL via a dedicated `http.Client`
  (its own timeout, no dependency on `internal/proxy`); 2xx → success,
  anything else (including 3xx) → failure.
- State machine: N consecutive failures → `MarkUnhealthy()`; M consecutive
  successes → `MarkHealthy()`. N and M are unexported Go constants.
- `Config` gains a new global section for probe interval and probe timeout
  (new YAML fields, `KnownFields(true)` strict — `Validate()` needs rules for
  these, e.g. positive durations).
- Goroutines share `main.go`'s existing `sigCtx`; each backend's probe-cycle
  logic is a standalone function, separate from the loop wiring.
- `Backend.SetHealthy(bool)` → `Backend.MarkHealthy()` / `Backend.MarkUnhealthy()`.
  Every existing caller (S1.T8's cross-selector tests) is updated to the new
  names.

### Passive outlier detection + observer fan-out (`internal/health`, `internal/proxy`)

- A new type in `internal/health` (per the package's existing doc comment,
  which already names "passive outlier detection" as this package's job)
  holding a count-based sliding window of recent outcomes per backend.
  Window size and failure-count threshold are unexported Go constants. On
  threshold breach, calls `backend.MarkUnhealthy()`.
- `internal/proxy` gains:
  ```go
  // RoundTripObserver receives every backend round trip's outcome. Called
  // unconditionally — success and failure alike — regardless of the
  // configured selector or the circuit breaker's current state.
  type RoundTripObserver interface {
      ObserveRoundTrip(b *backend.Backend, d time.Duration, success bool)
  }
  ```
  (from the design session, not a prototype) plus an additive
  `Proxy.RegisterObserver(o RoundTripObserver)` method — `New(reg, sel)`'s
  signature is untouched.
- `modifyResponse` computes `success := resp.StatusCode < 500` and
  `d := time.Since(state.dispatchStart)`; `errorHandler` always passes
  `success = false, d = p2cFailurePenalty`. Both then loop over every
  registered observer, unconditionally.
- The existing `RecordLatency` call becomes the first registered observer
  (a small adapter implementing `RoundTripObserver`); the passive-outlier
  detector is the second. `main.go` registers both after constructing the
  `Proxy`.

### Circuit breaker (`internal/backend`, `internal/circuit`, `internal/proxy`)

- Circuit state — a small state enum plus consecutive-failure count,
  opened-at timestamp, and a half-open-trial-in-flight flag — is defined and
  stored **in `internal/backend`** (not `internal/circuit`), as unexported,
  CAS-guarded fields reached only through exported methods (naming at the
  implementer's discretion, but method-only access is not optional — this is
  what keeps `internal/backend` free of any import from `internal/circuit`,
  preserving the acyclic package graph). `Registry.Selectable()` (below)
  lives in the same package and reads this state directly.
- `internal/circuit` holds the *policy*: the consecutive-failure-to-open
  threshold (Go constant) and the cooldown duration (read from `Config`),
  exposing something equivalent to `Allow(b *backend.Backend) bool` and
  implementing `proxy.RoundTripObserver` to record outcomes into `Backend`'s
  state via its methods. `internal/circuit` imports `internal/backend`;
  the reverse never happens.
- Open→Half-Open promotion is evaluated lazily, on read, inside whatever
  method reports circuit state — CAS-transitioning `Open`→`HalfOpen` the
  moment `time.Since(openedAt) >= cooldown` is observed. Both
  `Registry.Selectable()` and `Allow()` go through this same read.
- `ServeHTTP` calls `Allow(b)` immediately after `Select()` returns, before
  `IncActive()`. On denial: respond immediately (503-shaped, with a distinct
  WARN log line following the existing `errorHandler`-style pattern for
  non-happy-path responses), without dispatching or touching active-connection
  accounting.
- `Registry.Healthy()` → `Registry.Selectable()`: filters on
  `b.IsHealthy() && <circuit not open>`. Every selector call site and its
  existing tests are updated to the new name — a mechanical rename, no
  selector logic changes.
- `Config` gains the circuit cooldown duration as a new global field,
  alongside the health-check interval/timeout fields from S3.T1.

## Testing Decisions

### What makes a good test here

Tests exercise external, observable behavior — probe outcomes reaching
`MarkHealthy`/`MarkUnhealthy`, selection results, HTTP status codes and
headers a real client would see, recorded observer invocations — never
internal mechanics (a CAS loop's retry count, for instance, is not asserted
directly). Table-driven where the input space is enumerable; `testify/require`
for setup, `testify/assert` for value checks, matching every existing package.

### Seams

Four seams, confirmed with the project owner before writing this spec. Three
extend existing seams; one is new because active probing has no existing
abstraction to reuse.

1. **`Backend`'s own method API** (existing — the same seam
   `backend_test.go` already uses for `IsHealthy`/`IncActive`/`DecActive`) —
   extended to `MarkHealthy`/`MarkUnhealthy` (S3.T1/T2) and the new
   circuit-state accessors (S3.T3). Direct method calls, no HTTP.
2. **The active-health-checker type, new** — constructed directly against
   `httptest.Server` fixtures (2xx, 5xx, 3xx-redirect, connection-refused),
   with the single-probe-cycle logic called directly rather than through the
   goroutine loop, so no real ticker or context cancellation is needed in
   tests. This fixture set is also where the 2xx-only / 3xx-is-a-failure
   distinction and the 5xx-plus-transport-failure signal definition get
   proven, rather than left implicit in the implementation.
3. **`RoundTripObserver` + `Proxy.RegisterObserver`, layered onto the
   existing `proxy.Proxy` `ServeHTTP` seam** (the `httptest`-backed harness
   from S1.T6/ADR-0007 and S2.T3). A spy observer is registered alongside
   the real ones. Tests here must assert **invocation count**, not just
   invocation content — a passing "content is correct" assertion says
   nothing about whether an observer fired once or twice — and the fixture
   set must include both a 5xx-response backend (`modifyResponse` path) and
   a connection-refused backend (`errorHandler` path), so the fan-out is
   proven at both terminal hooks, not just one.
4. **The passive-outlier counter and the circuit's failure counter, each
   tested directly via their own `ObserveRoundTrip` calls** — no HTTP
   needed to prove "N failures in a window ejects" or "N consecutive
   failures opens," mirroring how S2.T1/T2 tested the ring and selectors
   directly before layering proxy tests on top.

`Registry.Healthy()` → `Selectable()` is **not** its own seam: it is a
mechanical rename of the existing `Registry` test suite (the same seam every
prior selector's health-transition tests already use), confirmed as a
deliberate scoping choice, not an oversight.

### Test groupings and cases

- **Active health checker**: a 2xx-returning fixture reaches
  `MarkHealthy` after M consecutive successes; a 5xx or connection-refused
  fixture reaches `MarkUnhealthy` after N consecutive failures; a
  3xx-redirecting fixture is treated as a failure, not a success; probe
  cycles run against a dedicated `http.Client` independent of any
  `proxy.Proxy` instance.
- **Passive outlier detector**: N failures within the window (mixing 5xx and
  transport-failure outcomes) ejects; failures below the threshold, or
  interspersed with enough successes to fall outside the window, do not
  eject; ejection calls `MarkUnhealthy()` exactly once, not per failure.
- **Observer fan-out**: registering two observers and sending one request
  through a 5xx-fixture backend invokes each exactly once with the correct
  `(backend, duration, success=false)`; the same for a connection-refused
  fixture backend routed through `errorHandler`; a successful 2xx request
  invokes each exactly once with `success=true` and a real, non-zero
  duration.
- **Circuit breaker**: N consecutive failures opens the circuit;
  `Selectable()` excludes an open-circuit backend; a single success from
  Closed resets the consecutive-failure count to zero; after the configured
  cooldown elapses (tested with real sleeps at comfortable margins — see
  below), the circuit is observed as half-open and `Allow()` admits exactly
  one concurrent trial, denying a second concurrent one; a successful trial
  closes the circuit, a failed one reopens it; `Allow()` denying a request
  never increments `ActiveConns`.
- **Circuit cooldown timing**: cooldown and sleep margins use real
  `time.Sleep`, not a fake clock (no precedent for one elsewhere in this
  codebase) — but with headroom well above scheduler-jitter risk on a shared
  CI runner: a cooldown around 50ms and a sleep around 200ms, not the
  10ms/15ms a tight-margin test would use. The goal is a flake that means
  something is actually broken, not scheduler noise.
- **`Registry.Selectable()`**: existing `Healthy()` test cases renamed and
  extended with an open-circuit backend excluded, and a half-open backend
  included.

### Prior art

- `internal/backend/backend_test.go` (S1.T3) — the direct-method seam for
  `Backend`'s existing atomics, including a concurrent `-race` case.
- `internal/balancer`'s selector tests (S1.T4/T5/T8, S2.T1/T2's hot-key
  comparative test, S2.T3's load-skew test) — the pattern of testing a
  component directly before layering proxy tests on top, and of
  demonstrating a distributional property with real recorded numbers.
- `internal/proxy`'s existing test file and ADR-0007 — the
  `httptest`-backed proxy-lifecycle seam, and the precedent (the
  exactly-once-decrement guarantee) for testing request-lifecycle wiring
  directly rather than inferring it from component-level tests alone.

## Out of Scope

- **Prometheus metrics, the circuit-state gauge, the Grafana dashboard** —
  `MILESTONES.md` lists these as Sprint 3 deliverables, but this spec covers
  only S3.T1–T3 as scoped in the design session; metrics land as separate,
  later Sprint 3 tasks.
- **Per-backend overrides** for probe interval/timeout or circuit
  cooldown — global config fields only, per ADR-0011 decision 10.
- **A per-backend configurable health-check path** — probes hit the
  backend's existing configured URL; no new schema field for this.
- **A fake/injectable clock abstraction** — circuit cooldown tests use real,
  comfortably-margined sleeps instead.
- **Retry or reselection when `Allow()` denies a request** — the request is
  answered immediately; reselection would grow the `Selector` interface and
  preempt the explicitly-deferred Sprint 4 retry-policy ADR.
- **An enable/disable config toggle** for any of the three subsystems — all
  three ship always-on together (ADR-0011 decision 4).
- **`ResponseHeaderTimeout` / `DialContext` timeout tuning** — the Sprint 4
  connection-pool work that closes the (already-existing, not
  circuit-specific) unbounded-`RoundTrip`-hang gap.
- **Sprint 4/5 work generally** — hot-reload, connection lifecycle,
  HTTP/2, benchmarks.

## Further Notes

### This task's design session

This spec is the direct product of a `grill-with-docs` session (`grilling` +
`domain-modeling`) spanning nine rounds of questions. One claim — that a
denied or disconnected request can never strand a half-open circuit's trial
slot — was verified directly against the `httputil.ReverseProxy` stdlib
source (Go 1.25.1, `net/http/httputil/reverseproxy.go`) rather than argued
from recollection, after the project owner specifically asked for that
verification. The full decision record, including every rejected
alternative, is [ADR-0011](../../docs/adr/0011-health-passive-outlier-and-circuit-breaker-composition.md),
written and accepted before this spec.

### Ticket sequencing

Revised via a proper `to-tickets` pass after an initial draft artificially
serialized this work — the first cut bundled the `MarkHealthy`/
`MarkUnhealthy` rename into S3.T1 and the `RoundTripObserver` fan-out into
S3.T2, which made S3.T2 wait on all of S3.T1's probe machinery and S3.T3
wait on all of S3.T2's counter logic, when neither dependency was real. Both
are small, mechanical, independently-verifiable prefactors, so they're their
own tickets, alongside a third schema-only prefactor for the new config
fields (avoiding two parallel tickets colliding on `config.go`):

- **01 — `MarkHealthy`/`MarkUnhealthy` rename** (no blockers)
- **02 — Round-trip observer fan-out** (no blockers)
- **03 — Sprint 3 config schema** (no blockers)
- **04 — Active health checks**, blocked by 01, 03
- **05 — Passive outlier detection**, blocked by 01, 02 (not 04 — it never
  needed the probe machinery, only the rename)
- **06 — Circuit breaker**, blocked by 02, 03 (not 05 — it never needed
  passive detection's counter, only the fan-out to register into); carries
  the `Registry.Selectable()` rename

Net effect: three tiny prefactors unblock 04, 05, and 06 to be built fully
in parallel, rather than the original serial chain. See
`.scratch/s3-t1-t3-health-passive-circuit/issues/` for the six ticket files.

### `CONTEXT.md`

`Probe`, `Selectable`, and `Trial` were already added during the design
session. Implementation should add further entries as they crystallize
(e.g. if passive detection's own vocabulary — "outlier," "ejection" — needs
a precise definition once built).
