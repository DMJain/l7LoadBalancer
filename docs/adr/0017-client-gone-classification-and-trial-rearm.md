# ADR-0017: Client-gone classification and half-open trial re-arm

- **Status**: Accepted
- **Date**: 2026-09-26
- **Deciders**: Darshan Jain (project owner) + opencode agent (S4.T5)

## Context

S4.T5 fixes a misclassification in the proxy's error handler: a client that
disconnects mid-request currently looks exactly like a backend failure, so the
circuit breaker, passive outlier detector, and EWMA latency all record a
backend failure that never happened. The bundle spec
(`.scratch/s4-t5-t10-connection-lifecycle/spec.md`, §"S4.T5") prescribes the
fix: classify every failed round trip into three tiers — drain cancellation,
client-gone, genuine transport failure — using the client's own request context
as the discriminator, and suppress the observer fan-out for client-gone.

Two decisions fall out of that prescription that ADR-0007 (proxy request
lifecycle) and ADR-0012 (circuit breaker state and admission) do not cover:

1. **Client-gone changes the error handler's frozen response shape.** ADR-0007
   decision 4 states `ErrorHandler` records 502 and decision 5 states it logs
   the cause at WARN. Client-gone must instead record 499 (`status_class="4xx"`)
   and log at INFO, so client churn never reads as a backend failure. ADR-0007
   is amended, not silently contradicted.

2. **Suppressing the fan-out can wedge a half-open circuit.** A half-open
   circuit's `CircuitAllow` admits exactly one trial request and sets a `trial`
   flag; the trial is resolved only by an outcome fed back through the observer
   fan-out. If the trial request is client-gone and the fan-out is suppressed,
   the `trial` flag is never cleared. While it is set, `CircuitAllow` denies
   every other request, so the backend stays selectable, receives no probe, and
   answers 503 forever — a permanent, silent outage of a live backend. Before
   S4.T5 a cancelled trial was recorded as a failure and reopened the circuit,
   so the proposal introduces this regression; it must not ship.

## Decision

### 1. Three-tier classification in the error handler (amends ADR-0007)

`errorHandler` classifies a failed round trip into exactly one of three tiers,
in this order:

1. **Drain cancellation** — `context.Cause(r.Context())` is
   `ErrDrainWindowExpired` (unchanged from ADR-0016 decision 4): WARN,
   `reason=window_expired`, no observer (the backend was already removed).
2. **Client-gone** — the captured client request context's `Err() != nil`:
   INFO, `reason=client_canceled`, no observer, no EWMA latency, and the
   response recorder gets **499** (nginx's client-closed-request code). The
   whole-request counter records 499, i.e. `status_class="4xx"`, so client
   churn stays visible while the 5xx class is reserved for backend-caused
   failures.
3. **Genuine transport failure** — the fallback (dial/response-header timeout,
   connection refused): WARN, observers get the fixed 2s penalty, 502
   (unchanged).

The order is the predicate. The transport can cancel the outbound context only
via parent propagation (client context done), the drain after-func, or its own
timers; the drain case is caught by tier 1 and the transport's own timers leave
the client context alive, so a real backend timeout still reaches tier 3.
Context cancellation is sticky, so checking at error-handler time is race-free.
On shutdown the HTTP server cancels in-flight request contexts, so
shutdown-time cancellations classify as client-gone too; that is accepted and
desirable, since suppressing observer writes while the process exits is
correct.

`reqState` gains a `clientCtx` field holding the client request context,
captured in `ServeHTTP` before the cancel-with-cause derivation. Concurrency is
unchanged: `reqState` is created on and touched only by the request goroutine
(ADR-0007).

### 2. `Backend.RearmTrial()` on client-gone (amends ADR-0012)

`Backend` gains one method: `RearmTrial()` clears the half-open trial flag if
and only if the circuit is currently Half-Open with its trial taken; otherwise
it is a no-op. The proxy calls it on the client-gone tier, where no round trip
completed and no observer sees the request. The next request to reach the
backend can then take the trial and actually probe it.

It is deliberately **not** generation-guarded. While a trial is outstanding the
circuit admits no other request, so the only outcome that can interleave is a
stale in-flight request admitted while Closed resolving the trial early — the
same documented stale-while-Half-Open limitation ADR-0012 already accepts. In
that interleaving `RearmTrial` sees a resolved (non-Half-Open) state and does
nothing. A generation token would close a narrower race that requires a stale
request to resolve the trial and the cooldown to elapse again inside the
client-cancellation window (sub-millisecond against a 30s default cooldown);
the extra state is not justified.

`RearmTrial` is a method-only addition to `Backend`, amending ADR-0002 decision
5 in the same style as ADR-0006/0010/0012/0015/0016: no caller outside
`internal/backend` touches the circuit snapshot, so its representation stays
free to change.

## Consequences

- Positive: a client disconnecting mid-request is no longer counted as a
  backend failure — no circuit, outlier, or EWMA write — and shows up as 499 /
  `4xx`, so client churn and backend breakage are distinguishable on a
  dashboard and a 5xx alert always means a backend-caused failure.
- Positive: the half-open trial can no longer be wedged by a cancelled client;
  the re-arm is one CAS with no new goroutine, lock, or per-request bookkeeping.
- Positive: the 499 write to a dead client is a no-op on the wire; it exists for
  the recorder, metrics, and logs.
- Negative: `Backend`'s exported surface grows once more (`RearmTrial`), the
  same method-only convention its last five amendments accepted.
- Negative: `RearmTrial`'s non-generation-guarded release has the theoretical
  stale-request overlap described above, bounded by the circuit cooldown.
  Documented rather than made impossible.
- Neutral: ADR-0007's `ErrorHandler` decision 4/5 are amended for the
  client-gone tier only; the drain and transport tiers keep 502/WARN.

## Alternatives considered

- **Record the cancelled trial's outcome as a failure so the circuit reopens
  (pre-T5 behavior)**: rejected — it counts a client cancellation as a backend
  failure, the exact misclassification S4.T5 exists to remove, and would reopen
  a healthy backend's circuit on client behavior.
- **Record the cancelled trial as a success so the circuit closes**: rejected —
  it closes the circuit on no evidence; a backend that is genuinely broken
  would be admitted until it fails again.
- **Suppress only the latency/outlier observers and still feed the circuit**:
  rejected — the `RoundTripObserver` fan-out is uniform and carries no per-
  observer or per-tier signal, so this would need a new interface and would
  still have to choose success/failure for a round trip that neither succeeded
  nor failed.
- **A generation token on the trial so `RearmTrial` only clears its own
  trial**: rejected as unearned complexity now — the only interleaving it closes
  also requires a cooldown re-elapse inside the cancellation window; revisit if
  the cooldown ever drops to the sub-second range.
- **A per-request "trial release" closure returned from admission**: rejected —
  it would change the frozen `CircuitGate.Allow` / `Registry.Allow` signatures
  (ADR-0012) for a bookkeeping need, when a single method-only `RearmTrial` call
  on the error path suffices.
- **Write `WriteTimeout`-style custom 499 body text**: rejected — the client is
  gone; the status exists for the recorder and metrics, and no response body is
  read.
