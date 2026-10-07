# ADR-0018: No retry, ever: a failed round trip is classified and surfaced, never repeated

**Status:** Accepted

## Context

The request path is built on one invariant: **one client request is one backend round trip.** Two mechanisms depend on it.

1. **Active-request accounting is per client request, claimed and released once.** `ServeHTTP` claims one slot through `reqState.activate()`. This increments `Backend.ActiveConns()` and the `lb_active_requests` gauge at the same call site. `reqState.release()` drops the slot through a `sync.Once`, because two paths can fire for one request: the body wrapper's `Close()` and `errorHandler`. The selectors read this counter as live load. `LeastConnections` picks the minimum `ActiveConns()`. `ConsistentHashBoundedLoads` sets capacity from the mean `ActiveConns()` over selectable backends.
2. **The observer fan-out fires once per observed terminal round trip.** `modifyResponse` reports a success, and `errorHandler` reports a failure, both through `observe()`. The observers are the passive outlier detector, the circuit breaker and the EWMA latency observer. Client-gone requests feed no observer (ADR-0017). This fan-out is the only path that feeds a half-open trial result back to the circuit (ADR-0011).

A failed round trip is put into one of three tiers (drain cancellation, client-gone, transport failure) and returned as 502 or 499 (ADR-0017). A backend death after the headers is logged without changing the recorded success. None of these tiers is "retryable".

## Decision

**The load balancer never retries a failed round trip.** It classifies the failure, logs it, and returns 502 (transport or drain) or 499 (client-gone). There is no retry to another backend, no retry to the same backend, and no retry budget, policy or config key.

### Why a retry does harm

1. **It double-counts active requests.** A retry to another backend must either create a second `reqState` or change `state.backend` after the first attempt was counted. A second `reqState` puts two slots on the fleet for one client request, so a burst of retries looks like a burst of load to `LeastConnections` and `ConsistentHashBoundedLoads`. Changing `state.backend` breaks the exactly-once release (ADR-0007). The gauge decrements by backend name, so it would decrement the wrong series.
2. **It corrupts the outlier window.** Each attempt sends one outcome to the observers. One client failure retried once gives two failures in the window. The detector ejects at 5 failures in a 10-outcome window, so retries reach that threshold sooner than 5 distinct requests would. The circuit breaker also sees failures the client never saw. The EWMA observer gets a second fixed 2s penalty (`p2cFailurePenalty`) for one logical failure.
3. **It hides the failure signal.** A 5xx means a backend-caused failure, and a 499 means client churn. If a retry succeeds, the client sees 200, but the outlier window still counts a failure. The recorded signals then drift from what the client saw.

### Why a retry cannot be added later without a redesign

- **Request bodies cannot be replayed.** The first attempt consumes the body stream. A retry needs a bounded body buffer, which breaks the streaming behaviour ADR-0007 keeps.
- **The load balancer cannot know idempotency.** It is method-agnostic and does not read bodies. Method alone is not a safe signal: a GET can mutate, and a POST can be idempotent. Only the client knows, so the retry belongs to the client.
- **Four subsystems share the round-trip contract:** active requests, outlier detection, the circuit breaker and EWMA latency. Adding retry is an architecture change, not a flag.

### What replaces retry

Classification and fast failure. A transport failure (dial timeout, response-header timeout, connection refused) returns 502 quickly, and the transport timeouts bound the wait. A client cancellation is 499 with no observer write. A backend death mid-response leaves a WARN line with the bytes copied. The client sees the outcome and decides whether to repeat the request.

`http.Transport`'s own retry of an idempotent request that failed on a reused connection before it was written is different. It is stdlib behaviour that the observers never see, and it does not break this decision.

## Consequences

- One client request claims one slot, and each observed round trip feeds one outcome. Selection load, outlier windows, circuit state and EWMA all describe the real traffic.
- The failure signal stays honest: 5xx is a backend fault, 499 is client churn, and no retry hides either.
- `httputil.ReverseProxy` needs no retry hook, no body buffer and no retry budget.
- A short backend blip that one retry would hide shows as a 502. The bounded transport timeouts keep this failure fast.
- LB-level retry later would need a retry-attempt identity in the observer fan-out and the active-request accounting first.
- Rejected alternatives:
  - Retry of idempotent methods only (method is not a safe signal, and it still double-counts).
  - One retry on a connection error (doubles the weight of a real unhealthy signal).
  - Body buffering (breaks streaming).
  - A retry budget or hedging (P2C-EWMA already routes around slow backends).
