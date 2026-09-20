# 06: Whole-Request Metrics — Counter + Histogram

**What to build:** `lb_requests_total` and `lb_request_duration_seconds`
are driven from the existing `proxy.ServeHTTP` request lifecycle, covering
every exit path — not just backend round trips — so short-circuited 503s
are counted and timed too, with the correct `backend` label in each case.

**Blocked by:** 01 (needs the `Collector`)

**Status:** ready-for-agent

- [ ] `proxy.Proxy` holds a metrics-observing reference (concrete
      `*metrics.Collector`, or a small consumer-defined interface at the
      implementer's discretion — no new fan-out interface; `metrics` is
      this hook's only consumer, unlike `RoundTripObserver`'s three)
- [ ] The observation is made from the same place `ServeHTTP`'s existing
      `start := time.Now()` and deferred `logRequest` already live — same
      timer, same `state`, reused rather than a new mechanism invented
- [ ] `backend=""` when `state.backend` is nil (no healthy backend found),
      computed the same way `logRequest` already derives `backendName`
- [ ] A circuit-denied request is counted with the **real** backend label
      (`Select()` already identified one before `Allow()` denied it),
      distinct from the no-healthy-backend case
- [ ] `status_class` derived from `state.status` the same way `logRequest`
      already branches on `>= 500`
- [ ] `RoundTripObserver`'s existing duration measurement remains purely
      internal (still feeds P2C-EWMA/health/circuit unchanged) and is
      never itself exposed as `lb_request_duration_seconds`
- [ ] `internal/balancer` requires zero code changes and zero new imports
- [ ] Test: a 2xx request increments the counter and records a non-zero
      histogram observation with the real backend/method/`2xx` labels
- [ ] Test: a no-healthy-backend 503 increments the counter with
      `backend=""`, `5xx` class
- [ ] Test: a circuit-denied 503 increments the counter with the real
      backend label, `5xx` class
- [ ] Test: a backend error via `ErrorHandler` increments the counter with
      the real backend label, `5xx` class
- [ ] Test: every existing proxy test continues to pass unchanged
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments
