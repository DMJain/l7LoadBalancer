# ADR-0018: No retry, ever — a failed round trip is classified and surfaced, never repeated

- **Status**: Accepted
- **Date**: 2026-09-27
- **Deciders**: Darshan Jain (project owner) + opencode agent (S4.T11, scope in `.scratch/s4-t5-t10-connection-lifecycle/spec.md`)

## Context

`MILESTONES.md` Sprint 4 lists "ADR: retry policy (or the deliberate absence of
one) and why". The bundle spec records the answer in S4.T11 — **no retry, ever** —
as a consequence of the observer and active-connection design, and defers the
ADR until after S4.T5 and S4.T6 land so it can cite the code that forces the
decision instead of asserting it. This is that ADR.

The request path is built around the invariant **one client request = one
backend round trip**. Two mechanisms encode it:

1. **Active-connection accounting is per client request, claimed and released
   exactly once.** `ServeHTTP` claims one slot on admission via
   `reqState.activate()` (`internal/proxy/proxy.go:86`), incrementing both
   `Backend.ActiveConns()` and the `lb_active_connections` gauge at the same
   call site so they cannot drift. `reqState.release()` (`proxy.go:95`) drops the
   slot through a `sync.Once` because two triggers can fire for one request —
   the response-body wrapper's `Close()` and `errorHandler` (`proxy.go:421`) —
   and exactly one must. The selection algorithms read that counter as live
   load: `LeastConnections` picks the minimum `ActiveConns()`
   (`internal/balancer/leastconn.go:37`), `ConsistentHashBoundedLoads` derives
   its capacity from the mean `ActiveConns()` over **selectable** backends —
   healthy *and* circuit-not-open (`internal/balancer/consistent_hash.go:23`,
   `:159`).

2. **The observer fan-out fires once per observed terminal round trip.** A
   success is fed in `modifyResponse` (`proxy.go:349`), a failure in
   `errorHandler` (`proxy.go:407`, `:419`), both through the single `observe()`
   path (`proxy.go:242`). The registered observers are the passive outlier
   detector (a sliding window of failures that ejects at threshold), the circuit
   breaker, and the EWMA latency observer. Client-gone is deliberately the one
   terminal tier that feeds no observer (ADR-0017), so "observed" is part of the
   invariant. ADR-0011 decision 9 is explicit that this fan-out "is the only
   mechanism that feeds a half-open trial's result back into the Closed/Open
   decision"; `proxy.go:120` records the same in code.

S4.T5 makes a failed round trip legible: it is classified into exactly one of
three tiers — drain cancellation, client-gone, genuine transport failure — and
surfaced as 502 or 499 (ADR-0017; `proxy.go:392`). S4.T6 makes a death after the
headers visible without un-ringing the success recorded when the headers arrived
(`proxy.go:343`). Neither classifies a round trip as *retryable*: the
classification is terminal by construction.

## Decision

**The load balancer never retries a failed round trip, ever.**

A failed round trip is classified (S4.T5), logged, and surfaced to the client as
502 (transport or drain) or 499 (client-gone). The request ends there. There is
no retry to a second backend, no retry of the same backend, and no retry budget,
policy, or config knob. The absence is a consequence of the architecture, not a
missing feature.

### Why retrying is harmful

1. **It double-counts active connections.** A retry that dispatches to another
   backend must either build a second `reqState` — calling `activate()` again and
   placing two slots on the fleet for one client request — or mutate the single
   `state.backend` after the first attempt was counted. The first inflates the
   live-load metric that `LeastConnections` and `ConsistentHashBoundedLoads`
   select on, so a burst of retries reads as a burst of load. The second breaks
   `release()`'s exactly-once contract (ADR-0007) and the `lb_active_connections`
   gauge, which decrements by backend name (`proxy.go:95`): once `state.backend`
   changed, the decrement would land on the wrong series.

2. **It corrupts the outlier window.** `observe()` fans out one outcome per
   observed round trip. If one logical client request fails against backend A
   and is retried against B, the passive outlier detector's sliding window
   receives one failure per attempt — two failures recorded for one
   client-visible failure. The detector ejects on 5 failures in its 10-outcome
   window (`internal/health/outlier.go:21`, `:29`; CONTEXT.md "Ejection"), so a
   retry lets a single bad request pattern reach that threshold sooner than 5
   distinct requests would, and the circuit breaker sees failures for attempts
   the client never observed. The EWMA observer is fed a second fixed 2s penalty
   (`p2cFailurePenalty`, `proxy.go:30`) for one logical failure, skewing a
   backend's latency estimate.

3. **It launders the failure signal operators rely on.** S4.T5's point is that a
   5xx always means a backend-caused failure and a 499 always means client
   churn. A retried failure whose retry succeeds reaches the client as a 200
   while still counting as a failure in the outlier window — a failing backend
   hidden behind its retry's success, with the recorded signals drifting away
   from what the client experienced.

### Why retrying could not be added later without redesigning

- **Bodies cannot be replayed.** The request body is a stream consumed by the
  first attempt; retrying requires buffering it (and bounding that buffer),
  which regresses the streaming posture ADR-0007 preserves by leaving the
  `ResponseWriter` stdlib-owned and unwrapped.
- **Idempotency is unknowable here.** A sound retry policy must know whether the
  request is safe to repeat. The LB is method-agnostic and body-opaque; method
  alone is not a sound proxy (a GET can mutate, a POST can be idempotent). The
  client that issued the request is the only party that knows — which is why the
  retry belongs to the client (spec story 17: "fail fast and retry elsewhere").
- **A retry changes the round-trip contract four subsystems already share** —
  active connections, passive outlier detection, the circuit breaker, and EWMA
  latency. Enabling it is an architectural change, not a flag.

### What replaces retry

Classification and fast failure. A transport failure (dial timeout,
response-header timeout, connection refused) is surfaced as 502 promptly — T8's
transport timeouts make "promptly" bounded rather than a hang. A client
cancellation is 499 with no observer write (T5). A backend death mid-response
leaves a WARN trace with the bytes copied (T6). The client sees the outcome and
decides whether to repeat, because only the client knows the request's
idempotency.

## Consequences

- Positive: the invariant holds by construction — one client request claims
  exactly one active-connection slot, and each observed round trip feeds exactly
  one observer outcome (client-gone feeds none, by design) — so selection load,
  outlier windows, circuit state, and EWMA latency all describe exactly the
  traffic that happened.
- Positive: the failure signal stays honest — a 5xx always means a
  backend-caused failure, a 499 always means client churn, and nothing is hidden
  behind an internal retry.
- Positive: `httputil.ReverseProxy`'s single `RoundTrip` needs no retryable seam,
  no request-body buffer, and no per-backend retry budget; the request path is
  unchanged.
- Negative: a transient backend blip that a single retry would have masked is
  surfaced as a 502. Accepted — the LB is not the retry layer, and T8's bounded
  timeouts make the failure fast rather than a hang.
- Negative: callers who later want LB-level retry must redesign the observer
  fan-out and active-connection accounting to carry a retry-attempt identity
  before adding it. This ADR exists so that redesign is deliberate, not
  accidental.
- Neutral: this ADR records the absence of a behavior; it changes no code. It is
  the Sprint 4 MILESTONES deliverable "ADR: retry policy (or the deliberate
  absence of one) and why".

## Alternatives considered

- **Retry idempotent methods only (GET/HEAD/PUT/DELETE) to the next backend**:
  rejected — method is not a sound idempotency proxy (a GET can mutate, a POST
  may be idempotent), and it still double-counts active connections and feeds
  two observer outcomes for one request.
- **Retry the same backend once on a connection-level error (dial failure,
  `ECONNRESET`) before failing**: rejected — a connection error is precisely the
  signal that the backend is unhealthy; retrying doubles that failure's weight
  in the outlier window and adds latency before the same 502.
- **Buffer the request body to make retries possible**: rejected — buffering
  regresses streaming, and the buffer bound is a new resource to tune and fail;
  no ticket in the plan needs it.
- **A retry budget or latency-hedging policy**: rejected — it duplicates the
  selection layer's job (P2C-EWMA already routes around slow backends) and would
  need the same observer/accounting redesign, for a behavior the client can own.
- **Rely on `http.Transport`'s own idempotent-request retry**: not an
  alternative but a distinction — that is bounded stdlib behavior for a request
  that failed to reuse a connection and was never written to a backend; it is
  invisible to the observer fan-out and does not violate this decision, which is
  about retrying a *failed round trip* the LB observes.
