# 06: Circuit Breaker

**What to build:** A per-backend closed/open/half-open circuit breaker,
state living directly on `Backend`, gating dispatch via a pre-`IncActive()`
`Allow()` check, registered as an observer into the round-trip fan-out.
Carries the `Registry.Healthy()` → `Registry.Selectable()` rename, since
that rename's justification doesn't exist before circuit state does.

**Blocked by:** 02 (needs `RoundTripObserver`/`Proxy.RegisterObserver`), 03
(needs the circuit-cooldown config field)

**Status:** ready-for-agent

- [ ] Circuit state (closed/open/half-open enum, consecutive-failure count,
      opened-at timestamp, half-open-trial-in-flight flag) is defined and
      stored **in `internal/backend`**, not `internal/circuit` — unexported,
      CAS-guarded fields reached only through exported methods, so
      `internal/backend` never needs to import `internal/circuit` (the
      package graph has `circuit` depending on `backend`, never the
      reverse)
- [ ] `internal/circuit` holds the policy: consecutive-failure-to-open
      threshold (unexported Go constant) and cooldown duration (read from
      `Config`, per ticket 03); exposes an `Allow(b *backend.Backend)
      bool`-equivalent and implements `RoundTripObserver` to drive
      `Backend`'s state via its methods
- [ ] Circuit's failure counter is **consecutive** — reset to zero on any
      success — distinct from passive detection's sliding window; watches
      the same signal (5xx responses and `errorHandler` transport failures)
- [ ] Open→Half-Open promotion is lazy and timer-free: any read of circuit
      state checks `time.Since(openedAt) >= cooldown` and CASes
      `Open`→`HalfOpen` on the spot; no background goroutine
- [ ] `main.go` constructs the circuit policy and registers it via
      `Proxy.RegisterObserver` (02's mechanism)
- [ ] `ServeHTTP` calls `Allow(b)` immediately after `Select()` returns and
      before `IncActive()` — same position as the existing
      `ErrNoHealthyBackends` short-circuit; on denial, respond immediately
      (503-shaped, with a distinct WARN log line following the existing
      `errorHandler`-style pattern) without dispatching or touching
      active-connection accounting
- [ ] `Registry.Healthy()` renamed to `Registry.Selectable()`, filtering on
      `b.IsHealthy() && <circuit not open>`; every existing selector call
      site and its tests updated to the new name (mechanical rename, no
      selector logic changes)
- [ ] A half-open backend stays fully `Selectable()` — no pool-membership
      exclusion during half-open; only `Allow()` gates the trial
- [ ] The half-open trial slot uses a real `CompareAndSwap`, not a plain bool
- [ ] A trial resolves to Closed on a single success, back to Open on a
      single failure — no additional threshold inside half-open
- [ ] Test: N consecutive failures opens the circuit; a single success from
      Closed resets the consecutive-failure count to zero
- [ ] Test: `Registry.Selectable()` excludes an open-circuit backend and
      includes a half-open one
- [ ] Test: after the configured cooldown elapses (real `time.Sleep`, ~50ms
      cooldown / ~200ms sleep margin — no fake clock), the circuit is
      observed as half-open
- [ ] Test: `Allow()` admits exactly one concurrent trial during half-open
      and denies a second concurrent one
- [ ] Test: a successful trial closes the circuit; a failed trial reopens it
- [ ] Test: `Allow()` denying a request never increments `ActiveConns`
- [ ] Test: injecting a run of 5xx responses on one backend opens its
      circuit and stops routing to it (the `MILESTONES.md` chaos-test exit
      criterion, at unit-test scale)
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on completion

## Comments
