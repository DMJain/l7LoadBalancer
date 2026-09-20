# 05: Circuit Transition Log Lines

**What to build:** `Backend`'s circuit methods gain a way to report whether
a given call actually changed the circuit's state, and `circuit.Breaker`
uses that to log exactly one line per genuine open/close/half-open
transition. Verifiable entirely through captured log output; no metrics
involvement. Includes the accepted, documented limitation around
`Selectable()`-scan-won promotions.

**Blocked by:** 03 (needs the `event`/`reason` vocabulary)

**Status:** ready-for-agent

- [ ] New exported `backend.CircuitTransition` type (`NoChange`, `Opened`,
      `Closed`, `HalfOpened`)
- [ ] `Backend.CircuitFailure`, `Backend.CircuitSuccess`, and
      `Backend.CircuitAllow` each return a `CircuitTransition` value
      alongside their existing return value, reflecting whether *this*
      call was the one that changed state
- [ ] `internal/backend` gains **no** new import — `CircuitTransition` is a
      plain type, not a logging call
- [ ] `circuit.New` gains a `*slog.Logger` parameter; `circuit.Breaker`
      logs at its own `ObserveRoundTrip`/`Allow` call sites whenever the
      returned `CircuitTransition` is not `NoChange`
- [ ] The `backend.CircuitGate` and `proxy.RoundTripObserver` interface
      signatures `Breaker` implements are unchanged — `Breaker`'s wrapper
      methods absorb the extra return value internally
- [ ] Logs `event=circuit_opened, reason=consecutive_failures` at WARN;
      `event=circuit_closed, reason=trial_success` at INFO;
      `event=circuit_half_opened, reason=cooldown_elapsed` at INFO;
      `event=circuit_opened, reason=trial_failure` at WARN — each
      including the `backend` field
- [ ] Documented (code comment plus a note carried into this batch's
      eventual ADR): a Half-Open promotion whose CAS is won by a
      `Registry.Selectable()` scan is **never** logged — permanently, not
      delayed — because `Backend.CircuitOpen()` (the method `Selectable()`
      calls) has no path to a logger without a cross-package plumbing
      change out of scope here
- [ ] Test: `CircuitTransition` return values match the actual state
      change for each of `CircuitFailure`/`CircuitSuccess`/`CircuitAllow`,
      and are `NoChange` when the call doesn't change anything (e.g. a
      second consecutive failure while already `Open`)
- [ ] Test: a run of failures while already `Open` produces **no**
      additional `circuit_opened` log line (exactly once, not once per
      failure) — mirroring the outlier detector's exactly-once property
- [ ] Test: the full closed→open→half-open→closed (and →open) cycle each
      produces exactly one log line at the correct transition, with the
      correct `event`/`reason` pair
- [ ] Test: every existing S3.T3 test continues to pass unchanged
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments
