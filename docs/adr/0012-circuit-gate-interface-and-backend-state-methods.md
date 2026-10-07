# ADR-0012: Circuit breaker: gate interface placement, Registry-mediated admission, and the Backend state API

**Status:** Accepted. Amended by ADR-0013 (the circuit methods return a `CircuitTransition`) and ADR-0017 (`Backend.RearmTrial()` re-arms a trial that a client-gone request held).

## Context

ADR-0011 sets the composition: circuit state lives on `Backend`, promotion from open to half-open is a lazy CAS, the `Allow()` gate runs after `Select()` and before `IncActive()`, the failure count is consecutive, and the observer fan-out is unconditional. Three questions remain:

1. Where is the gate interface, and how does the proxy reach it? `proxy.New(reg, sel)` does not change, and `backend` cannot import `circuit`.
2. How does `Registry.Selectable()` read circuit state, when `Registry` does not know the cooldown?
3. What are the circuit methods on `Backend`?

## Decision

### Gate placement and admission

1. **`CircuitGate` is a consumer-defined interface in `internal/backend`, and `Registry` controls eligibility and admission.**

   ```go
   type CircuitGate interface {
       Open(b *Backend) bool
       Allow(b *Backend) bool
   }
   ```

   `internal/app` calls `Registry.SetCircuitGate` once before serving. `Registry.Selectable()` keeps a backend when `b.IsHealthy() && !gate.Open(b)`. `Registry.Allow(b)` calls `gate.Allow(b)`. With no gate, both methods pass everything through. `Proxy.ServeHTTP` calls `p.reg.Allow(b)`, so the proxy needs no new dependency. `circuit.Breaker` implements `CircuitGate` and is also a `RoundTripObserver`. The gate has two methods because the questions differ. `Open` asks "may this backend be in the pool?" and must not take the trial. `Allow` asks "may this request go out?" and takes the trial in half-open. With one method, each `Select` would use up the single trial.
2. **A half-open backend is selectable.** `gate.Open` is true only in the open state. The trial is a real selected request, not a synthetic probe, so the recovery path from ADR-0011 stays reachable.

### Backend circuit API

3. **The whole circuit state is one immutable snapshot behind `atomic.Pointer[circuitSnapshot]`, replaced with one CAS.** The snapshot holds the state, the consecutive-failure count, the trial flag and `openedAt`. A separate atomic for `openedAt` could race with the state change. One snapshot makes each compound change (open, close, promote, take trial) one compare-and-swap. A `nil` pointer reads as closed, so `&Backend{}` needs no setup.
4. **Policy comes in as arguments. `Backend` stores only state.** The methods are:

   - `CircuitOpen(cooldown time.Duration) bool`: promote if due, then report.
   - `CircuitAllow(cooldown time.Duration) (bool, CircuitTransition)`: promote if due, then admit. Closed admits. Open denies. Half-open does a CAS on the trial flag and admits only the winner.
   - `CircuitSuccess() CircuitTransition`
   - `CircuitFailure(threshold int) CircuitTransition`

   `circuit.Breaker` passes `cooldown` and `threshold` on each call, so `circuit` is the only package that reads `Config.Circuit.Cooldown` and holds the failure threshold. The returned `CircuitTransition` lets the breaker log and count only real transitions (ADR-0013).
5. **Outcomes in the open state are ignored.** A request admitted while closed can finish after the circuit opens. Its late success must not close the circuit early, and its late failure must not reopen an open circuit. In the closed state, a success resets the count. In the half-open state, a success closes the circuit, and a failure reopens it with a new `openedAt`.

### Threshold and a known asymmetry

6. **The failure threshold is `circuitFailuresBeforeOpen = 3`**, an unexported constant. It matches the three consecutive probe failures that `internal/health` uses for "down". Three is the smallest count that is a trend and not one bad request.
7. **`ConsistentHashBoundedLoads` checks health, removal and capacity on its walk, not circuit state.** All other selectors use `Registry.Selectable()` and never return an open-circuit backend. The consistent-hash walk can return one. The proxy's `Allow()` then answers 503. This is also why the denial path occurs outside a half-open race. Tests cover it through this selector.

## Consequences

- The package graph stays acyclic. `circuit` depends only on `backend`, and it satisfies `proxy.RoundTripObserver` structurally.
- `proxy.New`, the `Selector` interface and `balancer` do not change because of the gate.
- `Selectable()` and `Allow()` use the same promotion path, so selection and admission cannot disagree about an open circuit.
- `Backend` and `Registry` have more exported methods. Method-only access is a convention that Go cannot enforce.
- A consistent-hash request to an open-circuit backend gets a 503. It is not sent to another backend (ADR-0018).
- `SetCircuitGate` must run before serving, the same rule as `Proxy.RegisterObserver`.
- Known limitation: a request admitted while closed that finishes while half-open looks the same as the trial. `RoundTripObserver` has no trial marker, so this late outcome can resolve the trial early. The window is short. A fix needs a change to the observer interface.
- A cooldown stored on `Backend` was not used. It copies a config value into the state holder.
- `Proxy.RegisterGate(g)` was not used. The proxy already holds the registry, which already controls eligibility.
- A mutex around transitions was not used. The snapshot CAS needs no critical section and keeps reads lock-free.
- A per-backend threshold or cooldown was not added. No need exists yet.
