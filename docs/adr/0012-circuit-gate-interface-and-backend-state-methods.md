# ADR-0012: Circuit breaker — gate interface placement, Registry-mediated admission, and the Backend state API

- **Status**: Accepted
- **Date**: 2026-09-20
- **Deciders**: Darshan Jain (project owner) + opencode agent (S3.T3)

## Context

[ADR-0011](0011-health-passive-outlier-and-circuit-breaker-composition.md)
decided the *composition* of Sprint 3's three subsystems: circuit state lives
on `Backend` (decision 5), Open→Half-Open promotion is a lazy read-time CAS
(decision 6), the breaker's `Allow()` gate runs after `Select()` and before
`IncActive()` (decision 7), the failure counter is consecutive (decision 8),
and the proxy fan-out is unconditional (decision 9). S3.T3 builds that circuit
breaker.

Three questions ADR-0011 deliberately left open have to be answered before a
line of code is written, because each is a package-boundary or API-shape
decision that is expensive to reverse:

1. **Where does the gate interface go, and how does the proxy reach it?**
   `Proxy.New(reg, sel)`'s two-argument signature is frozen and the package
   graph forbids `backend` importing `circuit`. The circuit policy must own the
   cooldown, and `ServeHTTP` must call `Allow(b)`; nothing yet connects them.
2. **How does `Registry.Selectable()` reach circuit state?** ADR-0011 decision
   1 says it filters on `healthy AND circuit-not-open`, and decision 6 says
   both it and `Allow()` share the same lazy promotion path — but `Registry`
   does not have the cooldown.
3. **What is `Backend`'s circuit-state method API?** ADR-0011 decision 5 says
   the fields are unexported and reached only through exported methods, but the
   method names, signatures, and transition representation are unspecified.

## Decision

### Gate placement and Registry-mediated admission

1. **`CircuitGate` is a consumer-defined interface in `internal/backend`, and
   `Registry` mediates both eligibility and admission.** The package graph has
   `circuit` → `backend` and never the reverse, so `backend` cannot name
   `circuit.Breaker`; an interface defined where it is consumed is the
   idiomatic Go answer and keeps the graph acyclic:

   ```go
   // in internal/backend
   type CircuitGate interface {
       Open(b *Backend) bool
       Allow(b *Backend) bool
   }
   ```

   `Registry` gains an optional gate (`SetCircuitGate`, called once by `main`
   before serving), `Registry.Selectable()` filters
   `b.IsHealthy() && !gate.Open(b)`, and `Registry.Allow(b)` delegates to
   `gate.Allow(b)` (both are pass-throughs when no gate is set, so a bare
   `NewRegistry` behaves exactly as before). `Proxy.ServeHTTP` calls
   `p.reg.Allow(b)` — it already holds `p.reg`, so the frozen `New(reg, sel)`
   signature and `Proxy`'s exported surface are untouched. `circuit.Breaker`
   implements `CircuitGate`; `main` both sets it as the registry's gate and
   registers it as the proxy's `RoundTripObserver` (ADR-0011 decision 9).

   The gate is two methods, not one, because selection and admission are
   different questions: `Open` asks "may this backend be in the pool at all"
   (and must not take the half-open trial), while `Allow` asks "may this
   specific request dispatch" (and does take it). Collapsing them would make
   every `Select` consume the single trial.

2. **A half-open backend is `Selectable()`; only `Open` is excluded.**
   `gate.Open` returns true only for `Open`, so `Selectable()` excludes a
   backend exactly while its circuit is open and includes it the instant the
   lazy promotion flips it to `HalfOpen`. This is what keeps ADR-0011
   decision 1's recovery path reachable: the trial is a real selected request,
   not a synthetic probe.

### Backend circuit-state API

3. **The whole circuit state is one immutable snapshot behind
   `atomic.Pointer[circuitSnapshot]`, replaced by CAS as a unit.** The snapshot
   holds the state enum, consecutive-failure count, half-open-trial flag, and
   opened-at timestamp. Packing them into one CAS-able value is what avoids a
   separate opened-at atomic racing the state transition (store the timestamp,
   lose the CAS, overwrite a winner's timestamp), and it makes every compound
   transition — open, close, promote, take trial — a single compare-and-swap,
   which is exactly the "real `CompareAndSwap`, not a plain bool" ADR-0011
   decision 6 requires for the trial slot. The zero value (`nil`) reads as a
   closed circuit, so a plain `&Backend{}` needs no initialization.

4. **Policy flows *into* `Backend`'s methods as arguments; it does not live on
   `Backend`.** The methods are:

   - `CircuitOpen(cooldown time.Duration) bool` — lazy promotion, then report.
   - `CircuitAllow(cooldown time.Duration) bool` — lazy promotion, then admit
     (Closed → true; Open → false; HalfOpen → CAS the trial flag, true for the
     winner, false for everyone else).
   - `CircuitSuccess()`
   - `CircuitFailure(threshold int)`

   S3.T5.4 later extended `CircuitAllow` to
   `(bool, backend.CircuitTransition)`, and `CircuitSuccess`/`CircuitFailure`
   to return a `backend.CircuitTransition`, so `circuit.Breaker` can log
   exactly the genuine transitions; the argument lists above are otherwise
   unchanged. See ADR-0013 decision 10 and its 2026-09-21 amendment.

   `cooldown` and `threshold` are supplied by `circuit.Breaker` on every call.
   This keeps ADR-0011 decision 10's split literal: `circuit` is the only
   package that reads `Config.Circuit.Cooldown` and the only place the
   failure-to-open constant lives; `Backend` stores state, not tuning. A
   cooldown-on-`Backend` design was rejected because it would put a config
   value in the state holder and leave two copies to keep in sync.

5. **A success or failure observed while `Open` is ignored; a success while
   `Closed` resets the consecutive counter; only a `HalfOpen` outcome resolves
   the trial.** A request admitted while `Closed` can still be in flight when
   the circuit opens on other requests' failures. If its (stale) success closed
   the circuit, it would bypass the cooldown ADR-0011 decision 6 exists to
   enforce; if its stale failure counted, it would open an already-open
   circuit. Ignoring outcomes in `Open` is the only branch that cannot violate
   either invariant. In `Closed`, any success zeroes the consecutive count
   (ADR-0011 decision 8); in `HalfOpen`, a success closes and a failure
   reopens with a fresh `openedAt`, with no inner threshold (decision 6's
   single-trial semantics).

### Policy constant and a known selection asymmetry

6. **The consecutive-failure-to-open threshold is the unexported constant
   `circuitFailuresBeforeOpen = 3`, matching the active checker's three
   consecutive probe failures.** ADR-0011 decision 8 assigns the circuit the
   "strict and fast" posture and decision 10 fixes it as a Go constant without
   fixing a value; three is the smallest count that is clearly a trend rather
   than one bad request, and it is the same number the project already trusts
   for "consecutive failures mean down" in `internal/health`.

7. **`ConsistentHashBoundedLoads`' ring walk keeps gating on `IsHealthy()`,
   so `Allow()` remains a real gate for that selector.** Every other selector
   iterates `Registry.Selectable()` and therefore never returns a circuit-open
   backend. The consistent-hash bounded-loads walk deliberately gates
   candidates on health plus capacity (ADR-0009), not on `Selectable()`, so it
   can still return a circuit-open backend as its only candidate; the proxy's
   `Allow()` check then answers 503. This is accepted, not fixed here: changing
   the walk is a selector-logic change and S3.T3 is explicitly a mechanical
   rename of selector call sites. It is also why the `Allow()` denial path is
   reachable at all outside a half-open trial race, and the S3.T3 tests cover
   it through that selector.

## Consequences

- Positive: the package graph stays acyclic with `backend` defining the
  interface it consumes; `circuit` depends only on `backend` (no edge to
  `proxy` — it satisfies `proxy.RoundTripObserver` structurally).
- Positive: `Proxy`'s frozen `New` signature, the selector `Selector`
  interface, and the `balancer` package are all untouched by the gate; the
  only change reaching them is the `Healthy()` → `Selectable()` rename.
- Positive: `Selectable()` and `Allow()` share one promotion path because both
  call the same `circuit.Breaker` methods, so selection and admission cannot
  disagree about whether a circuit is open.
- Negative: `Backend`'s exported surface grows by four circuit methods, and
  `Registry` gains `SetCircuitGate`/`Allow`; like `SetHealthy` (ADR-0006),
  method-only access is a convention Go cannot enforce.
- Negative: a circuit-open backend selected by the consistent-hash walk is
  answered 503 rather than rerouted to another healthy backend (decision 7);
  rerouting is the explicitly-deferred Sprint 4 retry-policy question, not a
  circuit concern.
- Neutral: `SetCircuitGate` must be called before serving (one write, then a
  read-only field), the same construction-time contract as
  `Proxy.RegisterObserver`.
- Neutral: circuit state cannot be promoted without a read, so a backend
  receiving zero traffic after its cooldown elapses is not observed to recover
  until traffic reaches it — the accepted consequence ADR-0011 decision 6
  already records.
- Negative (known limitation): a request admitted while `Closed` whose response
  arrives while the circuit is `Half-Open` cannot be distinguished from the
  trial that `Allow` admitted, because the frozen three-argument
  `RoundTripObserver` carries no per-request trial marker. Such a stale outcome
  may resolve the trial early. Outcomes observed while `Open` are already
  ignored, so the exposure is the narrow window between promotion and the
  trial's own result; closing it fully would require threading a trial
  indicator from `Allow` to the observer, which would change the interface
  ADR-0011 decision 9 froze. Accepted as a bounded edge rather than solved with
  speculative state.

## Alternatives considered

- **Store the cooldown on `Backend` and let `Selectable()` call a parameterless
  `b.CircuitOpen()`:** rejected in decision 4 — duplicates a config value into
  the state holder and makes `Backend` the owner of tuning rather than state.
- **Add `Proxy.RegisterGate(g)` so the proxy calls `circuit.Allow` directly:**
  rejected — a second additive registration method for a dependency `Proxy`
  already has a handle to (`p.reg`), and it would put circuit admission in the
  proxy's wiring rather than behind the registry that already owns backend
  eligibility. `Registry.Allow` keeps the frozen `New` signature and the
  consumer-defined-interface pattern the fan-out uses.
- **Fold `Open` and `Allow` into one gate method:** rejected in decision 1 —
  selection would consume the half-open trial, making recovery impossible.
- **Make the circuit breaker also write `Backend.healthy`:** already rejected
  by ADR-0011 decisions 1–2; recorded here only because S3.T3 is where the
  temptation first appears in code.
- **A `map[*Backend]state` inside `internal/circuit`:** already rejected by
  ADR-0011 decision 5 — a second concurrency story Sprint 4's reload would have
  to reconcile separately.
- **A mutex around the state transitions instead of CAS:** rejected — the
  contracts doc's Sprint 1 guess ("likely mutex") was superseded by ADR-0011
  decision 6, and an immutable-snapshot CAS loop needs no critical section for
  the trial slot while keeping the read path lock-free.
- **A configurable failure threshold or cooldown per backend:** deferred by
  ADR-0011 decision 10; both are one-line changes if a real need appears.
