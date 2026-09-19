# ADR-0011: Health, passive-outlier, and circuit-breaker composition

- **Status**: Accepted
- **Date**: 2026-09-20
- **Deciders**: Darshan Jain (project owner) + Claude (Sonnet 5) discussion session, pre-S3.T1/T2/T3

## Context

Sprint 3 introduces three subsystems that can each independently conclude
"don't send traffic to backend X," on different timescales and for
different reasons: active health checks (S3.T1), passive outlier detection
(S3.T2), and a per-backend circuit breaker (S3.T3). `Backend.go`'s existing
doc comment already anticipated part of this — "`healthy` is written by the
health checker (active probes and passive outlier detection, both Sprint
3)" — but that was a Sprint-1-era stub comment, not a decision, and it says
nothing about how the circuit breaker fits. Left unresolved, three real
failure modes were identified during discussion:

1. A circuit that flips `healthy=false` on trip removes the backend from
   `Registry.Healthy()` entirely, so nothing ever routes a half-open trial
   request to it — the state machine's own recovery path becomes
   unreachable.
2. Multiple independent writers on one shared bit (active checks, passive
   detection, circuit recovery) race: whichever writes last silently
   overrides the others' decision, exactly the kind of shared-mutable-state
   hazard ADR-0007 was careful to avoid for `ActiveConns`.
3. Both S3.T2 and S3.T3 need to observe the same two proxy hook points
   (`modifyResponse`, `errorHandler`) that today make exactly one call
   (`RecordLatency`, per ADR-0010). Whichever lands second has to convert a
   direct call into a fan-out; if the first implementation doesn't
   anticipate the second, the fan-out gets built twice.

This ADR records the composition model and the mechanical decisions it
forces, in one document, following the per-topic bundling precedent
ADR-0002 and ADR-0010 set. It also records how the three Sprint 3 tasks
should be sequenced against each other, since the fan-out decision (item 9
below) is a ticket-boundary concern, not just an architectural one.

## Decision

### Authority model

1. **Selection eligibility is `healthy AND circuit-not-open`; `Backend.healthy`
   stays owned exclusively by active checks and passive outlier
   detection — the circuit breaker never writes it.** This is the fix for
   failure mode 1 and 2 above: circuit state and `healthy` become two
   orthogonal facts ANDed at read time instead of two writers fighting over
   one bit. A half-open backend keeps `healthy=true` and stays in the normal
   candidate pool; the breaker's own per-request gate (decision 7), not pool
   membership, is what enforces "exactly one trial in flight." `Backend`'s
   own `IsHealthy()` stays narrow and untouched. `Registry.Healthy()` is
   renamed to `Registry.Selectable()` so its name doesn't quietly mean
   something wider than the field it reads — the same honesty property
   `IsHealthy()` already has. This is a change to a name frozen in the
   Sprint 1 concurrency ownership table (`docs/design/sprint-1-contracts.md`
   line 167); per **decision 15**, it ships with S3.T3, not S3.T1 or S3.T2,
   since its justification (ANDing in circuit state) doesn't exist until
   circuit state does.

2. **`SetHealthy(bool)` is split into `MarkHealthy()` and
   `MarkUnhealthy()`.** Both active checks and passive detection may call
   `MarkUnhealthy()`; only active checks call `MarkHealthy()`, by convention
   (Go cannot enforce caller identity, the same limitation ADR-0006 already
   accepted for `SetHealthy`). This makes the recovery asymmetry in
   **decision 3** legible at the call site instead of only living in this
   ADR's prose. This amends [ADR-0006](0006-backend-sethealthy-amends-adr-0002.md)
   (itself amending ADR-0002 decision 5) — a fourth link in the same
   amendment chain that continued through [ADR-0010](0010-p2c-ewma-backend-latency-state-cold-start-and-failure-penalty.md).

3. **A passively-ejected backend recovers only via the next successful
   active probe.** Passive detection has no independent timer-based
   reinstatement. This keeps the state machine single-path (one way in via
   either subsystem, one way out via active checks only) rather than two
   independent recovery mechanisms that could disagree, and it makes active
   health checking a load-bearing prerequisite for passive detection.

4. **No independent enable/disable toggle for any of the three
   subsystems.** All three ship always-on together. Given decision 3, a
   config surface that allowed "passive detection on, active checks off"
   would create a backend that can be ejected but can never recover — a
   footgun state simplest to not offer rather than specially reject in
   validation.

### Circuit breaker internals

5. **Circuit state lives directly on `Backend`, not in a `circuit`-package-owned
   map.** State (closed/open/half-open, consecutive-failure count,
   opened-at timestamp, half-open-trial-in-flight flag) is a small set of
   unexported, CAS-guarded fields on `Backend`, reached only through
   exported methods — mirroring how `latencyEWMA` was added in ADR-0010
   rather than editing ADR-0002. This is forced, not just preferred: the
   package graph has `internal/circuit` depending on `internal/backend`
   and never the reverse, so `Backend` cannot import a `circuit`-defined
   type without a cycle. It also avoids inventing a `map[*Backend]state`
   lookup with its own concurrency story, one Sprint 4's registry-reload
   work would otherwise have to reconcile with.

6. **Open→Half-Open is a lazy, timer-free transition, and the half-open
   trial slot is CAS-guarded, not a plain bool.** Any read of circuit state
   checks `time.Since(openedAt) >= cooldown` and CASes `Open`→`HalfOpen` on
   the spot; there is no background goroutine, keeping `circuit` free of
   its own goroutine lifecycle (unlike `health`, which owns one per
   backend per S3.T1). Both `Registry.Selectable()` and the per-request
   `Allow()` gate read through this same promotion path. The trial slot
   itself needs a real `CompareAndSwap`, not a bool guarded by convention:
   two requests can land in the same instant the circuit is observed to be
   past cooldown, and only a CAS guarantees exactly one of them believes
   it is "the" trial. Accepted consequence: a backend receiving zero
   traffic during its cooldown window is never observed to recover until
   traffic actually reaches it — acceptable, since a backend nothing is
   routing to doesn't need to be mid-rotation.

7. **The breaker's `Allow(b)` gate is checked immediately after `Select()`
   returns and before `IncActive()`** — the same position `ServeHTTP`
   already uses for the `ErrNoHealthyBackends` short-circuit. A denied
   request never touches active-connection accounting, so ADR-0007's
   exactly-once `DecActive` discipline needs no new exit path; there is
   nothing to release for a request that was never dispatched. Verified
   against the `httputil.ReverseProxy` source (Go 1.25.1,
   `net/http/httputil/reverseproxy.go:333-343,493-533`) that a `RoundTrip`
   failure — including a client disconnect or context cancellation before
   any response is received — is exhaustive for "the trial got no
   verdict," and that a disconnect *after* headers arrive cannot strand a
   trial, because `modifyResponse` (where the verdict is recorded, per
   decision 9) already runs before body-streaming begins; whatever happens
   to the body afterward is irrelevant to whether the backend answered.
   The only way neither hook fires is an unbounded hung `RoundTrip` with no
   timeout — an existing systemic gap (it would already leak `ActiveConns`
   today, independent of the circuit breaker) that Sprint 4's transport
   timeout work (`ResponseHeaderTimeout`, `DialContext` timeout) already
   owns. No separate timeout on the trial slot is needed.

8. **The circuit's failure counter is consecutive (reset to zero on any
   success); passive detection's is a sliding window that tolerates
   occasional failures.** This is the substantive difference between the
   two mechanisms, not just differently-tuned thresholds on the same
   counting method — circuit is strict and fast, passive is tolerant and
   slow, matching `AGENTS.md`'s existing wording ("Closed: … Track failure
   count" vs. passive's explicit "sliding window"). Both watch the same
   underlying signal: a 5xx response or an `errorHandler` transport
   failure (decision 12). Thresholds and window sizes for both are Go
   constants (decision 10), so nothing here is config-tunable per backend.

### Config-vs-constant, probe mechanics, and wiring

9.  **`modifyResponse`/`errorHandler` fan out to every registered
   `RoundTripObserver`, unconditionally, regardless of current circuit
   state.** A new consumer-defined interface,

    ```go
    // RoundTripObserver receives every backend round trip's outcome.
    // Called unconditionally — success and failure alike, regardless of
    // the configured selector or the circuit breaker's current state —
    // mirroring how IncActive/DecActive already run unconditionally.
    type RoundTripObserver interface {
        ObserveRoundTrip(b *backend.Backend, d time.Duration, success bool)
    }
    ```

    replaces the single hardcoded `state.backend.RecordLatency(d)` call.
    `Proxy` gains an additive `RegisterObserver(o RoundTripObserver)`
    method — not a change to `New(reg, sel)`'s frozen two-argument
    signature — and holds a slice, appended to at construction time in
    `main.go`. `modifyResponse` computes `success := resp.StatusCode < 500`
    and `d := time.Since(state.dispatchStart)`; `errorHandler` always
    passes `success = false, d = p2cFailurePenalty`; both then loop over
    every registered observer. **Recording is unconditional; gating is
    conditional** — they are different call sites entirely. An
    implementation that adds an early "circuit already open, skip
    recording" guard looks like a reasonable simplification and is
    actively wrong: it is the only mechanism that feeds a half-open
    trial's result back into the Closed/Open decision.

10. **Split tunables between config and constants.** Probe interval,
    probe timeout, and circuit cooldown duration become new **global**
    (not per-backend) `Config` fields — these are operational knobs that
    plausibly differ per deployment (a slow dev docker-compose vs. a fast
    prod environment). Consecutive-success/failure thresholds for active
    health, the passive-outlier window size and failure threshold, and the
    circuit's consecutive-failure-to-open threshold all stay Go constants,
    matching the "constant, not config" posture ADR-0009 (ε) and ADR-0010
    (α, the failure penalty) already established for algorithm-shaped
    tuning values. No per-backend overrides for any of these yet — every
    existing config field is either global or backend-identity
    (`name`/`url`), and today's dummy backends are homogeneous.

11. **Active probes issue a plain GET to the backend's already-configured
    URL** — no new `health_path` field — **and only 2xx counts as
    success; 3xx is a failure.** `httputil.ReverseProxy` forwards
    redirects verbatim rather than following them, so a redirecting
    backend is unusable for real traffic even though it answered. The
    health checker uses its own dedicated `http.Client` with its own
    timeout, independent of the proxy's transport — `internal/health`
    does not depend on `internal/proxy`.

12. **Passive detection's failure signal is the union of 5xx responses and
    `errorHandler` transport failures**, over a **count-based** (not
    time-based) sliding window per backend — consistent with this
    project's existing preference for consecutive/count-based state
    machines over clock-based ones (mirrors the active-check state
    machine's "N consecutive" language).

13. **Probe goroutines reuse `main.go`'s existing `sigCtx`**
    (the `signal.NotifyContext` for SIGINT/SIGTERM) rather than a second
    shutdown primitive; one goroutine per backend selects on `ctx.Done()`
    against a ticker. Each probe cycle's logic is factored as a function
    separate from the `for { select }` loop wiring so it is unit-testable
    without a real ticker or context cancellation.

### Ticket sequencing

14. **S3.T1 (active health) is fully independent of decision 9's fan-out**
    and can be built, tested, and merged standalone — it never touches
    `modifyResponse`/`errorHandler`, only its own goroutine calling
    `MarkHealthy`/`MarkUnhealthy` directly.

15. **S3.T2 (passive outlier detection) is responsible for building the
    generic `RoundTripObserver` fan-out (decision 9), sized for three
    listeners (`RecordLatency`, passive-outlier, circuit) from the start,
    even though only two are wired until S3.T3 lands.** This is
    deliberately not split into "T2 does the minimum, T3 refactors it
    later": a fan-out built for exactly the callers known at the time is
    the common source of "just add another if-statement" drift this
    project's ADR discipline exists to prevent. S3.T3 then registers as a
    third observer with **zero further changes to `proxy.go`'s hook
    logic**, and is the task that ships **decision 1's**
    `Selectable()` rename. T1/T2/T3 remain three separate tickets, not a
    combined T2+T3 ticket — the fan-out is a small, precisely-specifiable
    interface addition (this ADR pins its exact shape), not a reason to
    merge two otherwise-independent state machines into one commit.

## Consequences

- Positive: every one of the three subsystems has an unambiguous single
  writer or read path for its state, closing the two race conditions
  identified in Context.
- Positive: no existing selector's code changes at all — `Registry`'s
  callers still call one method (now `Selectable()`) and get one filtered
  slice back, same as `Healthy()` today.
- Positive: the fan-out mechanism (decision 9) is specified precisely
  enough that S3.T2 and S3.T3 can be built as independent tickets by
  different sessions without re-deriving its shape from a conversation.
- Negative: `Backend`'s exported surface grows again (circuit-state
  accessors join `IsHealthy`/`MarkHealthy`/`MarkUnhealthy`/`IncActive`/
  `DecActive`/`ActiveConns`/`RecordLatency`/`EWMALatency`); like
  `SetHealthy`, caller-identity conventions (decision 2) are not
  type-system-enforced.
- Negative: `Registry.Selectable()`'s rename is a breaking change to a
  name frozen in the Sprint 1 contracts doc — mechanical, but every
  selector call site and its tests must be touched in S3.T3.
- Neutral: a backend that recovers from passive ejection but whose circuit
  independently tripped stays excluded until the circuit's own cooldown
  and trial resolve — the two subsystems can disagree about *why* a
  backend is unreachable, by design (decision 1's whole point is that they
  don't need to agree, only to both gate correctly).
- Neutral: three of Sprint 3's thresholds are constants, not config, per
  decision 10 — a one-line change later if an operator ever asks, matching
  ADR-0009/ADR-0010's precedent.

## Alternatives considered

- **Circuit breaker also writes `Backend.healthy` on trip/recovery**:
  rejected — this is failure mode 1/2 in Context; it makes the half-open
  trial unreachable through normal selection and creates a last-writer-wins
  race between circuit recovery and active/passive checks.
- **Circuit breaker as the sole selection authority, with active/passive
  checks reduced to failure-signal sources feeding it**: rejected — bigger
  deviation from the existing `Backend.go` stub comment and from the
  package graph's "no edge between `health` and `circuit`"; would require
  either `circuit` depending on `health` or a third mediating component
  neither package graph nor any existing ADR anticipates.
- **Three fully independent gates (`healthy`, a separate `ejected` field,
  circuit state) ANDed at selection time**: rejected — more moving parts
  than decision 1's two-gate model for no behavioral difference, since
  passive detection writing the same `healthy` bit as active checks
  (decision 3's single recovery path) already gives clean attribution
  without a third field.
- **`circuit` package owns a `map[*Backend]*breakerState`, `Backend` stays
  untouched**: rejected in decision 5 — introduces a lookup plus its own
  concurrency story for a map that Sprint 4's reload work would need to
  reconcile with separately from every other per-backend field.
- **Timer/goroutine-driven Open→Half-Open transition**: rejected in
  decision 6 — `circuit` would need its own goroutine lifecycle
  (mirroring `health`'s, per S3.T1) for a transition a lazy read-time check
  handles with no extra state.
- **Proxy re-selects (excluding the denied backend) when `Allow()` denies a
  half-open request**: rejected in decision 7 — requires the `Selector`
  interface to grow an exclude-set parameter, and would decide the
  explicitly-deferred Sprint 4 retry-policy ADR by accident, for one
  specific code path.
- **A trial-slot timeout as a defensive measure against a stuck half-open
  trial**: rejected in decision 7 after verifying against the
  `httputil.ReverseProxy` source that the scenario motivating it is
  unreachable given how the two proxy hooks are wired; adding timeout state
  for an unreachable case would be speculative complexity.
- **Circuit's failure counter also uses a sliding window (matching
  passive detection)**: rejected in decision 8 — would make circuit and
  passive detection the same mechanism at two thresholds rather than two
  genuinely different failure-response postures.
- **Circuit reacts only to transport-level failures, not 5xx** (narrower
  than passive detection's signal): rejected in decision 8 — would make
  `MILESTONES.md`'s chaos-test exit criterion ("injecting 500s on one
  backend eventually opens its circuit") impossible to satisfy.
- **Per-backend overrides for probe interval/timeout/cooldown**: deferred
  in decision 10 — no precedent in the current schema, additive later if a
  real need appears.
- **Health-check path as a configurable per-backend field**: deferred in
  decision 11 — the backend's own URL is sufficient for every currently
  committed dummy backend; adding a field for a need that hasn't appeared
  yet would widen the `KnownFields(true)` schema speculatively.
- **Ship S3.T2 and S3.T3 as one combined ticket** to guarantee the
  fan-out is designed for both callers at once: rejected in decision 15 —
  the fan-out's shape is small and precisely specifiable in this ADR, so
  splitting the *tickets* doesn't reintroduce the "hardcoded then
  refactored" risk that motivated combining them; it would only enlarge a
  single commit's blast radius and mix two independent state machines'
  tests into one Red-Green-Refactor cycle.
