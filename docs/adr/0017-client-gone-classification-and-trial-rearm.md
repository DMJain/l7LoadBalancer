# ADR-0017: Client-gone classification and half-open trial re-arm

**Status:** Accepted. Amends ADR-0007 (error handler response) and ADR-0012 (circuit state API).

## Context

A client that disconnects mid-request must not look like a backend failure. Otherwise the circuit breaker, the passive outlier detector and the EWMA latency all record a failure that did not happen.

The fix has two consequences that ADR-0007 and ADR-0012 do not cover:

1. **The error handler's response changes.** ADR-0007 says `ErrorHandler` records 502 and logs at WARN. A client-gone request must record 499 and log at INFO, so client churn never reads as a backend failure.
2. **Skipping the observers can wedge a half-open circuit.** In Half-Open, `CircuitAllow` admits one trial request and sets a `trial` flag. Only an outcome from the observer fan-out clears the flag. If the trial request is client-gone and the fan-out is skipped, the flag stays set. `CircuitAllow` then denies every other request. The backend stays selectable, gets no trial, and answers 503 forever: a silent outage of a live backend.

## Decision

### 1. Three-tier classification in the error handler

`errorHandler` puts each failed round trip into exactly one tier, checked in this order:

| Tier | Test | Log | Observers | Status |
|---|---|---|---|---|
| Drain cancellation | `context.Cause(r.Context())` is `ErrDrainWindowExpired` (ADR-0016) | WARN, `reason=window_expired` | none (backend already removed) | 502 |
| Client-gone | the client request context has `Err() != nil` | INFO, `reason=client_canceled` | none, no EWMA latency | 499 |
| Transport failure | anything else (dial or response-header timeout, connection refused) | WARN | fixed 2s penalty | 502 |

499 is the nginx "client closed request" code. The request counter records it as `status_class="4xx"`. So client churn stays visible, and the 5xx class means a backend-caused failure.

The order is the test. The outbound context is cancelled only by the client context (its parent), by the drain `AfterFunc`, or by the transport's own timers. Tier 1 catches the drain case. The transport's timers leave the client context alive, so a real backend timeout reaches tier 3. Context cancellation is sticky, so the check is race-free. On shutdown the HTTP server cancels in-flight request contexts, so those requests are client-gone too. That is correct, because no observer writes are wanted while the process exits.

`reqState` has a `clientCtx` field. `ServeHTTP` stores the client context there before it derives the cancel-with-cause context. Only the request goroutine touches `reqState` (ADR-0007).

### 2. `Backend.RearmTrial()` on client-gone

`RearmTrial()` clears the half-open trial flag only if the circuit is Half-Open with its trial taken. Otherwise it does nothing. The proxy calls it in the client-gone tier. The next request can then take the trial and test the backend.

It has no generation guard. While a trial is open, the circuit admits no other request. The only possible overlap is a stale request admitted while Closed that resolves the trial early. ADR-0012 already accepts that case. `RearmTrial` then sees a non-Half-Open state and does nothing. A generation token would only close a race where a stale request resolves the trial and the cooldown elapses again inside the cancellation window. That window is sub-millisecond against a 30s default cooldown, so the extra state is not justified.

`RearmTrial` is a method-only addition to `Backend`, like the other state methods since ADR-0006.

## Consequences

- A client disconnect writes nothing to the circuit, the outlier window or EWMA. It shows as 499 / `4xx`. A 5xx alert always means a backend-caused failure.
- A cancelled client can no longer wedge the half-open trial. The re-arm is one CAS, with no goroutine, lock or per-request bookkeeping.
- The 499 write to a closed connection does nothing on the wire. It exists for the recorder, metrics and logs.
- `Backend` grows `RearmTrial`.
- The stale-request overlap above is possible in theory and is bounded by the circuit cooldown. Revisit the generation token if the cooldown ever drops below one second.
- Rejected alternatives:
  - Recording the cancelled trial as a failure (reopens a healthy circuit because of client behaviour).
  - Recording it as a success (closes the circuit with no evidence).
  - Feeding only the circuit (needs a new interface and still has to choose an outcome).
  - A trial-release closure from admission (changes the `CircuitGate.Allow` / `Registry.Allow` signatures).
