# 06: Whole-Request Metrics — Counter + Histogram

**What to build:** `lb_requests_total` and `lb_request_duration_seconds`
are driven from the existing `proxy.ServeHTTP` request lifecycle, covering
every exit path — not just backend round trips — so short-circuited 503s
are counted and timed too, with the correct `backend` label in each case.

**Blocked by:** 01 (needs the `Collector`)

**Status:** done

- [x] `proxy.Proxy` holds a metrics-observing reference (concrete
      `*metrics.Collector`, or a small consumer-defined interface at the
      implementer's discretion — no new fan-out interface; `metrics` is
      this hook's only consumer, unlike `RoundTripObserver`'s three)
- [x] The observation is made from the same place `ServeHTTP`'s existing
      `start := time.Now()` and deferred `logRequest` already live — same
      timer, same `state`, reused rather than a new mechanism invented
- [x] `backend=""` when `state.backend` is nil (no healthy backend found),
      computed the same way `logRequest` already derives `backendName`
- [x] A circuit-denied request is counted with the **real** backend label
      (`Select()` already identified one before `Allow()` denied it),
      distinct from the no-healthy-backend case
- [x] `status_class` derived from `state.status` the same way `logRequest`
      already branches on `>= 500`
- [x] `RoundTripObserver`'s existing duration measurement remains purely
      internal (still feeds P2C-EWMA/health/circuit unchanged) and is
      never itself exposed as `lb_request_duration_seconds`
- [x] `internal/balancer` requires zero code changes and zero new imports
- [x] Test: a 2xx request increments the counter and records a non-zero
      histogram observation with the real backend/method/`2xx` labels
- [x] Test: a no-healthy-backend 503 increments the counter with
      `backend=""`, `5xx` class
- [x] Test: a circuit-denied 503 increments the counter with the real
      backend label, `5xx` class
- [x] Test: a backend error via `ErrorHandler` increments the counter with
      the real backend label, `5xx` class
- [x] Test: every existing proxy test continues to pass unchanged
- [x] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on
      completion

## Comments

- 2026-09-21 — opencode (S3.T6.1): implemented. `proxy.Proxy` holds an optional
  concrete `*metrics.Collector` via the additive `SetMetrics`; `ServeHTTP`'s
  deferred hook is now `recordRequest`, which runs `observeRequest` plus the
  unchanged `logRequest` from the same `start`/`state`. `observeRequest` feeds
  `lb_requests_total`/`lb_request_duration_seconds` on every exit path with a
  shared `backendName` helper (`backend=""` only when no backend was chosen)
  and a `statusClass` (`status/100 + "xx"`) helper; a circuit-denied request
  keeps the real backend label. `RoundTripObserver`'s backend-leg duration is
  untouched, `internal/balancer` is untouched, and `main.go` constructs the
  collector before the handler and passes it through `newHandler`. Tests in
  `internal/proxy/metrics_test.go` assert both series' labels and a non-zero
  histogram observation per exit path; all existing proxy tests pass unchanged.
  `make test`/`make test-race` green, `go vet` clean, `make fmt` no diff.
