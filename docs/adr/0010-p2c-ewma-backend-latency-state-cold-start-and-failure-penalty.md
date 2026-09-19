# ADR-0010: P2C-EWMA — Backend-owned latency state, cold-start semantics, and the failure penalty

- **Status**: Accepted
- **Date**: 2026-09-19
- **Deciders**: Darshan Jain (project owner) + opencode agent (S2.T3)

## Context

Sprint 2's second algorithm is `PowerOfTwoChoicesEWMA` (the `p2c_ewma`
config identifier). It exists for the gap the other three selectors cannot
see: `LeastConnections` and `ConsistentHashBoundedLoads` cap load by *count*
of in-flight requests, so a backend that is up, healthy, accepting
connections, and returning 2xx — but simply *slow* (degraded hardware, a
noisy neighbor, a GC pause, a struggling downstream dependency) — is
indistinguishable from a fast one right up until requests start queuing on
it. P2C-EWMA routes away from a backend that is measurably slower, without
needing it to be unhealthy or over any connection-count threshold first.

Delivering that requires new *state* — a per-backend latency estimate — and
that state has three properties that are hard to reverse or genuinely
surprising without context: where it lives, what the very first sample means,
and what a failed round trip records. Those are recorded here, in one
document, following the per-task bundling precedent ADR-0008 and ADR-0009 set
for Sprint 2. The smaller choices that either follow directly from existing
project convention or have negligible reversal cost — the smoothing factor α,
the `atomic.Int64`-nanoseconds representation and its CAS retry loop, the
`math/rand/v2` source for the two-backend draw, and the sole-healthy-backend
bypass — are documented as inline code comments at their use sites instead.

## Decision

1. **`Backend` owns the EWMA-smoothed latency estimate.** A new unexported
   `latencyEWMA atomic.Int64` field (nanoseconds) lives on `Backend`
   alongside `healthy`/`active`, reached only through exported methods
   `RecordLatency(d time.Duration)` and `EWMALatency() time.Duration`. This
   amends ADR-0002 decision 5 a second time — symmetric with
   [ADR-0006](0006-backend-sethealthy-amends-adr-0002.md)'s `SetHealthy` —
   rather than editing ADR-0002 or the frozen Sprint 1 contracts doc. Any
   package holding a `*Backend` may call both methods: the proxy (the writer)
   and `balancer` (the reader) are different packages and neither should need
   a workaround to reach state it owns a side of. Encapsulation is preserved:
   the field stays unexported, so its type remains free to change.

2. **Cold start: the first sample sets the estimate directly; it is never
   blended from a zero baseline.** On the first-ever `RecordLatency` call for
   a backend the atomic reads zero, and the observed duration is stored
   verbatim. Every subsequent call blends per
   `latency_new = α·observed + (1-α)·latency_old` with α = 0.1 (an unexported
   constant), so the estimate is stable against per-request jitter while
   still adapting to a sustained regression within roughly ten requests. The
   zero value doubles as "no sample yet" with no separate flag: a real round
   trip completing in exactly 0ns cannot happen against Go's monotonic clock.
   A consequence is that a never-recorded backend reads `EWMALatency() == 0`,
   which `Select` treats as fastest-possible — an intentional, self-correcting
   bootstrap (such a backend wins its next comparison, receives a real sample,
   and stops reading zero), not a condition needing a guard.

3. **A failed round trip records a fixed `2 * time.Second` penalty, not the
   real elapsed time-to-failure.** `errorHandler` (no response ever received)
   calls `RecordLatency(p2cFailurePenalty)` with `p2cFailurePenalty` a fixed
   Go constant. Using the real elapsed time would be actively harmful: a
   backend failing fast (e.g. connection refused) would record a near-zero
   duration and look *attractively* fast to P2C — the opposite of the intended
   effect — and failures would not push traffic away from the failing
   backend, which is the entire reason P2C-EWMA was chosen over
   `LeastConnections`. 2s comfortably dominates every latency this project's
   own dummy backends currently produce (the checked-in `docker-compose.yml`
   defaults are 50/150/300ms), while remaining a single human-graspable
   number. It is set with headroom above 1s specifically because `SLEEP_MS`
   has no enforced upper bound — the dummy backend rejects only non-integer
   and negative values — so "sub-second" is a description of today's
   committed defaults, not a guaranteed margin; a future chaos configuration
   simulating a "legitimately slow but alive" backend in the 800–900ms range
   must not collapse the distinction the penalty exists to preserve. The
   penalty is the fixed *input* to `RecordLatency`, not a hard overwrite of the
   stored estimate: it is folded through the same EWMA as any observation, so
   repeated failures converge the estimate toward 2s (roughly 1.3s after ten
   consecutive failures, 1.75s after twenty) while a single transient failure
   does not erase a backend's history. That is the behavior the Sprint 2 spec's
   Implementation Decisions prescribe (`… calls state.backend.RecordLatency(p2cFailurePenalty)`);
   "flat" describes the penalty input, not the resulting smoothed value.

4. **Recording is unconditional, and the window measured is the backend
   round trip only.** `RecordLatency` is called on every request's backend
   round trip — success and failure alike — regardless of which selector is
   configured, mirroring how `IncActive`/`DecActive` already run
   unconditionally today (tracked and maintained even under `RoundRobin`,
   whose selection logic never reads them). The duration is measured from
   `director()`, immediately before dispatch, to `modifyResponse` receiving
   the response headers — explicitly not the full `ServeHTTP`-to-body-close
   window the existing `latency_ms` log field measures, so P2C's signal
   reflects backend speed rather than how long the client took to consume a
   streamed body. That window is why `reqState` carries a second timestamp
   distinct from the log line's `start`; the two are not interchangeable.

## Evidence

### Load-skew property (checked in)

`TestPowerOfTwoChoicesEWMAShiftsLoadToFasterBackend` seeds one fast backend
(10ms) and three slow (500ms) across four healthy backends, then makes 4,000
`Select` calls. Because P2C samples two of the four, the fast backend is drawn
half the time and wins every comparison it appears in; each slow backend is
drawn and compared against a slow peer the other times. One recorded run:

| Backend | Selections | Share |
|---|---|---|
| backend-a (fast, 10ms) | **2,043** | 51.1% |
| backend-b (slow, 500ms) | 671 | 16.8% |
| backend-c (slow, 500ms) | 647 | 16.2% |
| backend-d (slow, 500ms) | 639 | 16.0% |

The test asserts the fast backend lands near half the requests
(`InDelta(2000, ±400)`) and receives more than twice the busiest slow
backend's share. `TestPowerOfTwoChoicesEWMAPrefersFasterOfTwo` pins the
two-backend case, where both backends are always sampled and the faster one is
therefore chosen on every call. As with ADR-0009's hot-key result, these are
this repository's own measurements on a deterministic fixture; the generator
uses `math/rand/v2`'s global source, so the exact split varies run to run while
the property does not.

## Consequences

- Positive: P2C-EWMA is a fully working, standalone Sprint 2 deliverable — it
  needs no Sprint 3 health checker, circuit breaker, or metrics to function.
- Positive: latency state follows the same method-only, unexported-atomic
  pattern as `healthy`/`active`, so its representation can change later
  without touching `balancer` or `proxy`.
- Positive: failures cannot masquerade as low latency; a failing backend
  accrues a penalty that shifts future traffic away from it.
- Negative: `Backend`'s exported surface grows again (now `IsHealthy` /
  `SetHealthy` / `IncActive` / `DecActive` / `ActiveConns` /
  `RecordLatency` / `EWMALatency`), and any caller holding a `*Backend` can
  record a latency; like `SetHealthy`, this is convention, not
  type-system-enforced.
- Negative: the fixed penalty is a heuristic, not a measurement. A backend
  that is genuinely slow to *fail* (e.g. a long TCP connect timeout) records
  2s, which may under-state its real cost; this is accepted because the
  alternative (recording real time) has the worse, wrong-signed failure mode.
- Neutral: α = 0.1 and the 2s penalty are Go constants, not `config.Config`
  fields — the same "constant, not config" posture ADR-0009 took for ε. Both
  are one-line changes if an operator ever asks.
- Neutral: a never-recorded backend reads zero and therefore tends to win its
  first comparisons; the effect is bounded to one request per backend and
  self-corrects.
- Neutral: recording on `errorHandler` is unconditional, so a client
  cancellation that aborts the round trip also records the penalty even though
  the backend is not at fault. Distinguishing cancellation is deliberately out
  of scope here (Sprint 4 owns client-cancellation handling); the spec records
  the errorHandler path unconditionally, and any exclusion belongs with the
  Sprint 4 lifecycle work rather than as an unreviewed branch here.

## Alternatives considered

- **Latency state owned by the `Registry` (or a separate metrics component)
  rather than `Backend`:** rejected — `SetHealthy` (ADR-0006) established
  that per-backend observable state lives on `Backend` with direct method
  access, and `Registry` does not mediate `Backend`'s mutable state
  (`IncActive`/`DecActive` already bypass it). A registry- or name-keyed
  store would add a lookup per request on the hot path for no isolation
  benefit.
- **A separate `latency`/`Publisher` interface implemented by `Backend`:**
  rejected — `Balancer` reads and `proxy` writes the same concrete
  `*Backend`; introducing an interface between them would be indirection
  with one implementation.
- **Blend the first sample from a zero baseline (`new = α·observed`):** rejected
  — a freshly-added or just-recovered backend would read artificially *fast*,
  attract a disproportionate share of P2C comparisons, and only converge
  slowly; the direct-set rule gives a first sample that is exactly the
  observed value.
- **Record the real elapsed time-to-failure (or no penalty at all):**
  rejected — see decision 3; a fast failure would look attractively fast and
  traffic would not move away from the failing backend.
- **A configurable penalty or α:** deferred — no operator has asked; adding
  config surface is a one-line change later, matching ADR-0008/0009's
  treatment of vnode count, key source, and ε.
- **Reuse the request-complete log line's `latency_ms` for the EWMA sample:**
  rejected — it includes the full response-body copy to the client, so a slow
  *client* would penalize a fast *backend*; P2C's signal must be the backend
  round trip alone.
