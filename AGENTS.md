# AGENTS.md

Canonical guidance for any coding agent working on this repository (Claude Code, Antigravity IDE, OpenCode CLI, or others). `CLAUDE.md` and `GEMINI.md` are symlinks to this file.

**Read this file in full before touching any code.** Then read `PROGRESS.md`, `MILESTONES.md`, and every file under `docs/adr/`.

---

## Project

**l7LoadBalancer** — a Layer 7 HTTP load balancer in Go, built entirely on the standard library (`net/http`, `net/http/httputil`). Owner: Darshan Jain.

### Why this project exists

Build a production-grade Layer 7 load balancer that demonstrates:
- Reverse-proxy architecture and request lifecycle in Go.
- Pluggable algorithm design behind a clean interface boundary.
- Concurrency correctness (atomics, channels, mutexes — each used where appropriate, documented why).
- Production resilience patterns (health checking, circuit breaking, graceful reload).
- Honest benchmarking against Nginx with published numbers and reproducible harness.

Every design decision must be **defensible**: if asked "why did you do X?", the answer is either in an ADR, in this file, or in a code comment pointing to one of those.

---

## Where to look first

1. `PROGRESS.md` — current sprint, in-progress and next tasks, session-by-session activity log.
2. `MILESTONES.md` — sprints, goals, deliverables, exit criteria.
3. `docs/design/sprint-1-contracts.md` — frozen interfaces, YAML schema, concurrency ownership table.
4. `docs/architecture.md` — architecture doc (fills in as sprints complete).
5. `docs/adr/` — architecture decision records. **Read them all** before proposing anything that contradicts one.
6. `docs/sessions/` — per-session logs from previous agent sessions.

---

## MANDATORY TASK PROTOCOL — STRICT TDD

Every task, without exception, follows this order. **TDD is not optional.** Tests are written before implementation code. No exceptions, no shortcuts.

### Step 0: Orient

1. Read `AGENTS.md`, `PROGRESS.md`, `MILESTONES.md`.
2. Read `docs/design/sprint-1-contracts.md` (or the current sprint's contract doc).
3. Read every ADR in `docs/adr/`.
4. If any task is `[IN_PROGRESS]` by a different agent or session, **STOP** and ask the user.

### Step 1: Claim

5. Update `PROGRESS.md`: mark task `[IN_PROGRESS]` with agent name, ISO timestamp, one-line description.
6. Commit alone as `chore(progress): start <task>`.

### Step 2: Design (think before coding)

7. Before writing any code, document what you will build:
   - Re-read the task's acceptance criteria in `PROGRESS.md`.
   - Re-read the frozen contract in `docs/design/sprint-1-contracts.md`.
   - Identify the exact interfaces, types, and function signatures you will implement.
   - Identify every design decision. If any decision is non-trivial (why this algorithm? why this concurrency primitive? why this error handling?), draft an ADR **before** writing code.
8. If the task involves a design decision not already covered by an ADR, **write the ADR first**, commit it, and reference it.

### Step 3: Write tests FIRST (Red phase)

9. Write the test file(s) for this task **before** any implementation code.
   - Table-driven tests where the input space is enumerable.
   - `testify/require` for setup assertions, `testify/assert` for value checks.
   - `httptest.NewServer` for integration tests.
   - Concurrency tests using goroutines + `-race` flag.
   - Tests must compile (they can reference types/functions from the frozen stubs).
   - Tests must **fail** when run against the panic stubs — this is the "Red" in Red-Green-Refactor.
10. Run `go test ./internal/<pkg>/... -run TestXxx` — confirm tests fail with the expected panic or assertion failure.
11. Commit test files alone as `test(<pkg>): add tests for <what>` (optional but encouraged for traceability).

### Step 4: Implement (Green phase)

12. Write the minimum implementation to make all tests pass.
13. Run `make test` — all tests green.
14. Run `make test-race` — no races.
15. Run `go vet ./...` — no warnings.
16. Run `make fmt` — no formatting diff.

### Step 5: Refactor

17. Clean up the implementation: improve naming, reduce duplication, simplify.
18. Re-run `make test-race` after every refactor. If it fails, **revert the refactor**.
19. Ensure all existing comments and docstrings unrelated to your changes are preserved.

### Step 6: Close out

20. Update `PROGRESS.md`: mark task `[DONE]` with completion timestamp.
21. Commit code + `PROGRESS.md` update together as **ONE** atomic commit.
22. Append to `docs/sessions/<YYYY-MM-DD>-<agent>.md`.
23. If a design decision was made during implementation, write an ADR if one wasn't already written in Step 2.

### TDD exception: docs-only and infra-only tasks

Tasks that produce no Go logic (e.g. S1.T0.5 schema freeze, S1.T10 retro, S1.T9 docker-compose) are exempt from the Red-Green-Refactor cycle. They still follow Steps 0–2 and 6.

---

## What we are building — full implementation documentation

### Core architecture

The load balancer layers a **pluggable backend-selection policy** over Go's `net/http/httputil.ReverseProxy`. The request path is:

```
Client → http.Server → Proxy.ServeHTTP → Selector.Select → ReverseProxy.Director → Backend → Response
                                                                                         ↓
                                                                          ModifyResponse (body wrapper)
                                                                                         ↓
                                                                              DecActive on Close
```

**Why `httputil.ReverseProxy`?** It handles hop-by-hop header stripping, `X-Forwarded-For`, buffering, and flushing. Building these from scratch would add code without adding value. The interesting work is in the **selection layer**, **concurrency model**, **resilience patterns**, and **lifecycle management** that wrap around it.

### Package dependency graph (acyclic — no cycles allowed)

```
cmd/l7LoadBalancer
      │
      ▼
   internal/proxy ──────┐
      │                 │
      ▼                 ▼
internal/balancer → internal/backend
      │                 │
      └───────┬─────────┘
              ▼
        internal/config

internal/logger   — leaf, no internal deps
internal/metrics  — leaf, no internal deps (Sprint 3)
internal/health   — depends on backend (Sprint 3)
internal/circuit  — depends on backend (Sprint 3)
```

**Rule**: `internal/backend` does NOT import `internal/balancer`. A `Backend` has no notion of how it's selected. If a future task is tempted to add that import, it's a sign the abstraction is leaking.

### Component details

#### `internal/config` — YAML loading and validation

**Concept**: Strict deserialization. Unknown YAML fields are an error, not silently ignored.

- **`Config` struct**: `Listen string`, `Algorithm string` (defaults to `"round_robin"`), `Backends []BackendConfig`.
- **`BackendConfig`**: `Name string`, `URL string`.
- **`Load(path) (*Config, error)`**: Uses `yaml.NewDecoder(f).KnownFields(true)` — NOT `yaml.Unmarshal`. Why: `KnownFields(true)` catches typos (`listn` instead of `listen`) at load time rather than silently ignoring them.
- **`Validate() error`**: Rejects empty `Listen`, zero backends, unparseable/hostless backend URLs, duplicate backend names, unrecognized `Algorithm`.
- **Error convention**: `fmt.Errorf("config: ...: %w", err)` — no exported sentinel errors. Callers don't need to distinguish "empty listen" from "bad URL" programmatically.
- **Algorithm identifier table** (frozen in ADR-0002):

  | Config string      | Const                            | Selector type                       | Sprint |
  |--------------------|----------------------------------|-------------------------------------|--------|
  | `round_robin`      | `config.AlgorithmRoundRobin`     | `balancer.RoundRobin`               | 1      |
  | `least_conn`       | `config.AlgorithmLeastConn`      | `balancer.LeastConnections`         | 1      |
  | `consistent_hash`  | `config.AlgorithmConsistentHash` | `balancer.ConsistentHashBoundedLoads` | 2    |
  | `p2c_ewma`         | `config.AlgorithmP2CEWMA`        | `balancer.PowerOfTwoChoicesEWMA`    | 2      |

- **Hot-reload** (Sprint 4): `atomic.Pointer[Config]` swap on SIGHUP. Sprint 1 config is immutable after init.

#### `internal/backend` — Backend struct and Registry

**Concept**: Concurrency-safe state tracking using atomic operations, with all mutable fields encapsulated behind methods.

- **`Backend` struct**:
  - Exported: `Name string`, `URL *url.URL`.
  - Unexported (ADR-0002 decision 5): `healthy atomic.Bool`, `active atomic.Int64`.
  - Methods: `IsHealthy() bool`, `IncActive()`, `DecActive()`, `ActiveConns() int64`.
  - **Why unexported fields?** Callers in `balancer` and `proxy` access state through methods. Sprint 3 can replace `atomic.Bool` with a richer health-state enum without touching any caller. Compile-time enforcement: you literally can't access the field from outside the package.

- **`Registry` struct**:
  - `NewRegistry(cfgs []config.BackendConfig) (*Registry, error)` — builds backends from validated config.
  - `All() []*Backend` — fresh slice per call. Safe to iterate concurrently.
  - `Healthy() []*Backend` — fresh slice of healthy backends per call.
  - **Sprint 1**: immutable after construction. **Sprint 4**: `atomic.Pointer` swap for hot-reload.

- **Concurrency model**:

  | Field | Writer | Readers | Sync mechanism |
  |-------|--------|---------|----------------|
  | `Backend.healthy` | Health checker (Sprint 3); `NewRegistry` (Sprint 1) | Selectors via `IsHealthy()`, proxy, metrics | `atomic.Bool` |
  | `Backend.active` | Proxy: `IncActive()` before dispatch, `DecActive()` on body `Close()` | `LeastConnections` via `ActiveConns()`, metrics | `atomic.Int64` |
  | Registry backend set | `NewRegistry` at construction | `All()`, `Healthy()`, all selectors | Immutable (Sprint 1) / `atomic.Pointer` swap (Sprint 4) |

#### `internal/balancer` — Selector interface and implementations

**Concept**: Strategy pattern. The `Selector` interface is the seam between the proxy and the selection algorithm. New algorithms are added by implementing one method.

- **`Selector` interface** (defined in `balancer`, not `proxy` — see ADR-0002):
  ```go
  type Selector interface {
      Select(ctx context.Context, r *http.Request) (*backend.Backend, error)
  }
  ```
  **Why in `balancer`?** Four implementations live here. `proxy` only stores and calls the interface. Consumer-side placement would add an import inversion for zero isolation benefit. See ADR-0002 decision 2.

- **`ErrNoHealthyBackends`** — the **only** exported sentinel error in the project. `proxy` branches on `errors.Is(err, ErrNoHealthyBackends)` to decide 503 vs 502. All other errors use wrapping, not sentinels.

- **`NewFromConfig(cfg, reg) (Selector, error)`** — selector factory. Lives in `balancer` (not `main.go`) because `balancer` owns the config-string-to-type mapping. `main.go` stays a thin wiring layer.

##### Sprint 1 selectors

**RoundRobin** (S1.T4):
- **Algorithm**: Rotate across `Registry.Healthy()` using an atomic counter (`atomic.Uint64`). No mutex — lock-free.
- **Why atomic counter over mutex?** Round-robin rotation is a single increment; `atomic.AddUint64` is cheaper than `sync.Mutex.Lock/Unlock` and cannot deadlock. The counter value is never "stale" in a meaningful way — if two goroutines race, the result is still a valid round-robin rotation.
- Compile-time assertion: `var _ Selector = (*RoundRobin)(nil)`.

**LeastConnections** (S1.T5):
- **Algorithm**: Scan `Registry.Healthy()`, pick the backend with the lowest `ActiveConns()`. Ties broken by registry order (first-wins).
- **Why deterministic tie-breaking?** Reproducible tests. If ties were broken randomly, assertions on "which backend was chosen" would flake.
- **Reads `ActiveConns` atomically; does not mutate it.** Mutation is the proxy's job (S1.T6). This separation of concerns means the selector is stateless with respect to connection tracking.
- Compile-time assertion: `var _ Selector = (*LeastConnections)(nil)`.

##### Sprint 2 selectors

**ConsistentHashBoundedLoads** (Sprint 2):
- **Algorithm**: Consistent hashing per Mirrokni-Thorup-Zadimoghaddam (2016). Per-backend load counters. When the first-choice backend exceeds `avg_load * (1 + ε)`, rehash to the next virtual node.
- **Why bounded-loads over naive consistent hashing?** Naive consistent hashing can send all traffic for a hot key to one backend. Bounded-loads provides the sticky-routing property of consistent hashing while guaranteeing no backend exceeds `(1 + ε)` times the average load. ADR required (Sprint 2).
- **Design decisions to document in ADR**:
  - Hash function choice (xxhash vs FNV vs CRC32).
  - Number of virtual nodes per backend.
  - Epsilon (ε) value and whether it's configurable.
  - Hash key: `r.URL.Path` or `r.Header.Get("X-Forwarded-For")` or configurable.

**PowerOfTwoChoicesEWMA** (Sprint 2):
- **Algorithm**: Pick two random healthy backends; choose the one with lower EWMA-tracked latency. Latency updated atomically on each response.
- **Why P2C over LeastConnections when latency is skewed?** LeastConnections treats all connections as equal; P2C-EWMA favors backends that are actually responding faster, naturally shifting load away from degraded-but-not-dead backends. ADR required (Sprint 2).
- **EWMA smoothing factor** (α): determines how quickly the latency estimate adapts. Too high = noisy, too low = slow to react. Document the choice in the ADR.
- **Design decisions to document in ADR**:
  - EWMA α value.
  - What to do when fewer than 2 backends are healthy (fallback to only choice, or error?).
  - Atomic latency representation (int64 nanoseconds? fixed-point?).

#### `internal/proxy` — Reverse proxy handler

**Concept**: Wrap `httputil.ReverseProxy`. Each request's `Director` consults the `Selector`, rewrites the target, and `ModifyResponse` installs a body wrapper that decrements `ActiveConns` on `Close()`.

- **`New(reg, sel) *Proxy`** → returns an `http.Handler`.
- **Request lifecycle**:
  1. `ServeHTTP` calls `sel.Select(ctx, r)`.
  2. If `ErrNoHealthyBackends` → respond 503 immediately.
  3. Otherwise, `IncActive()` on the chosen backend.
  4. `Director` rewrites `req.URL.Scheme` and `req.URL.Host`.
  5. `ReverseProxy` dispatches the request.
  6. `ModifyResponse` wraps `resp.Body` with a `Close()` that calls `DecActive()` **exactly once**.
  7. `ErrorHandler` also decrements if the backend fails mid-response.
- **Why decrement in body `Close()` and not in `ModifyResponse` directly?**
  The response body may be streamed. If we decrement in `ModifyResponse`, we signal "request done" while the body is still being read. The body wrapper ensures we decrement only after the client has consumed (or abandoned) the response. This is critical for `LeastConnections` accuracy.
- **ActiveConns leak prevention**: Test with 100 concurrent in-flight requests; assert `ActiveConns` returns to 0 within a drain window.

#### `internal/logger` — Structured logging

- `log/slog` only. JSON output via `slog.NewJSONHandler`.
- **Canonical field names** (frozen, see `internal/logger/doc.go`):
  `backend`, `method`, `status`, `latency_ms`, `remote_addr`, `path`.
- **Why structured logging?** Grep-able, machine-parseable, consistent across all log lines from day one.

#### `internal/metrics` — Prometheus instruments (Sprint 3)

- **Metric name prefixes**: `lb_requests_total`, `lb_request_duration_seconds`, `lb_backend_healthy`, `lb_circuit_state`.
- **Labels**: `backend`, `method`, `status_class` (NOT `status_code`).
- **Why `status_class` over `status_code`?** Unbounded cardinality from arbitrary upstream status codes would make Prometheus time series explode. `2xx`/`4xx`/`5xx` is sufficient for alerting and dashboarding.
- **Histogram buckets** (proposed, to be tuned with real data): `.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10` seconds.

#### `internal/health` — Health checking (Sprint 3)

- **Active health checks**: One goroutine per backend, HTTP probe at configurable interval. State machine: N consecutive failures → unhealthy, M consecutive successes → healthy.
- **Passive outlier detection**: Sliding window of recent request outcomes. Eject after N 5xx responses in the window.
- **Why both active and passive?** Active catches backends that are down (no traffic reaching them). Passive catches backends that are up but returning errors under load. Together they cover the full failure spectrum.

#### `internal/circuit` — Circuit breaker (Sprint 3)

- **State machine**: Closed → Open → Half-Open → Closed.
  - **Closed**: All requests pass through. Track failure count.
  - **Open**: All requests immediately rejected (503). Timer-based cooldown.
  - **Half-Open**: Allow one trial request. If it succeeds → Closed. If it fails → Open again.
- **Why per-backend?** A single global circuit breaker would trip when any backend fails, taking down routing to all backends. Per-backend isolation means one failing backend doesn't affect the others.
- **Concurrency**: Likely mutex, not atomic — closed→open→half-open transitions are compound (check-then-act). Decide in Sprint 3 ADR.

#### `cmd/l7LoadBalancer/main.go` — Entry point

- Thin wiring layer: config.Load → backend.NewRegistry → balancer.NewFromConfig → proxy.New → http.Server.
- Graceful shutdown on SIGINT/SIGTERM (already scaffolded).
- Sprint 4 adds SIGHUP for zero-downtime reload.

### Sprint 4 — Hard subsystems

- **Zero-downtime SIGHUP reload**: `atomic.Pointer[Config]` swap. Diff-based backend add/remove. Draining state for removed backends with configurable drain window. ADR: atomic pointer swap vs SO_REUSEPORT.
- **Connection lifecycle correctness**: Context propagation client→backend. Clean handling of client cancellation mid-stream, backend death mid-response, slow-loris timeouts. `pprof` audit for goroutine leaks.
- **Connection pool tuning**: `http.Transport` settings: `MaxIdleConnsPerHost`, `IdleConnTimeout`, `DialContext` with timeout, `ResponseHeaderTimeout`.
- **ADR: retry policy** (or the deliberate absence of one) — idempotency concerns, amplification risk.

### Sprint 5 — HTTP/2, benchmarks, docs

- **HTTP/2 client-facing**: TLS + ALPN, h2c via `golang.org/x/net/http2/h2c`.
- **HTTP/2 to backends**: `http.Transport` with `ForceAttemptHTTP2: true`.
- **Benchmark rig**: docker-compose with LB + Nginx + 4 backends. wrk and vegeta. Response-size matrix (200B, 10KB, 1MB). Concurrency sweep. Failure-mode benchmarks.
- **Published numbers**: p50 / p99 / p99.9 at 50% and 90% of peak.
- **Design decisions doc**: why stdlib, why bounded-loads CH, why P2C, reload architecture, honest Nginx comparison.

---

## Design decisions index

All non-trivial decisions must have an ADR. Current ADRs:

| ADR | Title | Status |
|-----|-------|--------|
| [0001](docs/adr/0001-record-adrs.md) | Record architecture decisions | Accepted |
| [0002](docs/adr/0002-interface-placement-and-schema-freeze.md) | Interface placement and Sprint 1 schema freeze | Accepted |
| [0003](docs/adr/0003-pin-unused-deps-with-tools-go.md) | Pin not-yet-imported dependencies with a build-tagged tools.go | Accepted |
| [0004](docs/adr/0004-reject-unimplemented-algorithms-in-validate.md) | Reject unimplemented algorithms in Validate | Accepted |
| [0005](docs/adr/0005-scope-of-production-grade.md) | Scope of "production-grade" | Accepted |
| [0006](docs/adr/0006-backend-sethealthy-amends-adr-0002.md) | Add Backend.SetHealthy, amending ADR-0002 decision 5 | Accepted |
| [0007](docs/adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md) | Proxy request lifecycle and exactly-once active-connection decrement | Accepted |
| TBD (Sprint 2) | Why bounded-loads consistent hashing over naive CH | — |
| TBD (Sprint 2) | Why P2C-EWMA over least-connections for latency-skewed workloads | — |
| TBD (Sprint 3) | Circuit breaker concurrency model | — |
| TBD (Sprint 4) | Reload architecture: atomic pointer swap vs SO_REUSEPORT | — |
| TBD (Sprint 4) | Deployment target decision (deferred from Sprint 1 per ADR-0005) | — |
| TBD (Sprint 4) | Retry policy (or deliberate absence) | — |

**Key decisions already made and documented:**

1. **`Selector` in `balancer`, not `proxy`** — ADR-0002. `balancer` has four implementations; `proxy` only calls the interface. Consumer-side placement adds nothing.
2. **`NewFromConfig` factory in `balancer`, not `main.go`** — ADR-0002. The package that knows the mapping should own the factory. `main.go` stays thin.
3. **Algorithm identifiers are snake_case constants in `config`** — ADR-0002. YAML-readable, matched 1:1 in `NewFromConfig`.
4. **Backend fields are unexported, methods-only access** — ADR-0002. Compile-time encapsulation lets Sprint 3 change internal representation.
5. **Contract freeze before implementation** — ADR-0002. Prevents cross-session integration drift.
6. **Log/metric names frozen before request-path code exists** — ADR-0002. Cheaper than grep-and-rename later.
7. **`ErrNoHealthyBackends` is the only sentinel** — `docs/design/sprint-1-contracts.md`. All other errors are wrapped with `fmt.Errorf`.
8. **`httputil.ReverseProxy` over hand-rolled proxy** — stated in architecture summary. Handles hop-by-hop headers, `X-Forwarded-For`, buffering. The interesting work is the selection layer.
9. **Atomic counter for RoundRobin, not mutex** — single increment doesn't need mutual exclusion.
10. **Deterministic tie-breaking in LeastConnections** — first-in-registry-order wins. Enables reproducible tests.
11. **ActiveConns decremented in body `Close()`, not `ModifyResponse`** — prevents counting streamed-but-incomplete responses as "done".
12. **Not-yet-imported deps pinned via a build-tagged `tools.go`** — ADR-0003. `go mod tidy` prunes unused modules, so S1.T1's two new deps are blank-imported under `//go:build tools` until S1.T2 imports them for real.
13. **"Production-grade" is explicitly scoped** — ADR-0005. Demonstrates production LB patterns with defensible decisions and honest benchmarking; explicitly excludes security hardening/WAF, cert rotation, kernel/OS tuning, SLO alerting, formal security review, multi-tenancy, secrets management beyond env-var interpolation, disaster recovery, capacity planning/SLA, and a settled deployment target.
14. **`Backend.SetHealthy(bool)` amends ADR-0002 decision 5** — ADR-0006. Added in S1.T3 so S1.T8 can drive health transitions before Sprint 3's health checker exists; Sprint 3 reuses it unchanged. Symmetric with `IncActive`/`DecActive` living directly on `Backend`.
15. **Proxy per-request state + `sync.Once` release** — ADR-0007. `ServeHTTP` selects, `IncActive`s, and attaches a `reqState` (backend, status, once) to the request context; both the `ModifyResponse` body-wrapper `Close()` and `ErrorHandler` call `reqState.release()`, so `DecActive` runs exactly once. Status is read from `resp.StatusCode` (no `ResponseWriter` wrapper, preserving flush/hijack), and one "request complete" line is logged per request via a deferred call in `ServeHTTP`.

---

## Directory map

- `cmd/l7LoadBalancer/` — binary entry point.
- `internal/proxy/` — the reverse-proxy handler, request path wiring.
- `internal/backend/` — backend struct, registry, state management.
- `internal/balancer/` — `Selector` interface + implementations (roundrobin, leastconn, consistent-hash-bounded, p2c-ewma).
- `internal/health/` — active health checks + passive outlier detection.
- `internal/circuit/` — per-backend circuit breaker state machine.
- `internal/config/` — YAML loading, validation, hot-reload (atomic pointer swap).
- `internal/metrics/` — Prometheus registration and instruments.
- `internal/logger/` — `log/slog` setup.
- `configs/` — example configuration files.
- `deployments/docker/` — dockerfiles, docker-compose for dummy backends and Nginx comparison.
- `bench/` — wrk / vegeta benchmark scripts and results.
- `docs/` — architecture, ADRs, design docs, session logs.
- `docs/design/` — frozen contract documents per sprint.
- `docs/adr/` — architecture decision records.
- `docs/sessions/` — per-session agent logs.
- `scripts/` — helper scripts.

---

## Commands

All commands via `make`. Run `make help` to list them. Common:

- `make build` — build the binary into `bin/l7LoadBalancer`.
- `make run` — build and run with `configs/example.yaml`.
- `make test` — run all tests.
- `make test-race` — run tests with `-race`.
- `make bench` — run Go benchmarks.
- `make fmt` — `gofmt` and `goimports`.
- `make tidy` — `go mod tidy`.
- `make vet` — run `go vet ./...`.
- `make clean` — remove build artifacts.

---

## Code conventions

- **Language**: Go 1.22+.
- **Style**: `gofmt` + `goimports`. No exceptions.
- **Packages**: lowercase, single word where possible. No `util`, no `common`.
- **Errors**: wrap with `fmt.Errorf("context: %w", err)`. No sentinel `errors.New` at call sites for dynamic messages. The only exported sentinel is `balancer.ErrNoHealthyBackends`.
- **Contexts**: every request-scoped function takes `context.Context` as first arg.
- **Interfaces**: define at the consumer, not the producer. Small interfaces preferred. **Exception**: `Selector` in `balancer` — see ADR-0002.
- **Concurrency**: prefer channels for coordination, atomics for counters, mutex for state. Document the concurrency model at the top of every file with shared state. See the concurrency ownership table in `docs/design/sprint-1-contracts.md`.
- **Logging**: `log/slog` only. Structured key-value. No `fmt.Println` outside of tests. Use canonical field names from `internal/logger/doc.go`.
- **Testing (TDD)**:
  - **Tests are written BEFORE implementation.** Always.
  - Table-driven where the input space is enumerable.
  - `testify/require` for setup assertions (fail fast), `testify/assert` for value checks (see all failures).
  - `httptest.NewServer` for integration tests.
  - Concurrency tests with goroutines + `go test -race`.
  - Every task's test file is committed before or with the implementation — never after, never "later".

---

## TDD workflow reference

For every implementation task (T2–T8):

```
1. Write _test.go           ← RED: tests compile, tests FAIL
2. Run: go test ./...       ← Confirm failure (panic or assertion)
3. Write implementation     ← GREEN: minimum code to pass
4. Run: make test-race      ← Confirm pass + no races
5. Refactor                 ← REFACTOR: clean up
6. Run: make test-race      ← Confirm still passing
```

**What "minimum code to pass" means**: Don't gold-plate. Write the simplest correct implementation. Optimize only when benchmarks (Sprint 5) show a need. The refactor phase is for readability and structure, not premature optimization.

**What to test for each component**:

| Component | Test cases |
|-----------|------------|
| `config.Load` | Valid YAML, missing file, invalid YAML syntax, unknown fields rejected |
| `config.Validate` | Missing listen, zero backends, malformed URL, hostless URL, duplicate names, unknown algorithm, empty algorithm defaults to round_robin |
| `backend.Backend` | IsHealthy reads initial state, IncActive/DecActive/ActiveConns correctness, concurrent access under -race |
| `backend.Registry` | Construction from config, All() returns all, Healthy() filters, concurrent health toggle + ActiveConns under -race |
| `balancer.RoundRobin` | Cyclic order over N backends, empty registry → ErrNoHealthyBackends, concurrent 1000 selects within ±5% expected distribution |
| `balancer.LeastConnections` | Pre-seeded ActiveConns → minimum chosen, tie-break by registry order, empty registry → ErrNoHealthyBackends |
| `balancer.selector_test` (cross-cutting) | Interface conformance assertions, health transition mid-run (both selectors) |
| `proxy.Proxy` | Distribution matches selector, no-healthy → 503, ActiveConns returns to 0 after 100 concurrent requests |

---

## Git commit convention

Conventional Commits:
- `feat:` new capability
- `fix:` bug fix
- `refactor:` internal restructuring, no behavior change
- `test:` tests only (use for Red-phase test commits)
- `docs:` docs only
- `chore:` scaffold, tooling, deps
- `bench:` benchmark work
- `perf:` performance improvement

Commit messages: imperative mood, lowercase after prefix, no trailing period. Reference sprint/task from `PROGRESS.md` when relevant.

**TDD commit pattern** (encouraged but not mandatory — the atomic task commit is mandatory):
```
test(config): add table-driven tests for Load and Validate    ← Red phase
feat(config): implement Load and Validate                      ← Green phase
refactor(config): simplify validation loop                     ← Refactor phase
```

Or as a single atomic commit if preferred:
```
feat(config): implement Load and Validate with tests (S1.T2)
```

---

## Explicitly deferred until working prototype exists

Per the user's discipline: do NOT set up any of these until Sprint 3 or later, and only if the user asks:

- Pre-commit hooks
- CI (GitHub Actions, etc.)
- MCP servers
- Linters beyond `gofmt` / `goimports` (e.g. no `golangci-lint` config yet)
- Dependabot / Renovate

---

## What NOT to do without asking

- Do not change `go.mod` module path.
- Do not add heavyweight dependencies (frameworks, ORMs, config libraries like Viper).
- Do not restructure `internal/` package boundaries.
- Do not add features not in `MILESTONES.md`. Propose them to the user first; if accepted, add to `MILESTONES.md` before coding.
- **Do not skip tests to move faster.** Tests are written first. Always.
- Do not commit if `make test-race` fails.
- Do not deviate from frozen contracts without writing an ADR first.
- Do not silently change function signatures, struct fields, or error types that are documented in `docs/design/sprint-1-contracts.md`.

---

## Concepts and patterns used

This section documents every non-trivial concept, pattern, and algorithm used in the project, so that any agent or contributor can explain **what** it is and **why** it was chosen.

### Reverse proxy pattern
- Go's `httputil.ReverseProxy` intercepts an incoming request, rewrites the destination via `Director`, forwards it to a backend, and streams the response back.
- Custom behavior injected via `Director` (rewrite URL), `ModifyResponse` (wrap body), `ErrorHandler` (handle backend failure).

### Strategy pattern (Selector interface)
- The `Selector` interface decouples "how to choose a backend" from "how to proxy a request."
- New algorithms are added by implementing `Select(ctx, r) (*Backend, error)` — zero changes to `proxy` code.

### Atomic operations for lock-free concurrency
- `atomic.Bool` for health state: single-bit flag, read on every request, written rarely (health checks). No contention benefit from a mutex.
- `atomic.Int64` for active connection count: increment/decrement on every request. Lock-free is crucial on the hot path.
- `atomic.Uint64` for round-robin counter: single increment per request.

### Consistent hashing with bounded loads (Sprint 2)
- **Paper**: Mirrokni, Thorup, Zadimoghaddam (2016). "Consistent Hashing with Bounded Loads."
- **Key idea**: Map requests to backends via a hash ring, but enforce a load cap of `avg_load * (1 + ε)`. When the primary backend exceeds the cap, walk the ring to the next under-capacity backend.
- **Property**: Provides session-sticky routing (same key → same backend) while preventing hot-spot overload.

### Power of Two Choices with EWMA latency (Sprint 2)
- **Key idea**: Pick two random backends, route to the one with lower exponentially weighted moving average latency.
- **EWMA**: `latency_new = α * observed + (1 - α) * latency_old`. Smooths out spikes while adapting to sustained changes.
- **Why P2C over pure random or round-robin?** With just two random samples, P2C achieves exponentially better load balance than single random choice (Mitzenmacher, "The Power of Two Choices in Randomized Load Balancing", 2001).

### Circuit breaker state machine (Sprint 3)
- **States**: Closed (normal) → Open (rejecting, on failure threshold) → Half-Open (trial request, on timeout) → Closed (on trial success) or Open (on trial failure).
- **Purpose**: Prevent cascading failure. Stop sending traffic to a backend that's clearly broken, give it time to recover, then cautiously test it.

### Active + passive health checking (Sprint 3)
- **Active**: Out-of-band HTTP probes. Detects "backend is down" even with zero traffic.
- **Passive**: In-band response monitoring. Detects "backend is up but returning errors" under load.
- **State machine**: N consecutive failures → unhealthy. M consecutive successes → healthy. Prevents flapping.

### Graceful shutdown and zero-downtime reload (Sprint 4)
- **Graceful shutdown**: `http.Server.Shutdown` drains in-flight requests before stopping.
- **SIGHUP reload**: Swap config via `atomic.Pointer[Config]`. Diff backends: add new, drain removed (configurable window), keep unchanged.
- **atomic.Pointer[Config]**: Single atomic swap means readers never see a partially-updated config. No mutex needed on the read path.

### Connection lifecycle management (Sprint 4)
- Context propagation: client cancellation → backend request cancelled.
- Body close: ensures `ActiveConns` decremented even on client disconnect mid-stream.
- Slow-loris protection: `ReadHeaderTimeout`, `WriteTimeout`, `IdleTimeout` on the server; `DialContext` timeout, `ResponseHeaderTimeout` on the backend transport.

---

## Agent skills

### Issue tracker

Local markdown under `.scratch/<feature-slug>/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five canonical role names (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context (`CONTEXT.md` + `docs/adr/` at repo root). See `docs/agents/domain.md`.

### Cross-tool skill availability

Skills are copied into a gitignored, project-level `.agents/skills/` (in addition to the home-level `~/.agents/skills/`) so Antigravity IDE and OpenCode CLI can use them too. If a skill isn't registered by the tool you're using, its slash form won't actually run it. See `docs/agents/skills.md`.
