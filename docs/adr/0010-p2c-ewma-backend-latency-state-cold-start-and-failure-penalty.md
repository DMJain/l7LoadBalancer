# ADR-0010: P2C-EWMA: Backend-owned latency state, cold-start semantics, and the failure penalty

**Status:** Accepted. Amended by ADR-0011 (latency goes to `RecordLatency` through the round-trip observer fan-out) and ADR-0017 (a client-gone failure records no penalty).

## Context

`LeastConnections` and `ConsistentHashBoundedLoads` limit load by the count of in-flight requests. A backend that is healthy and returns 2xx, but is slow, looks the same as a fast one until requests queue on it. `PowerOfTwoChoicesEWMA` (the `p2c_ewma` algorithm) picks two backends at random and sends the request to the one with the lower smoothed latency. It needs a latency estimate for each backend. Three properties of that state are hard to reverse: where it lives, what the first sample means, and what a failure records.

Smaller choices are in code comments: the `atomic.Int64` nanosecond representation and its CAS loop, the `math/rand/v2` source, and the bypass when only one backend is healthy.

## Decision

1. **`Backend` owns the latency estimate.** The unexported field `latencyEWMA atomic.Int64` holds nanoseconds. Callers use `RecordLatency(d time.Duration)` and `EWMALatency() time.Duration`. This amends ADR-0002 decision 5. The proxy writes and `balancer` reads. Both are different packages, and both reach the state with no workaround.
2. **The first sample sets the estimate directly.** When the field reads zero, `RecordLatency` stores the observed duration. After that, it blends `latency_new = α·observed + (1-α)·latency_old` with α = 0.1 (`ewmaAlpha`). The estimate then ignores jitter but follows a lasting slowdown within about ten requests. Zero also means "no sample yet". A real round trip of 0ns cannot happen on Go's monotonic clock. `Select` treats a zero estimate as the fastest. This is intentional: the backend wins its next comparison, gets a real sample, and stops reading zero.
3. **A failed round trip records a fixed 2s penalty** (`p2cFailurePenalty`), not the real time to failure. A fast failure (for example, connection refused) would otherwise look fast to P2C, and traffic would move toward the failing backend. 2s is far above the dummy-backend defaults in `deployments/docker/docker-compose.yml` (50, 150 and 300ms). `SLEEP_MS` has no upper limit, so the margin above 1s keeps a slow but alive backend (800–900ms) separate from a failing one. The penalty goes through the same EWMA. Ten failures in a row move the estimate to about 1.3s, and twenty to about 1.75s. One transient failure does not erase the history.
4. **Recording is unconditional and measures only the backend round trip.** Every round trip records a latency, success or failure, for every configured selector. `IncActive`/`DecActive` also run under every selector. The window starts in `director()` just before dispatch and ends when `modifyResponse` gets the response headers. It is not the `latency_ms` log window, which includes the body copy to the client. A slow client must not make a fast backend look slow. For this reason `reqState` has a `dispatchStart` timestamp separate from the log's start time.

## Evidence

`TestPowerOfTwoChoicesEWMAShiftsLoadToFasterBackend` seeds one fast backend (10ms) and three slow backends (500ms), then runs 4,000 `Select` calls. P2C draws two of four backends, so the fast backend is in half of the draws and wins each of them. One run:

| Backend | Selections | Share |
|---|---|---|
| backend-a (fast, 10ms) | 2,043 | 51.1% |
| backend-b (slow, 500ms) | 671 | 16.8% |
| backend-c (slow, 500ms) | 647 | 16.2% |
| backend-d (slow, 500ms) | 639 | 16.0% |

The test asserts that the fast backend gets about half (`InDelta(2000, ±400)`) and more than twice the share of the busiest slow backend. `TestPowerOfTwoChoicesEWMAPrefersFasterOfTwo` covers two backends, where the faster one wins every call. The global `math/rand/v2` source makes the split change between runs. The property does not change.

## Consequences

- P2C-EWMA works without the health checker, the circuit breaker or metrics.
- The latency state follows the same method-only, unexported-atomic pattern as `healthy` and `active`.
- A failure cannot look like low latency.
- Any caller with a `*Backend` can record a latency. This is a convention, not a type rule.
- The penalty is a heuristic. A backend that is slow to fail (a long TCP connect timeout) records only 2s. Recording the real time has the worse, inverted failure mode.
- α and the penalty are Go constants, not config fields, like ε in ADR-0009.
- State owned by `Registry` or a separate store was not used. It adds a lookup for each request on the hot path.
- A latency interface between `proxy` and `balancer` was not used. Both use the same concrete `*Backend`.
- A blend from zero for the first sample was not used. A new or recovered backend would read too fast and get too many requests.
- Reusing `latency_ms` was not used, because it includes the client's body read time.
