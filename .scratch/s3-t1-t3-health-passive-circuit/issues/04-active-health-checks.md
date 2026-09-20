# 04: Active Health Checks

**What to build:** A per-backend active health-check subsystem — one probe
goroutine per backend, and an N-consecutive-failure / M-consecutive-success
state machine driving `Backend.MarkHealthy()`/`MarkUnhealthy()`.

**Blocked by:** 01 (needs `MarkHealthy`/`MarkUnhealthy`), 03 (needs the
probe-interval/timeout config fields)

**Status:** ready-for-agent

- [x] A new health-checker type (e.g. `health.Checker`) constructed from the
      registry plus the probe interval/timeout read from `Config`, owning
      one goroutine per backend
- [x] Probe: plain GET to the backend's already-configured URL (no new
      per-backend health-path field) via a dedicated `http.Client` with its
      own timeout, independent of `internal/proxy`'s transport
- [x] 2xx response → success; anything else, including 3xx, → failure
      (`httputil.ReverseProxy` forwards redirects verbatim, so a redirecting
      backend must not read as healthy)
- [x] State machine: N consecutive failures → `MarkUnhealthy()`; M
      consecutive successes → `MarkHealthy()`; N and M are unexported Go
      constants, not config fields
- [x] Probe goroutines are started in `main.go` after the registry is built
      and share the existing `sigCtx` (SIGINT/SIGTERM `signal.NotifyContext`)
      for shutdown — no second shutdown primitive
- [x] Each backend's single-probe-cycle logic is a function separate from
      the goroutine's `for { select }` loop wiring, callable directly in
      tests with no real ticker or context cancellation
- [x] Test: a 2xx-returning `httptest.Server` fixture reaches `MarkHealthy`
      after M consecutive successes
- [x] Test: a 5xx-returning or connection-refused fixture reaches
      `MarkUnhealthy` after N consecutive failures
- [x] Test: a 3xx-redirecting fixture is treated as a failure, not a success
- [x] Test: probe cycles run against the dedicated `http.Client`, independent
      of any `proxy.Proxy` instance
- [x] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on completion

## Comments
