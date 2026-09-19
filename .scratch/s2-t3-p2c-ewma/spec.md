# Spec: S2.T3 — Power-of-Two-Choices with EWMA Latency Tracking

Status: ready-for-agent

---

## Problem Statement

Sprint 2 has shipped `ConsistentHashBoundedLoads`, which caps load by *count* of
in-flight requests (`ActiveConns()`), and Sprint 1 shipped `LeastConnections`,
which minimizes that same count directly. Neither reacts to a backend that is
up, healthy, accepting connections, and returning 2xx responses, but simply
*slow* — degraded hardware, a noisy neighbor, a GC pause, a downstream
dependency it calls into that's struggling. `ActiveConns()` alone cannot see
this: a slow backend that responds in 2s looks identical to a fast one that
responds in 20ms, right up until requests start queuing on it.

`MILESTONES.md`'s Sprint 2 deliverables reserve `PowerOfTwoChoicesEWMA` for
exactly this gap ("pick two random backends, choose lower EWMA-tracked
latency"), and `docs/design/sprint-1-contracts.md`'s algorithm identifier
table and `config.AlgorithmP2CEWMA` already reserve the `p2c_ewma` config
value for it — but `config.implementedAlgorithms` doesn't include it,
`balancer.NewFromConfig` doesn't route to it, and
`internal/balancer/p2c_ewma.go` is still the Sprint 1 schema-freeze panic
stub. An operator cannot get latency-aware routing at all today, and Sprint 2
is incomplete without it: MILESTONES.md's own exit criteria requires "all 4
algorithms selectable via config" and "P2C measurably favors faster backends
after warmup."

## Solution

Implement the fourth and final Sprint-1-reserved selector, wired to the
`p2c_ewma` config identifier, plus the `Backend`-level latency-tracking
capability it depends on:

1. **Backend latency state** — `Backend` gains an EWMA-smoothed latency
   estimate alongside its existing `healthy`/`active` atomics, following the
   same unexported-field-plus-methods pattern ADR-0002 established and
   ADR-0006 already extended once for `SetHealthy`. The proxy updates it on
   every request's backend round trip, on both the success path and the
   failure path, regardless of which selector is configured — mirroring how
   `IncActive`/`DecActive` already run unconditionally today.
2. **`PowerOfTwoChoicesEWMA`** — on each `Select`, draws two distinct random
   backends from the healthy set and returns whichever has the lower
   EWMA-tracked latency. Falls back to returning the sole backend directly
   when exactly one is healthy, and to `ErrNoHealthyBackends` when none are —
   the same terminal condition every other selector already uses.
3. **Config wiring** — `p2c_ewma` added to `config.implementedAlgorithms` and
   to `balancer.NewFromConfig`'s switch, completing the fourth of four
   algorithms reserved since the Sprint 1 schema freeze.

After this lands, an operator gets latency-aware load balancing that shifts
traffic away from a backend that's degraded-but-not-dead, without needing
Sprint 3's circuit breaker to exist first.

## User Stories

**Backend latency state**

1. As a downstream selector implementer, I want `Backend` to expose an
   EWMA-smoothed latency estimate through method calls only (`RecordLatency`,
   `EWMALatency`), never a raw field, so that the encapsulation guarantee
   ADR-0002 established for `healthy`/`active` — and that ADR-0006 preserved
   when adding `SetHealthy` — holds for this new piece of state too.
2. As a project maintainer, I want the EWMA update expressed as
   `latency_new = α·observed + (1-α)·latency_old` (the formula already named
   in `AGENTS.md`'s "Concepts and patterns used" section), with α fixed at
   `0.1` as an unexported Go constant, so that the estimate is stable against
   normal per-request jitter while still adapting to a sustained regression
   within roughly the next ten requests, and so Sprint 2 doesn't grow a new
   YAML tuning surface for a value nobody has asked to configure — the same
   "constant, not config" posture ADR-0009 already took for ε.
3. As a project maintainer, I want a backend's very first-ever latency sample
   to set the EWMA directly rather than blend from a zero baseline, so that a
   freshly-added or just-recovered backend doesn't look artificially fast —
   and therefore attract a disproportionate share of P2C comparisons — purely
   because it hasn't been measured yet. The zero-value of the underlying
   atomic doubles as "no sample yet" with no separate flag, since a real
   round trip completing in exactly 0ns cannot happen against Go's monotonic
   clock.
4. As a project maintainer, I want the EWMA field stored as `atomic.Int64`
   nanoseconds, updated via a compare-and-swap retry loop rather than a
   mutex, so that `Select`'s hot read path — which reads two backends' EWMA
   on every single request — stays lock-free, consistent with the
   atomics-over-mutex convention `healthy`/`active` already established.
5. As a project maintainer, I want `RecordLatency` and `EWMALatency` callable
   by any code holding a `*Backend` — not gated to one package — the same
   openness `SetHealthy` already has per ADR-0006, since the proxy (the
   writer) and `balancer` (the reader) are different packages and neither
   should need a workaround to reach this state.
6. As a load balancer operator, I want a failed backend round trip (the
   `errorHandler` path — no response ever received) to also update the EWMA,
   with a fixed penalty duration rather than the real elapsed time-to-failure,
   so that a backend which fails fast (e.g. connection refused) doesn't look
   *attractively* fast to P2C — the opposite of the intended effect — and so
   that failures actually push future traffic away from the backend, which is
   the entire reason P2C-EWMA was chosen over `LeastConnections` for
   latency-skewed workloads.
7. As a project maintainer, I want the failure penalty fixed at a flat `2s`
   constant, so that it comfortably dominates every latency sample this
   project's own dummy backends currently produce (the checked-in
   `docker-compose.yml` defaults are 50/150/300ms) while remaining a single,
   human-graspable number — chosen with headroom above 1s specifically
   because `SLEEP_MS` has no enforced upper bound (only non-integer and
   negative values are rejected), so a future chaos configuration simulating
   a "legitimately slow but alive" backend in the 800–900ms range doesn't
   collapse the distinction the penalty exists to preserve.
8. As a project maintainer, I want `RecordLatency` called unconditionally on
   every request's backend round trip — success or failure — regardless of
   which selector is actually configured, so that `proxy` stays exactly as
   selector-agnostic as it already is for `ActiveConns` (tracked and
   maintained today even under `RoundRobin`, whose selection logic never
   reads it).

**`PowerOfTwoChoicesEWMA` selector**

9. As a load balancer operator, I want `p2c_ewma` to route each request to
   whichever of two randomly-sampled healthy backends currently has the
   lower EWMA-tracked latency, so that traffic measurably shifts away from a
   backend that's responding slowly, without needing it to be unhealthy or
   over any connection-count threshold first.
10. As a project maintainer, I want the two-backend sample drawn via
    `math/rand/v2`'s package-level functions rather than a per-selector
    `*rand.Rand`, so that `Select` doesn't need its own mutex to guard a
    non-concurrency-safe RNG — which would undercut the same hot-path
    lock-freedom the CAS-based `RecordLatency` design (story 4) is chosen
    for.
11. As a load balancer operator, I want `Select` to return the sole healthy
    backend directly, with no comparison performed, whenever exactly one
    backend is healthy — there's nothing to compare it against, so a random
    draw would be theater, not policy.
12. As a load balancer operator, I want `Select` to return
    `ErrNoHealthyBackends` when the healthy set is empty, identically to
    every other selector's terminal condition, so error handling in `proxy`
    (503 vs 502) needs no `p2c_ewma`-specific branch.
13. As a project maintainer, I want `PowerOfTwoChoicesEWMA` to satisfy
    `balancer.Selector` with its own compile-time assertion
    (`var _ Selector = (*PowerOfTwoChoicesEWMA)(nil)`), matching every other
    selector in the package.
14. As a load balancer operator, I want to see, in tests, that after a warmup
    period a backend with a lower simulated latency is chosen measurably more
    often than a slower one under sustained sampling — the load-skew property
    MILESTONES.md's Sprint 2 exit criteria names explicitly — demonstrated
    with real recorded numbers, the same evidentiary standard ADR-0009 set for
    bounded-loads' hot-key test.
15. As a load balancer operator, I want `p2c_ewma` to stop choosing a backend
    the moment it goes unhealthy and resume once it recovers, mirroring the
    health-transition property every Sprint 1 and Sprint 2 selector already
    guarantees (S1.T8's pattern).

**Proxy integration**

16. As a project maintainer, I want the latency window recorded to cover only
    the backend round trip — from `director()` rewriting the request (just
    before dispatch) to `modifyResponse` receiving the response headers — and
    explicitly *not* the full `ServeHTTP`-to-response-complete window the
    existing `latency_ms` log field already measures, so that P2C's signal
    reflects backend speed rather than how long the client took to consume a
    streamed body. This requires a new timestamp on the per-request `reqState`
    (distinct from the existing `start` used for the log line), captured at
    the end of `director()`.
17. As a future maintainer reading this code, I want an explicit comment at
    the new timestamp's declaration stating that it is not interchangeable
    with the request-complete log line's `latency_ms`, so that when Sprint
    3's metrics work arrives, nobody quietly reuses one measurement for both
    purposes and silently conflates backend-only latency with end-to-end
    client-observed latency.
18. As a load balancer operator, I want a backend's EWMA updated even when
    the request never reaches Sprint 3's health checker (which doesn't exist
    yet) — i.e. this task does not depend on or block on Sprint 3 — so that
    `p2c_ewma` is a fully working, standalone Sprint 2 deliverable.

**Cross-cutting**

19. As an operator selecting `p2c_ewma` in YAML, I want it recognized by
    `config.Validate` and routed correctly by `balancer.NewFromConfig`, so
    that all four Sprint-1-reserved algorithm identifiers are finally
    selectable via config — the explicit Sprint 2 exit criterion.
20. As a project maintainer, I want the ADR for this task to bundle the three
    decisions that are genuinely hard to reverse or surprising without
    context — `Backend`-owned latency state (amending ADR-0002, symmetric
    with ADR-0006), cold-start first-sample semantics, and the fixed failure
    penalty (with the unconditional-recording mechanism noted in one line) —
    into a single ADR-0010, following the same per-task bundling precedent
    ADR-0008 and ADR-0009 set for S2.T1/T2, rather than one ADR per
    individual decision.
21. As a project maintainer, I want α, the CAS-loop/nanosecond representation,
    the `math/rand/v2` choice, and the sole-healthy-backend bypass documented
    as inline code comments rather than ADR entries, since each is either
    dictated by existing project convention or has no real reversibility
    cost — matching this task's own design-review conclusion that not every
    decision clears the three-part ADR bar (hard to reverse, surprising
    without context, a genuine trade-off).

## Implementation Decisions

### `Backend` latency state (`internal/backend`)

- New unexported field, e.g. `latencyEWMA atomic.Int64` (nanoseconds),
  alongside the existing `healthy`/`active` atomics. Amends ADR-0002 decision
  4/5 the same way ADR-0006 did for `SetHealthy` — recorded in the new
  ADR-0010, not by editing ADR-0002 or the frozen Sprint 1 contracts doc.
- Two new exported methods:
  - `RecordLatency(d time.Duration)` — on the very first call for a given
    `Backend` (zero-value field), stores `d` directly. On every subsequent
    call, stores `α·d + (1-α)·previous` via a CAS retry loop, with `α = 0.1`
    as an unexported package constant. Illustrative shape of the update (the
    exact loop form, not a literal diff):
    ```go
    for {
        old := b.latencyEWMA.Load()
        var next int64
        if old == 0 {
            next = int64(d)
        } else {
            next = int64(ewmaAlpha*float64(d) + (1-ewmaAlpha)*float64(old))
        }
        if b.latencyEWMA.CompareAndSwap(old, next) {
            return
        }
    }
    ```
  - `EWMALatency() time.Duration` — reads the field, returns it as a
    `time.Duration`. A never-recorded backend reads zero; `Select` treats
    that as "fastest possible," which is an intentional, self-correcting
    bootstrap property (any backend that's never been sampled wins its next
    comparison, gets a real sample, and stops reading zero) rather than a
    bug needing a guard.
- Both methods callable by any package holding a `*Backend`, matching
  `SetHealthy`'s openness (ADR-0006) rather than gating through `Registry`.

### `PowerOfTwoChoicesEWMA` (`internal/balancer`)

- Fills in the frozen Sprint 1 stub's signature exactly:
  `NewPowerOfTwoChoicesEWMA(reg *backend.Registry) *PowerOfTwoChoicesEWMA` and
  `Select(ctx context.Context, r *http.Request) (*backend.Backend, error)` —
  no signature changes.
- `Select`: snapshot `reg.Healthy()`. Zero healthy → `ErrNoHealthyBackends`.
  Exactly one healthy → return it directly, no draw. Two or more → draw two
  distinct indices via `math/rand/v2` package-level functions, compare
  `EWMALatency()` on each, return the lower (tie broken arbitrarily — no
  deterministic tie-break is promised or needed, unlike `LeastConnections`,
  since a tie between EWMA float-derived values from two different backends
  is not a reproducible-by-design scenario the way registry-order ties are).
- Compile-time assertion `var _ Selector = (*PowerOfTwoChoicesEWMA)(nil)`,
  same as every other selector.
- No hash key, no request-derived state — `p2c_ewma` has no session-affinity
  property, unlike `consistent_hash`.

### Proxy wiring (`internal/proxy`)

- `reqState` gains a new field (e.g. `dispatchStart time.Time`), set at the
  end of `director()` — deliberately not reusing the `start` timestamp
  `ServeHTTP` already captures before `Select` runs, since that window also
  includes selection overhead and, for the log line, the full response-body
  copy to the client.
- `modifyResponse`: alongside its existing `status` capture, computes
  `time.Since(state.dispatchStart)` and calls
  `state.backend.RecordLatency(...)` — unconditionally, every request,
  regardless of configured selector.
- `errorHandler`: alongside its existing `release()` call, calls
  `state.backend.RecordLatency(p2cFailurePenalty)` where
  `p2cFailurePenalty` is a fixed `2 * time.Second` constant — also
  unconditional.
- A comment at the `dispatchStart` field (or immediately around its use)
  states explicitly that it is a distinct measurement from the "request
  complete" log line's `latency_ms`, and why.

### Config wiring (`internal/config`, `internal/balancer`)

- `config.AlgorithmP2CEWMA` (`"p2c_ewma"`) added to
  `config.implementedAlgorithms`.
- `balancer.NewFromConfig`'s switch gains
  `case config.AlgorithmP2CEWMA: return NewPowerOfTwoChoicesEWMA(reg), nil`.
- Both are one-line additions following the exact pattern ADR-0004 and S2.T2
  already established for adding a new selector to both maps.

### ADR-0010

Bundles, in one document, per the S2.T1/T2 (ADR-0008/ADR-0009) precedent of
one ADR per task rather than one per decision:

- `Backend`-owned latency state, amending ADR-0002 (symmetric with
  ADR-0006's `SetHealthy` precedent).
- Cold-start semantics: first sample sets directly, no zero-blend.
- Fixed `2s` failure penalty, including the reasoning against using raw
  elapsed time-to-failure (a fast failure would look attractively fast) and
  the explicit note that `SLEEP_MS` has no enforced ceiling, so "sub-second"
  is a description of today's committed defaults, not a guaranteed margin.
- One line noting `RecordLatency` is called unconditionally regardless of
  active selector, mirroring `IncActive`/`DecActive`.

α, the CAS-loop/nanosecond representation, `math/rand/v2`, and the
sole-healthy bypass are inline comments only — evaluated against the
project's three-part ADR bar and judged not to clear it (each either follows
directly from existing convention or has negligible reversal cost).

## Testing Decisions

### What makes a good test here

Tests exercise external behavior — selection outcome, recorded latency
values, error returns, measured distribution under simulated latency skew —
never internal implementation details (the CAS loop's retry mechanics, for
instance, are not directly observable or asserted). Table-driven where the
input space is enumerable; `testify/require` for setup, `testify/assert` for
value checks, matching every prior package's convention.

### Seams

Three seams — all existing, none new, confirmed directly with the project
owner before writing this spec:

1. **`Backend`'s own method API** (`internal/backend`) — `RecordLatency` and
   `EWMALatency` called directly, no selector, no HTTP. The same seam
   `backend_test.go` already uses for `IsHealthy`/`IncActive`/`DecActive`
   (S1.T3).
2. **`balancer.Selector` interface** — `PowerOfTwoChoicesEWMA` instantiated
   directly, exercised via `Select(ctx, r)` against a real
   `*backend.Registry`, with `RecordLatency` pre-seeded directly on fixture
   backends to construct "A is faster than B" scenarios without needing real
   HTTP round trips. The same seam every Sprint 1 and Sprint 2 selector test
   already uses.
3. **`proxy.Proxy`'s existing `ServeHTTP` seam** — a small number of new
   cases added to the existing proxy test file (S1.T6's `httptest`-backed
   harness), not a new harness. Confirmed with the project owner as worth
   keeping (rather than trusting the wiring implicitly) because ADR-0007
   already established the precedent of testing request-lifecycle wiring —
   the exactly-once-decrement guarantee — directly at this seam.

### Test groupings and cases

- **`Backend` latency state**: first-ever `RecordLatency` call sets the value
  directly (no blend from zero); a second call blends per the α formula
  against a known prior value with an assertable expected result; concurrent
  `RecordLatency` calls under `-race` converge to a stable value with no data
  race; `EWMALatency()` on a never-recorded backend reads zero.
- **`PowerOfTwoChoicesEWMA` conformance**: compile-time `Selector` assertion;
  empty healthy set → `ErrNoHealthyBackends`; exactly one healthy backend is
  always returned with no draw; a backend going unhealthy mid-run is skipped
  and resumes being chosen once healthy again (S1.T8's pattern); given two
  backends with a pre-seeded, sustained EWMA gap, repeated `Select` calls
  choose the faster one measurably more often over a large sample —
  the load-skew property, demonstrated with real numbers the way ADR-0009's
  hot-key test demonstrated bounded-loads' rebalancing.
- **Proxy wiring**: a successful round trip records a real, non-zero,
  round-trip-scoped latency on the backend that actually served the request
  (not on any other configured backend); a round trip that fails before a
  response is received (triggering `errorHandler`) records the fixed `2s`
  penalty rather than the real (potentially very short) elapsed time.
- **Config wiring**: `p2c_ewma` is accepted by `config.Validate` and
  `balancer.NewFromConfig` returns a working `*PowerOfTwoChoicesEWMA` for it
  — mirrors the existing per-algorithm factory tests in `factory_test.go`.

### Prior art

- `internal/backend/backend_test.go` (S1.T3) — direct-method seam for
  `Backend`'s existing atomics, including a concurrent `-race` case.
- `internal/balancer`'s selector tests (S1.T4, S1.T5, S1.T8, and S2.T1/T2's
  hot-key comparative test) — the `Selector`-interface seam and the
  precedent for demonstrating a distribution property with real recorded
  numbers rather than asserting only the mechanism.
- `internal/proxy`'s existing test file and ADR-0007 — the proxy-lifecycle
  seam, and the precedent for testing request-lifecycle wiring
  (exactly-once-decrement) directly rather than inferring it from
  `Backend`-level tests alone.

## Out of Scope

- **Configurable α or failure penalty** — both are fixed Go constants; no
  `config.Config` schema change in this scope, matching the "constant, not
  config" posture ADR-0009 already set for ε.
- **Sprint 3 health checking, circuit breaking, metrics** — `p2c_ewma` calls
  `IsHealthy()` exactly like every other selector; no new health-state
  producer or consumer is introduced, and this task does not depend on
  Sprint 3 landing first.
- **A formal statistical proof of P2C's load-balance property** — the
  Mitzenmacher (2001) result is cited as the algorithm's justification in
  `AGENTS.md`; this task's tests demonstrate the property empirically at the
  same evidentiary bar ADR-0009 set, not re-derive it.
- **Reusing or unifying the `dispatchStart` timestamp with the existing
  request-complete log line's `latency_ms`** — deliberately kept as two
  distinct measurements (story 16/17); no `internal/logger` or log-field
  change in this scope.
- **Dynamic backend membership, hot-reload** — Sprint 4's problem, unrelated
  to this task.
- **HTTP/2, connection pool tuning, the benchmark rig** — unrelated
  later-sprint work.

## Further Notes

### This task's design session

This spec is the direct product of a grilling session (`grill-with-docs`)
covering: where EWMA state lives, α, cold-start semantics, the atomic
representation, the randomness source, the sole-healthy-backend bypass, the
latency measurement window, and failure-path penalty behavior — each
resolved and confirmed with the project owner in turn, including one
mid-session correction (the failure penalty moved from an initially-proposed
1s to 2s after verifying, against the actual repository state, that
`SLEEP_MS` has no enforced upper bound and the "sub-second" reasoning was
resting on an unstated assumption rather than a measured constraint). No
separate prototype was built; the CAS-loop and cold-start reasoning were
verified by design-session analysis against this project's existing atomics
conventions, not by running code.

### `CONTEXT.md`

This task introduces at least one new glossary-worthy term — the EWMA-tracked
latency estimate itself — into `CONTEXT.md`, alongside the existing `Backend`,
`Selector`, `Load`, and `Capacity` entries from the S2.T1/T2 session.
Implementation should add the entry (and any others that crystallize during
the work) rather than leaving the new state undocumented in the glossary.
