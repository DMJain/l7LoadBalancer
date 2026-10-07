# ADR-0011: Health, passive-outlier, and circuit-breaker composition

**Status:** Accepted. Amended by ADR-0017: a client-gone failure feeds no observer, and it re-arms a half-open trial.

## Context

Three subsystems can each decide "do not send traffic to backend X", on different timescales and for different reasons: active health checks, passive outlier detection, and a circuit breaker for each backend. Without a composition rule, three faults appear:

1. If the circuit sets `healthy=false` when it trips, the backend leaves the selectable pool. No half-open trial request can then reach it, so the circuit cannot recover.
2. If several writers share one health bit, the last writer silently overrides the others.
3. Passive detection and the circuit breaker both need the outcome of each round trip, from the same two proxy hooks (`modifyResponse`, `errorHandler`). Without a shared mechanism, each one adds its own call.

## Decision

### Authority

1. **A backend is selectable when it is healthy AND its circuit is not open.** Only active checks and passive detection write `Backend.healthy`. The circuit breaker never writes it. Circuit state and health are two separate facts, combined at read time. A half-open backend keeps `healthy=true` and stays in the pool. The breaker's per-request gate (decision 7) limits it to one trial in flight. `IsHealthy()` still reports only health. The registry method is `Registry.Selectable()`, so its name matches what it filters.
2. **Health has two mutators: `MarkHealthy()` and `MarkUnhealthy()`.** Active checks and passive detection may call `MarkUnhealthy()`. By convention, only active checks call `MarkHealthy()`. Go cannot enforce the caller. The split makes the recovery rule in decision 3 visible at the call site. This replaces `SetHealthy` from ADR-0006.
3. **A backend that passive detection ejects recovers only through active probes.** Passive detection has no recovery timer. There are two ways in and one way out. Active checks are therefore a prerequisite for passive detection. Recovery fires when the consecutive-success count is `>= probeSuccessesBeforeHealthy` and the backend is not already healthy. The `>=` matters: active probes can keep succeeding while passive detection ejects the backend, so the count can already be above the threshold.
4. **No subsystem has an on/off switch.** All three always run. A config with passive detection on and active checks off would make a backend that can be ejected but can never recover.

### Circuit breaker internals

5. **Circuit state lives on `Backend`.** The state (closed/open/half-open, consecutive failures, opened-at time, trial flag) is unexported and has method-only access. `internal/circuit` imports `internal/backend`, never the reverse, so `Backend` cannot use a type from `circuit`. A `map[*Backend]state` in `circuit` was not used, because it needs its own concurrency model and its own reload handling.
6. **Open → half-open is lazy, with no timer.** Each read of circuit state checks `time.Since(openedAt) >= cooldown` and moves open to half-open with a CAS. `circuit` has no goroutine. `Registry.Selectable()` and the per-request `Allow()` gate use the same promotion path. The trial slot uses a real `CompareAndSwap`, because two requests can see the expired cooldown at the same time, and only one may become the trial. A backend that gets no traffic during cooldown recovers only when traffic reaches it. This is acceptable.
7. **The `Allow(b)` gate runs after `Select()` and before `IncActive()`.** A denied request gets a 503 and never touches active-connection accounting, so ADR-0007's exactly-once release needs no new path. In the Go 1.25.1 `httputil.ReverseProxy` source, a `RoundTrip` failure covers every case where a trial gets no verdict, including a client disconnect before the response. A disconnect after the headers cannot strand a trial, because `modifyResponse` records the verdict before the body streams. The only remaining case is a `RoundTrip` that hangs forever. The transport timeouts (`ResponseHeaderTimeout`, the dial timeout) prevent this, so the trial slot needs no timeout of its own.
8. **The circuit counts consecutive failures. Passive detection uses a sliding window.** Any success resets the circuit count to zero. The circuit is strict and fast. Passive detection is tolerant and slow. Both use the same signal: a 5xx response or a transport failure in `errorHandler`.

### Wiring, configuration and probes

9. **The proxy fans out each round-trip outcome to every registered `RoundTripObserver`:**

    ```go
    type RoundTripObserver interface {
        ObserveRoundTrip(b *backend.Backend, d time.Duration, success bool)
    }
    ```

    `Proxy.RegisterObserver(o RoundTripObserver)` adds an observer before serving. `New(reg, sel)` does not change. `internal/app` registers three observers: the latency observer (`RecordLatency`), the outlier detector and the circuit breaker. `modifyResponse` passes `success := resp.StatusCode < 500` and `d := time.Since(state.dispatchStart)`. `errorHandler` passes `success = false` and `d = p2cFailurePenalty` for a transport failure. Recording is unconditional. Gating happens at a different call site. Do not add a "circuit already open, skip recording" guard: the fan-out is the only path that feeds a half-open trial's result back to the circuit.
10. **Operational knobs are config. Algorithm tuning is constants.** Probe interval, probe timeout and circuit cooldown are global config fields: `health.probe_interval` (default 5s), `health.probe_timeout` (default 2s) and `circuit.cooldown` (default 30s). These Go constants are fixed:

    | Constant | Value |
    |---|---|
    | `probeFailuresBeforeUnhealthy` | 3 |
    | `probeSuccessesBeforeHealthy` | 2 |
    | `outlierWindowSize` | 10 |
    | `outlierFailuresBeforeEject` | 5 |
    | `circuitFailuresBeforeOpen` | 3 (ADR-0012) |

    There are no per-backend overrides.
11. **An active probe is a plain GET to the backend's configured URL.** There is no `health_path` field. Only 2xx is success, and 3xx is a failure, because `ReverseProxy` passes redirects through instead of following them. The health checker has its own `http.Client` and timeout. `internal/health` does not import `internal/proxy`.
12. **The passive window counts outcomes, not time.** A failure is a 5xx response or a transport failure.
13. **Probes stop with the run context.** `App.Run` passes its context to the checker, and `main` makes that context with `signal.NotifyContext` for SIGINT and SIGTERM. Each backend has one probe goroutine that selects on `ctx.Done()` and a ticker. The logic of one probe cycle is a separate function, so tests run it without a ticker.

## Consequences

- Each piece of state has one writer or one read path. Faults 1 and 2 cannot happen.
- Selectors still call one registry method and get one filtered slice.
- `Backend` has more exported methods. Caller rules (decision 2) are conventions, not type rules.
- Health and the circuit can disagree about why a backend is out. A backend that recovers from ejection stays out until its own circuit recovers. Both gate correctly, so they do not need to agree.
- The circuit does not write `healthy`. That design makes the trial unreachable and causes a last-writer race.
- The circuit is not the only selection authority with health as an input. That design needs an edge between `health` and `circuit`, or a third component.
- A separate `ejected` field was not used. Two gates give the same behavior with fewer parts.
- A timer-driven promotion was not used. It needs a goroutine for a check that a read does at no cost.
- The proxy does not select again when `Allow()` denies a request. That needs an exclude set on `Selector` and is a retry. ADR-0018 rejects retries.
- A sliding window for the circuit was not used. The two mechanisms would then be the same at two thresholds.
- A circuit that ignores 5xx was not used. A backend that returns 500 must open its circuit.
