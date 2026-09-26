# ADR-0016: Drain lifecycle — retired-context join, two-phase drain, and cancel-at-window

- **Status**: Accepted
- **Date**: 2026-09-26
- **Deciders**: Darshan Jain (project owner) + opencode agent (S4.T4.0, design session recorded in `.scratch/s4-t0-t4-reload/spec.md`)

## Context

ADR-0015 makes the backend set replaceable while traffic flows: a reload marks
removed backends retired from selection before the snapshot swap, but their
in-flight requests are deliberately left alone — S4.T3's hand-off records that
a removed backend's in-flight requests finish with **no bound**, and its
active-connections metric series stays in place because those requests still
decrement it by name. Sprint 4's first exit criterion is "SIGHUP with 1000
in-flight requests drops zero": the bound must let everything finish in the
normal case, yet a stuck backend must not be able to hold a request forever.

A drain needs one mechanism the request path can observe: a per-backend signal
that cancels exactly that backend's in-flight requests on demand, joined into
each outbound request without a goroutine, a mutex, or a registry entry per
request. S4.T4.0 builds that mechanism and its config; S4.T4 adds the goroutine
that decides *when* to fire it. This ADR is written before the T4.0 code, per
the ADR-0015 precedent, and covers both tickets' design so T4 has a fixed
contract to implement.

## Decision

### The drain window is configuration

1. **`reload.drain_window` is a top-level `reload:` block holding one pointer
   duration, defaulting to 30s when omitted and rejecting zero or negative at
   load time with the field named.** It follows the nil-means-omitted
   convention `health`/`circuit`/`metrics` established (ADR-0011 decision 10,
   ADR-0013): a plain duration cannot distinguish "key absent" from "key set to
   zero", so the pointer is what lets `Validate` default the former and reject
   the latter. It is a global knob like the other timing values. It is **not
   reloadable**: ADR-0015 decision 4 rejected any non-backend change whole, and
   `NonBackendChanges` is extended to name `reload` — a running drain's timer is
   already armed, so silently accepting a new window would make the reload's
   effect invisible, exactly the failure decision 4 exists to prevent.
   `configs/example.yaml` documents the block at its default.

### One retired context per backend

2. **`Backend` owns an unexported retired context, created at construction,
   cancelled only by `Backend.Retire()`, and observed through
   `Backend.RetiredContext()`.** This is a method-only addition in the
   ADR-0006/0010/0012/0015 style, amending ADR-0002 decision 5 again: no caller
   outside `internal/backend` touches the field, so the representation can
   change. `Retire` is idempotent (`context.CancelCauseFunc` is) and cancels the
   context with the exported cause `ErrDrainWindowExpired`. A `*Backend` built
   by a bare literal has a nil context; the accessor returns
   `context.Background()` and `Retire` is a no-op for it, so the proxy's join is
   total and test fixtures that construct a `Backend` directly keep working. In
   production every instance comes from `newBackend`, so every selectable
   backend has a real retired context.

   Why a context rather than a cancel-func or a done channel: `context` is the
   stdlib idiom for "work scoped to a lifetime", it composes with the request's
   own client-cancellation context, and `context.AfterFunc` is the one stdlib
   primitive that registers a callback on cancellation with **no goroutine while
   the context is live**. A bare `chan struct{}` would force the proxy to spawn
   a goroutine per request (or hand-roll a select loop) to join two completion
   signals — the exact cost this design avoids.

### The proxy joins, and stops the join on completion

3. **`ServeHTTP` derives the outbound request context with
   `context.WithCancelCause`, then registers a `context.AfterFunc` on the
   chosen backend's retired context that cancels the outbound context with the
   retirement's cause; both the cancel and the registration's stop function run
   when the request completes.** The join is therefore two calls and one
   `defer`/`stop` per request, no goroutine, and no mutex-guarded cancel-func
   registry. The stop is what prevents a long-lived backend accumulating one
   dead callback per completed request: `context.AfterFunc` holds the callback
   in the context until it fires or is stopped.

   The cancellation *cause* is the point of using the cause-carrying context:
   the proxy can distinguish a drain cancellation (`ErrDrainWindowExpired`) from
   a client cancellation (`context.Canceled`) or a real transport failure, and
   the error handler can attribute the right reason without guessing from the
   error text.

### What a drain cancellation looks like

4. **Before response headers: the request fails with 502, its active-connection
   slot is released, and one WARN line carries reason `window_expired`.** The
   cancellation surfaces through `httputil.ReverseProxy`'s `ErrorHandler`
   exactly as a transport failure does, so the existing release path runs
   unchanged; the handler reads the cancellation cause and adds
   `reason=window_expired`. The `window_expired` value is a new
   `logger.Reason*` constant — vocabulary lands with its first producer
   (ADR-0013 decision 12).

5. **Observers are not called for a drain cancellation, because the backend is
   already removed.** A backend is retired only after a reload removed it
   (ADR-0015 decision 7), and ADR-0015 decision 8 already suppresses the whole
   observer fan-out for a removed backend — normal completions, transport
   failures, and client cancellations alike. A drain cancellation is just
   another transport failure on that request, so it inherits the suppression
   with no new rule: no circuit, outlier, health, or gauge write, and the
   cancelled request counts as **no backend's failure**. This is deliberate —
   the suppression check is `IsRemoved()`, not the cancellation cause, so the
   observers stay ignorant of draining exactly as they stay ignorant of reload.

6. **After response headers: the client gets a truncated body and the success
   already recorded when the headers arrived stands.** `ModifyResponse` has
   already run (status recorded, observer outcome recorded as a success, body
   wrapper installed). Cancelling the outbound context mid-copy makes
   `ReverseProxy` abort the stream — the client sees a short body, not a
   buffered error — and no second observer outcome or failure is recorded. The
   active-connection slot still releases via the body wrapper's `Close`. This
   asymmetry is the honest semantics of a bounded drain: a backend that really
   did answer is not recorded as failing, and the client's truncation is the
   observable cost.

### The drain itself (S4.T4)

7. **Reload starts one drain goroutine per removed backend. It waits for
   whichever comes first: `ActiveConns` reaching zero (polled on a ~100ms
   ticker) or the drain window timer.** Phase one is "finish normally". On idle
   it deletes the backend's active-connections series only if no current
   backend shares the name, logs one `backend drained` event with reason
   `idle`, and exits. Polling is chosen over a completion channel because the
   request path must pay nothing for drain bookkeeping — `DecActive` is an
   atomic add and nothing else (ADR-0015 decision 8's "accounting is not
   observer-driven"). Phase two, on window expiry, calls `Backend.Retire()`
   (which fires the joins built in decision 3), waits for `ActiveConns` to
   reach zero, then deletes the series and logs `backend drained` with reason
   `window_expired` and the number of requests cancelled.

8. **Drain goroutines select on the process shutdown context and exit
   immediately without cleanup.** Shutdown timing is unchanged: there is no
   deferred work a drain can block on, and the existing server shutdown grace
   period covers in-flight requests. A drain that would have finished after
   shutdown does not matter; the process is ending.

9. **A drain is independent of later reloads.** It is keyed to the removed
   `*Backend` instance, not its name. Re-adding the same identity constructs a
   fresh instance (ADR-0015 decision 6), so a later reload neither revives nor
   ends the draining one, and the fresh same-name backend's state reflects only
   its own traffic. This is why decision 7 deletes the active-connections
   series only when no current backend shares the name: the fresh instance has
   its own series from the moment it is added, and deleting the old instance's
   series must not wipe the new one's.

## Consequences

- Positive: the request path pays one `context.WithCancelCause`, one
  `context.AfterFunc`, and two deferred calls per request — no goroutine, no
  mutex, no per-request registry entry — while still giving a per-backend
  cut-off signal.
- Positive: a drain cancellation reuses the existing `ErrorHandler` release
  path and the existing removed-backend suppression, so no new observer rule is
  introduced and "not a backend failure" is structural rather than a special
  case.
- Positive: using the cancellation cause, not the error string, to pick the
  `window_expired` reason keeps the reason machine-checkable and greppable.
- Negative: `Backend`'s exported surface grows once more (`Retire`,
  `RetiredContext`) and `ErrDrainWindowExpired`, another method-only convention
  rather than a type-enforced one (the limitation ADR-0006, ADR-0011, and
  ADR-0015 each accepted).
- Negative: the after-headers case is a truncated body, not a clean error.
  Accepted as the literal bound's semantics (decision 6): bufferable responses
  will already have been written, streaming ones get cut.
- Negative: a nil retired context (a directly-constructed `Backend` in a test)
  makes `Retire` a no-op, so a fixture that wants cancellation must build its
  backend through `NewRegistry`. Documented rather than made impossible, so
  every existing zero-value fixture keeps compiling.

## Alternatives considered

- **A goroutine per request waiting on a per-backend done channel**: rejected in
  decision 3 — one goroutine per in-flight request is the cost this design
  exists to avoid; `context.AfterFunc` gives the same wake-up with none.
- **A mutex-guarded registry of per-request cancel functions on the backend**:
  rejected in decision 3 — the request path would take a lock and register/
  deregister on every request, and the backend would hold a map whose lifetime
  is every live request. The context already is that registry, implemented
  lock-free by the stdlib.
- **Cancel the outbound request on a backend done channel with `select` around
  `RoundTrip`**: rejected — `httputil.ReverseProxy` owns the round trip; there
  is no seam to race it against a second signal except by cancelling the
  request context, which is what decision 3 does directly.
- **Store an unexported cancel func and expose `CancelInFlight()` without a
  context**: rejected in decision 2 — the proxy still needs a context to join
  via `AfterFunc`, and a done channel would reintroduce the goroutine of the
  first alternative; the context is the only stdlib object that supports both
  the join and the cause.
- **Notify drain completion through the observer fan-out (a `DecActive`
  hook)**: rejected in decision 7 — it would add a per-request branch to the hot
  path to serve an off-path bookkeeping need; a 100ms poll over a small backend
  set is free by comparison, and the request path stays untouched.
- **Reclassify client cancellation and drain cancellation in the same change**:
  out of scope — the client-cancellation misclassification is a recorded
  proposed ticket belonging to Sprint 4's connection-lifecycle deliverable.
  This ADR's cause mechanism is the tool that ticket will reuse, but T4.0
  changes only the drain reason.
- **Buffer the response so a drain produces a clean error instead of a
  truncation**: rejected — buffering every response to make an uncommon case
  tidier would regress streaming, the property ADR-0007 protects by leaving
  `ResponseWriter` unwrapped.
