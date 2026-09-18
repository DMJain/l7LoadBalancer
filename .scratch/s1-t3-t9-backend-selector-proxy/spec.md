# Spec: S1.T3–T9 — Backend Registry, Selectors, Proxy, Wiring, and Dummy Backends

Status: ready-for-agent

---

## Problem Statement

The load balancer has a working, validated config loader (S1.T2) but nothing downstream of it. There is no backend registry, no selection algorithm, no proxy, and `cmd/l7LoadBalancer/main.go` still returns a placeholder `501` for every request. An operator can describe backends and pick an algorithm in YAML, but nothing acts on that description — no connection is tracked, no request is routed, nothing is forwarded anywhere. Until this phase lands, Sprint 1's exit criteria ("curl through the proxy distributes across 3 backends per selected algorithm") is unreachable, and there is no way to demonstrate the load balancer actually balancing load.

## Solution

Implement the request-handling core end-to-end, in dependency order:

1. **S1.T3** — a concurrency-safe `Backend` and `Registry` in `internal/backend` that track identity, health, and active-connection count behind method-only access.
2. **S1.T4** — `RoundRobin`, the first `Selector` implementation, rotating across healthy backends with a lock-free atomic counter.
3. **S1.T5** — `LeastConnections`, the second `Selector` implementation, picking the healthy backend with the fewest active connections, with deterministic tie-breaking.
4. **S1.T6** — `internal/proxy.Proxy`, wrapping `httputil.ReverseProxy`: selects a backend per request, short-circuits a clean 503 when none are healthy, tracks `ActiveConns` precisely around the round trip, and emits one structured log line per request.
5. **S1.T7** — wire `cmd/l7LoadBalancer/main.go` end-to-end: `config.Load` → `backend.NewRegistry` → `balancer.NewFromConfig` → `proxy.New` → `http.Server`, replacing the placeholder handler.
6. **S1.T8** — cross-selector regression tests proving `RoundRobin` and `LeastConnections` are interchangeable with respect to health transitions.
7. **S1.T9** — three dummy backend containers via docker-compose, with independently configurable latency/failure chaos knobs, so the whole system can be exercised without real upstreams.

After this phase, `make run` starts a load balancer that an operator can point at real or dummy backends and watch requests distributed per the configured algorithm, with accurate connection accounting and structured logs.

## User Stories

**Backend / Registry (S1.T3)**

1. As a load balancer operator, I want each backend's health state tracked safely under concurrent access, so that a request goroutine never races with a health check.
2. As a load balancer operator, I want each backend's active-connection count tracked atomically, so that `LeastConnections` reads an accurate, race-free count under concurrent load.
3. As a downstream developer (balancer, proxy), I want to query a backend's health only through `IsHealthy()`, never a raw field, so that Sprint 3 can swap the underlying representation without touching my code.
4. As the future Sprint 3 health-check subsystem, I want a `SetHealthy(bool)` method I can call directly on the `*Backend` I'm probing, so that I don't have to round-trip through the registry by name on every tick.
5. As a test author (S1.T8), I want a way to flip a backend's health mid-test before Sprint 3's health checker exists, so that I can prove both selectors react correctly to health transitions.
6. As a load balancer operator, I want every backend to start healthy when the registry is built, so that Sprint 1 can route traffic before any health-checking subsystem exists.
7. As a downstream developer, I want `Registry.All()` and `Registry.Healthy()` to each return a fresh slice per call, so that I can iterate safely while another goroutine concurrently mutates backend state, without holding a lock myself.
8. As the `LeastConnections` selector, I want the registry's backend order to be stable and deterministic, so that tie-breaking by "first in registry order" is reproducible across test runs.
9. As a downstream developer, I want `NewRegistry` to build backends directly from validated `config.BackendConfig` entries, so that the registry never re-validates what `config.Validate()` already checked.

**RoundRobin (S1.T4)**

10. As a load balancer operator using `round_robin`, I want requests distributed in rotating order across healthy backends, so that load is spread evenly over time.
11. As a load balancer operator, I want round-robin rotation to use a lock-free atomic counter, so that selection never blocks under high concurrency and can never deadlock.
12. As a load balancer operator, I want round-robin to select only from currently healthy backends, so that an unhealthy backend is silently skipped rather than sent traffic.
13. As a load balancer operator, I want a clear `ErrNoHealthyBackends` error when every backend is unhealthy, so that the proxy can distinguish "nothing to route to" from any other failure.
14. As a test author, I want 1000 concurrent round-robin selections against 3 healthy backends to land within ±5% of the expected 1/3 share each, so that I have confidence the rotation is fair under real concurrency, not just in a single-threaded loop.

**LeastConnections (S1.T5)**

15. As a load balancer operator using `least_conn`, I want requests routed to the healthy backend with the fewest active connections, so that load is balanced by actual current work, not just round-robin turn-taking.
16. As a load balancer operator, I want ties in active-connection count broken deterministically (first tied backend in registry order), so that behavior is reproducible and testable, not randomly chosen.
17. As a downstream developer, I want `LeastConnections` to only read `ActiveConns()`, never mutate it, so that connection-count bookkeeping stays the proxy's single responsibility.
18. As a load balancer operator, I want `LeastConnections` to also return `ErrNoHealthyBackends` when nothing is healthy, so that both selectors behave identically for that failure case.

**Proxy (S1.T6)**

19. As a load balancer operator, I want every incoming request routed through the configured selection algorithm to a live backend, so that the algorithm choice in my config actually takes effect.
20. As a load balancer operator, I want a request to receive an immediate 503 when no backend is healthy, so that clients get a clear, fast failure instead of a hang or a panic.
21. As a load balancer operator, I want the active-connection count incremented before a request is dispatched and decremented only after the response body is fully closed, so that streamed-but-incomplete responses are never counted as "done" early.
22. As a load balancer operator, I want the connection count to return to zero after a burst of 100 concurrent requests completes, so that I can trust `LeastConnections`' accuracy isn't drifting from a leak.
23. As a load balancer operator, I want a backend round-trip failure (e.g. connection refused) to also decrement the active-connection count, so that a flaky backend doesn't silently pin its counter above zero forever.
24. As an operator debugging live traffic, I want one structured "request complete" log line per request — including which backend served it, status, and latency — so that I can grep logs by any of the canonical fields.
25. As an operator debugging a failure, I want failed requests (503 no-healthy, 502 backend error) logged at `WARN`, so that they stand out from routine 200s in a live JSON log stream without needing a field-based filter.

**Wiring (S1.T7)**

26. As a load balancer operator, I want `make run` to load my config, build the registry and selector, and start serving real traffic — not the placeholder 501 handler — so that the binary actually does its job.
27. As a load balancer operator, I want a bad or invalid config file to produce a clear fatal error and non-zero exit at startup, not a silent fallback to defaults, so that misconfiguration is never masked.
28. As a load balancer operator, I want the algorithm named in my config (`round_robin` or `least_conn`) to select the matching selector implementation automatically, so that I don't need to touch code to switch algorithms.
29. As a load balancer operator, I want SIGINT/SIGTERM to still gracefully drain in-flight requests before the process exits, so that restarting the LB doesn't drop active connections.
30. As a developer reading logs, I want `main.go`'s logging to go through the existing `internal/logger` setup, so that log configuration lives in one place instead of being duplicated inline.

**Cross-selector tests (S1.T8)**

31. As a project maintainer, I want a compile-time guarantee that both `RoundRobin` and `LeastConnections` satisfy the `Selector` interface, so that a future signature change is caught at build time, not at runtime.
32. As a project maintainer, I want a test proving that when a backend goes unhealthy mid-run, both selectors immediately stop choosing it, so that health-awareness isn't accidentally selector-specific.
33. As a project maintainer, I want a test proving that when a previously-unhealthy backend recovers, both selectors resume choosing it, so that recovery is symmetric with ejection.
34. As a project maintainer, I want `go test -cover` output for the balancer package recorded in the session log, so that future sprints have a baseline to compare against (no enforced threshold yet).

**Docker-compose dummy backends (S1.T9)**

35. As a developer demoing the project, I want `docker compose up` to bring up 3 backend services immediately usable by the existing `configs/example.yaml`, so that I can see real load-balancing behavior without writing a second config file.
36. As a developer demoing the project, I want each dummy backend's response body to identify which backend served the request, so that I can visually confirm distribution by grepping curl output.
37. As a developer demoing algorithm differences, I want the 3 dummy backends to have different default latency/failure characteristics out of the box, so that `RoundRobin` vs. `LeastConnections` behavior is visibly different without any manual configuration step.
38. As a developer preparing for Sprint 3, I want the dummy backends' chaos knobs (`SLEEP_MS`, `FAIL_RATE`) independently configurable per service, so that later I can demonstrate circuit-breaking against a specifically flaky backend.

## Implementation Decisions

### `internal/backend` (S1.T3)

- `Backend` exported fields: `Name string`, `URL *url.URL`. Unexported: `healthy atomic.Bool`, `active atomic.Int64`. Reachable only via `IsHealthy()`, `SetHealthy(bool)`, `IncActive()`, `DecActive()`, `ActiveConns() int64` — per ADR-0002 decision 5, amended by ADR-0006 to add `SetHealthy`.
- `SetHealthy` is a permanent method, not a test shim: Sprint 3's per-backend health-check goroutines will call it directly on the `*Backend` they hold, symmetric with how the proxy calls `IncActive`/`DecActive` directly. Owned by the health-check subsystem by convention; not enforced by the type system in Sprint 1.
- `NewRegistry(cfgs []config.BackendConfig) (*Registry, error)` parses each `BackendConfig.URL` into a `*url.URL` and sets every backend's initial health to `true` — there is no health checker yet to set it any other way, and Sprint 1's exit criteria requires all configured backends reachable from the start.
- `Registry` holds an ordered slice only — no name-indexed map. Nothing through Sprint 3 needs O(1) lookup by name; that need belongs to Sprint 4's reload-diffing, which isn't designed yet.
- `Registry.All()` / `Registry.Healthy()` return a fresh slice per call, preserving registry order (required for `LeastConnections`' deterministic tie-break).

### `balancer.RoundRobin` (S1.T4)

- `Selector` interface (`Select(ctx, r) (*backend.Backend, error)`) and `ErrNoHealthyBackends` live in `internal/balancer`, per ADR-0002.
- `RoundRobin` holds an `atomic.Uint64` counter, incremented once per `Select` call; index into the current `Registry.Healthy()` snapshot via `counter % len(healthy)`. No mutex.
- Because `Healthy()` returns a fresh snapshot each call, indexing modulo the current length is correct even as the healthy set changes size between calls — no cross-call consistency is needed.

### `balancer.LeastConnections` (S1.T5)

- Linear scan over `Registry.Healthy()`, tracking the minimum `ActiveConns()` seen; first backend to reach a new minimum wins ties (i.e., first-in-registry-order wins, since the scan proceeds in order and only replaces the current minimum on a strictly lower count).
- Reads `ActiveConns()` only — never mutates it.

### `internal/proxy.Proxy` (S1.T6)

- `Proxy.ServeHTTP` calls `sel.Select(ctx, r)` itself — **not** `Director`. `Director`'s signature (`func(*http.Request)`) has no way to write a response, so the 503 short-circuit for `ErrNoHealthyBackends` must happen in `ServeHTTP`, before `httputil.ReverseProxy` is invoked at all.
- On success, `ServeHTTP` calls `IncActive()` on the chosen backend and attaches it to the request context via an unexported context-key type. It then delegates to one `*httputil.ReverseProxy` built once in `New()`. `Director` reads the backend back out of context and only sets `req.URL.Scheme`/`Host`.
- `ModifyResponse` and `ErrorHandler` read the same backend off `resp.Request.Context()` / `req.Context()` to drive `DecActive()`.
- `ActiveConns` decrement happens via a response-body wrapper whose `Close()` decrements exactly once (guard with `sync.Once` or equivalent), installed in `ModifyResponse`. Never decrement in `Director`, directly in `ModifyResponse`, or only in `ErrorHandler`.
- `ErrorHandler` also calls `DecActive()` (the body-wrapper path never runs if the round trip itself failed), logs the failure, and responds `502 Bad Gateway`.
- Every request emits one "request complete" `slog` line using the canonical fields (`backend`, `method`, `status`, `latency_ms`, `remote_addr`, `path`) — on the success path, the 503 short-circuit path, and the `ErrorHandler` path alike. Logged at `WARN` for 5xx outcomes (both 503 and 502), `INFO` otherwise. No separate "request start" line in Sprint 1.
- No `http.Transport` tuning (`MaxIdleConnsPerHost`, timeouts, etc.) in this phase — stock defaults; connection pool tuning is explicitly Sprint 4 per AGENTS.md.

### `cmd/l7LoadBalancer/main.go` (S1.T7)

- Wiring order: `config.Load(*configPath)` → fatal + non-zero exit on error (no silent fallback) → `backend.NewRegistry` → `balancer.NewFromConfig(cfg, reg)` (the config-string → selector-type switch lives here, in `balancer`, per ADR-0002 — `main.go` stays a thin wiring layer) → `proxy.New(reg, sel)` → `http.Server`.
- Replace the inline `slog.NewJSONHandler` setup with the existing `logger.New(slog.LevelInfo)` (currently dead code) — pure dedup, no new flag.
- Existing SIGINT/SIGTERM graceful-shutdown behavior is preserved unchanged.

### Cross-selector tests (S1.T8)

- `var _ Selector = (*RoundRobin)(nil)` / `var _ Selector = (*LeastConnections)(nil)` compile-time assertions (already present in the frozen stubs).
- A scripted health-toggle sequence run against both selectors: build a registry, run selections, call `SetHealthy(false)` on one backend mid-sequence, assert it's no longer chosen by either selector, call `SetHealthy(true)`, assert it resumes being chosen by both.

### docker-compose dummy backends (S1.T9)

- `docker-compose.yml` defines exactly 3 services (no LB service — the LB runs locally via `make run`; Sprint 5 has its own, separate "LB + Nginx + 4 backends" bench compose, kept distinct so the two don't drift against each other).
- Container ports mapped to host `9001`/`9002`/`9003` so the existing `configs/example.yaml` works against them unmodified — no second config file.
- Dummy backend is a Go stdlib-only binary (matches the project's "everything on stdlib" convention), responding to `GET /` with `{"backend":"<name>"}`.
- Two env vars: `SLEEP_MS` (int, artificial per-request latency, default 0) and `FAIL_RATE` (float 0.0–1.0, fraction of requests returning HTTP 500, default 0), documented in `deployments/docker/dummy-backend/README.md`.
- `docker-compose.yml` gives each of the 3 services distinct, non-zero defaults (not all zero) so `docker compose up` demonstrates uneven latency/failure — and therefore visible `RoundRobin` vs. `LeastConnections` differences — without a manual override.

## Testing Decisions

### What makes a good test here

Tests exercise **external behavior** — selection outcome, HTTP status/body, `ActiveConns` value, log fields — never internal implementation details (e.g. not asserting the exact order atomics are incremented, or which internal helper a selector calls). Table-driven where the input space is enumerable; `testify/require` for setup, `testify/assert` for value checks — the convention `internal/config`'s tests (S1.T2) already established.

### Seams

Three seams, matching what the project's frozen contracts (`docs/design/sprint-1-contracts.md`, ADR-0002) already dictate per package — not collapsed further, because concurrency correctness and exact algorithm behavior aren't cleanly observable only through HTTP responses:

1. **`internal/backend` package boundary** — `NewRegistry`, `Backend.{IsHealthy,SetHealthy,IncActive,DecActive,ActiveConns}`, `Registry.{All,Healthy}` exercised directly. No mocks; no lower seam exists for this concern.
2. **`balancer.Selector` interface** — `RoundRobin`, `LeastConnections`, and the cross-selector health-transition tests all exercise the interface directly, using a real `*backend.Registry` (built via `NewRegistry`) as the fixture. No mocks — a real `Registry` is the natural, cheap fixture.
3. **`proxy.Proxy` as `http.Handler`** — `httptest.NewServer` fake backends behind an `httptest`-wrapped `Proxy`, asserting distribution matches the selector, no-healthy-backend → 503, and `ActiveConns` draining to 0 after concurrent load. The one seam exercising backend + balancer + proxy together end-to-end.

`main.go` (S1.T7) and the docker-compose dummy backends (S1.T9) get **no automated seam** — manual/scripted smoke test only, documented in the Sprint 1 session log. This matches the already-frozen test-approach text for both tasks: a thin wiring layer and an infra-only compose file have no Go logic of their own to unit test.

### Test cases by seam

- **backend**: construction from config; `All()` returns all regardless of health; `Healthy()` filters; concurrent `IncActive`/`DecActive` from N goroutines converges to the correct final count under `-race`; concurrent `SetHealthy` toggling is race-free.
- **balancer.RoundRobin**: cyclic order over 3 backends; empty-healthy-set → `ErrNoHealthyBackends`; 1000 concurrent selects land within ±5% of expected 1/3 share each.
- **balancer.LeastConnections**: pre-seeded `ActiveConns` values → minimum is chosen; tie-break case (equal counts → first in registry order); empty-healthy-set → `ErrNoHealthyBackends`.
- **balancer (cross-selector)**: interface conformance assertions; scripted health-toggle sequence (unhealthy mid-run → excluded; recovers → included again) for both selectors.
- **proxy**: distribution over N requests matches the configured selector's expected pattern; no-healthy-backend → 503 with no panic/hang; 100 concurrent in-flight requests → `ActiveConns` returns to 0 within a small drain window; backend round-trip failure → 502 and `ActiveConns` still decremented.

### Prior art

- `internal/config`'s table-driven + `testify` tests (S1.T2) set the pattern this phase follows.
- `docs/design/sprint-1-contracts.md`'s "Concurrency ownership table" and PROGRESS.md's per-task "Test approach" column are the authoritative source for the seams and concurrency test shapes above — this spec restates them, it doesn't redefine them.

## Out of Scope

- **S1.T10 (Sprint 1 retro / architecture doc)** — depends on S1.T1 through S1.T9 all `[DONE]`; a separate task, not part of this spec.
- **Deployment target decision** (bare binary vs. Docker vs. Kubernetes) — deliberately deferred to Sprint 4/5 per ADR-0005; the reload/health-check/connection-lifecycle work built in later sprints should inform that choice, not the other way around.
- **Sprint 2 algorithms** (`consistent_hash`, `p2c_ewma`) — stubs remain panics; `implementedAlgorithms` stays `{round_robin, least_conn}` per ADR-0004.
- **Sprint 3 health checking, circuit breaking, metrics** — `SetHealthy` exists (ADR-0006) but nothing calls it outside of S1.T8's tests; no active/passive health checker, no circuit breaker, no Prometheus instruments in this phase.
- **Connection pool tuning** (`MaxIdleConnsPerHost`, `IdleConnTimeout`, `DialContext` timeout, `ResponseHeaderTimeout`) — explicitly Sprint 4 per AGENTS.md.
- **Client cancellation / slow-loris / goroutine-leak hardening** — connection lifecycle correctness is explicitly Sprint 4.
- **Rate limiting, TLS, security hardening** — per ADR-0005's scope of "production-grade."
- **Sprint 5's "LB + Nginx + 4 backends" bench compose** — a separate, more elaborate compose file; S1.T9's compose is 3 backends only.

## Further Notes

### Frozen contracts

All interfaces, struct shapes, and method signatures are frozen by S1.T0.5 (`docs/design/sprint-1-contracts.md`, ADR-0002), amended once by ADR-0006 (`Backend.SetHealthy`). Any further deviation requires a superseding ADR or explicit user sign-off — not a silent change.

### Key principle from this grilling session

**Selection happens in `ServeHTTP`, not `Director`.** AGENTS.md's request-lifecycle description (`ServeHTTP` calls `Select`, checks the error, then `Director` just rewrites the URL) is authoritative; PROGRESS.md's earlier, looser wording ("Director selects via `selector.Select`") was corrected to match during this design review, since `Director`'s signature can't return a response and therefore can't itself implement the 503 short-circuit.

### Health-mutation ownership

`Backend.SetHealthy` is public Go, but its *ownership* — "only the health-check subsystem should call this in Sprint 3+" — is a convention recorded in ADR-0006, not something the type system enforces. Sprint 1 has no caller besides S1.T8's tests to misuse it.

### Why the chaos knobs default non-zero

The entire point of `SLEEP_MS`/`FAIL_RATE` existing is to make `RoundRobin` vs. `LeastConnections` behavior — and, later, Sprint 3's circuit-breaking — visible immediately on `docker compose up`. All-zero defaults would demonstrate nothing without a manual override, which is a worse default for a project meant to be run and inspected by others.
