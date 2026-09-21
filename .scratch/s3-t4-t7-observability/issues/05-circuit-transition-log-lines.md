# 05: Circuit Transition Log Lines

**What to build:** `Backend`'s circuit methods gain a way to report whether
a given call actually changed the circuit's state, and `circuit.Breaker`
uses that to log exactly one line per genuine open/close/half-open
transition. Verifiable entirely through captured log output; no metrics
involvement. Includes the accepted, documented limitation around
`Selectable()`-scan-won promotions.

**Blocked by:** 03 (needs the `event`/`reason` vocabulary)

**Status:** done

- [x] New exported `backend.CircuitTransition` type (`NoChange`, `Opened`,
      `Closed`, `HalfOpened`, **plus `Reopened`** — see Comments)
- [x] `Backend.CircuitFailure`, `Backend.CircuitSuccess`, and
      `Backend.CircuitAllow` each return a `CircuitTransition` value
      alongside their existing return value, reflecting whether *this*
      call was the one that changed state
- [x] `internal/backend` gains **no** new import — `CircuitTransition` is a
      plain type, not a logging call
- [x] `circuit.New` gains a `*slog.Logger` parameter; `circuit.Breaker`
      logs at its own `ObserveRoundTrip`/`Allow` call sites whenever the
      returned `CircuitTransition` is not `NoChange`
- [x] The `backend.CircuitGate` and `proxy.RoundTripObserver` interface
      signatures `Breaker` implements are unchanged — `Breaker`'s wrapper
      methods absorb the extra return value internally
- [x] Logs `event=circuit_opened, reason=consecutive_failures` at WARN;
      `event=circuit_closed, reason=trial_success` at INFO;
      `event=circuit_half_opened, reason=cooldown_elapsed` at INFO;
      `event=circuit_opened, reason=trial_failure` at WARN — each
      including the `backend` field
- [x] Documented (code comment plus a note carried into this batch's
      eventual ADR): a Half-Open promotion whose CAS is won by a
      `Registry.Selectable()` scan is **never** logged — permanently, not
      delayed — because `Backend.CircuitOpen()` (the method `Selectable()`
      calls) has no path to a logger without a cross-package plumbing
      change out of scope here
- [x] Test: `CircuitTransition` return values match the actual state
      change for each of `CircuitFailure`/`CircuitSuccess`/`CircuitAllow`,
      and are `NoChange` when the call doesn't change anything (e.g. a
      second consecutive failure while already `Open`)
- [x] Test: a run of failures while already `Open` produces **no**
      additional `circuit_opened` log line (exactly once, not once per
      failure) — mirroring the outlier detector's exactly-once property
- [x] Test: the full closed→open→half-open→closed (and →open) cycle each
      produces exactly one log line at the correct transition, with the
      correct `event`/`reason` pair
- [x] Test: every existing S3.T3 test continues to pass unchanged
- [x] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments

Completed 2026-09-21 by opencode (S3.T5.4).

**`CircuitTransition` gained a fifth value, `Reopened`, approved by the owner
during this session.** The ticket (and ADR-0013 decision 10) named four values
(`NoChange`/`Opened`/`Closed`/`HalfOpened`), but the ticket's own log
requirement has four distinct circuit reasons, two of which — `consecutive_
failures` (`Closed→Open`) and `trial_failure` (`HalfOpen→Open`) — both leave
the circuit `Open`. A four-value enum reporting only the resulting state cannot
tell `circuit.Breaker` which reason to log. ADR-0013's phrase
"`circuit_half_opened`-via-`trial_failure`" was the same inconsistency: a
failed trial reopens (`circuit_opened`), it does not half-open. Resolved by
adding `Reopened` for the trial-failure case so each logged reason maps 1:1 to
a transition; ADR-0013 decision 10 was amended in place and a dated
"Amendment" section records the change and the corrected decision 12 WARN
list. (The Go constants are exported with a `Circuit` prefix —
`backend.CircuitNoChange`, `CircuitOpened`, `CircuitReopened`, `CircuitClosed`,
`CircuitHalfOpened` — to avoid generic package-scope names like
`backend.Opened`; the bare names above are shorthand.) ADR-0012 decision 4
gained a pointer to the extended signatures, since it is the ADR that froze
them.

`CircuitAllow` now returns `(bool, CircuitTransition)`; `CircuitFailure` and
`CircuitSuccess` return `CircuitTransition`. `CircuitAllow` reports
`CircuitHalfOpened` when *that call* performed the `Open→Half-Open` promotion
(the promotion CAS has one winner, so exactly one concurrent caller reports it
even if it then loses the trial-slot CAS — tying the report to the promotion,
not to winning the trial, keeps the exactly-once guarantee under a race), so
the scan-won-promotion gap is preserved exactly: a promotion won by
`Registry.Selectable()`'s `CircuitOpen` read is never logged. `internal/backend`
gained no import; `circuit.New(cooldown, log)` gained the logger; the
`CircuitGate`/`RoundTripObserver` signatures are untouched (the wrapper methods
absorb the transition).

Tests: five new `Backend` tests for the transition returns (including the
scan-won-promotion case) and five new `circuit` buffered-`slog.JSONHandler`
tests asserting exactly-once logging per cycle, no re-log for a sustained
failure run while `Open`, the WARN/INFO levels, the `event`/`reason` pairs, and
the documented gap. Existing S3.T3 assertions are unchanged — only the
`CircuitAllow` and `circuit.New` call sites were updated mechanically for the
new arity (`circuitAllow` test helper; `discardLogger()`/`slog.Default()`).
`go test -cover` → circuit 100.0%, backend 98.9%. No new ADR number: ADR-0013
decision 10/12 covers this work and was amended in place.
