# PROGRESS

Live state of the project. Every agent updates this file per the protocol in `AGENTS.md`.

## Current status

**S1.T2 in progress** by opencode (started 2026-09-17T22:12:47Z). S1.T1 complete. S1.T2: implement `internal/config` Load/Validate + tests + `configs/example.yaml`.

## Sprint 1 — Foundation

- [DONE] S1.T0 — Repository scaffold (bootstrap agent, 2026-08-31T00:00:00Z, commit: 25c1952)

- [DONE] S1.T0.5 — Interface & Schema Freeze (Phase A) (claude, started 2026-09-01T16:04:36Z, completed 2026-09-01T16:35:00Z)
  - Goal: freeze all cross-package contracts as compilable Go stubs + a design doc before any Sprint 1 implementation begins, so T1–T10 (potentially executed by different agents/sessions) cannot silently diverge.
  - Files: `internal/config/config.go`; `internal/backend/backend.go`, `internal/backend/registry.go`; `internal/balancer/selector.go`, `roundrobin.go`, `leastconn.go`, `consistent_hash.go`, `p2c_ewma.go`; `internal/proxy/proxy.go`; `internal/logger/logger.go` (+doc.go); `internal/metrics/metrics.go` (+doc.go); `internal/health/health.go`; `internal/circuit/circuit.go`; `docs/design/sprint-1-contracts.md`; `docs/adr/0002-interface-placement-and-schema-freeze.md`
  - Depends on: S1.T0
  - Acceptance: `go build ./...` and `go vet ./...` pass with every stubbed body either `panic("not implemented: <task-id>")` or trivial (`logger.New`); compile-time `var _ Selector = (*X)(nil)` assertions in every selector file; design doc covers dependency graph, interface placement, YAML schema, algorithm identifier table, error convention, log field vocabulary, metric reservations, concurrency ownership table; ADR-0002 written; no changes to `main.go` behavior (still returns 501).
  - Test approach: none — Phase A is analysis/contract-definition only, no logic, no test files.

- [DONE] S1.T1 — Add Go dependencies (opencode, started 2026-09-17T21:57:24Z, completed 2026-09-17T22:01:39Z)
  - Goal: pull in the third-party deps Sprint 1 needs (YAML parsing, test assertions) so later tasks aren't blocked on dependency wrangling.
  - Files: `go.mod`, `go.sum` (both tool-managed, not hand-edited)
  - Depends on: S1.T0
  - Acceptance:
    - `go.mod` requires `gopkg.in/yaml.v3` and `github.com/stretchr/testify`.
    - `go.sum` generated and committed.
    - `go mod tidy` produces no further diff after this task.
    - `go build ./...` and `go vet ./...` still pass.
  - Test approach: none (dependency-only); gated by existing build/vet checks.

- [IN_PROGRESS] S1.T2 — Implement `internal/config` (opencode, started 2026-09-17T22:12:47Z)
  - Goal: load and strictly validate the YAML config (listen address + backend list + algorithm choice) into a typed struct, as the canonical config source for the registry and proxy.
  - Files: `internal/config/config.go`, `internal/config/config_test.go`, `configs/example.yaml` (updated to a realistic 3-backend example)
  - Depends on: S1.T1
  - Acceptance:
    - `Config` struct: `Listen string`; `Backends []BackendConfig` (`Name`, `URL` string fields at minimum); `Algorithm string`, defaulting to `"round_robin"` when empty.
    - `Load(path string) (*Config, error)` reads + unmarshals YAML.
    - Unknown YAML fields cause `Load` to return an error. Use `yaml.NewDecoder(f).KnownFields(true)`; do not use `yaml.Unmarshal` directly.
    - `Validate() error` rejects: empty `Listen`, zero backends, a backend whose `URL` fails `url.Parse` or has no host, duplicate backend names, unrecognized `Algorithm` value.
    - Errors wrapped `fmt.Errorf("config: ...: %w", err)` per AGENTS.md.
    - `configs/example.yaml` updated to a valid 3-backend config that passes `Validate()`.
  - Test approach: table-driven tests covering valid config, missing listen, empty backends, malformed URL, duplicate names, unknown algorithm, unknown field. testify/require for setup, testify/assert for values.

- [TODO] S1.T3 — Implement `internal/backend`
  - Goal: define `Backend` and a concurrency-safe `Registry` tracking identity, health, and active-connection count, since balancer and proxy both read/mutate this under concurrent requests.
  - Files: `internal/backend/backend.go`, `internal/backend/registry.go`, `internal/backend/registry_test.go` (replaces the placeholder `backend_test.go`)
  - Depends on: S1.T2
  - Acceptance:
    - `Backend`: exported `Name string` and `URL *url.URL`; unexported `healthy` (`atomic.Bool`) and `active` (`atomic.Int64`) fields, reachable only via methods `IsHealthy()`, `IncActive()`, `DecActive()`, `ActiveConns() int64`. This matches the frozen contract in `docs/design/sprint-1-contracts.md` and ADR-0002.
    - `Backend` exposes an `IsHealthy() bool` method; downstream code (balancer, proxy) accesses health only via this method, never via the `Healthy` field directly. This isolates the field type from callers so Sprint 3 can replace `atomic.Bool` with a state enum without touching balancer or proxy code.
    - `NewRegistry(cfgs []config.BackendConfig) (*Registry, error)` builds backends from validated config.
    - `Registry.All()` and `Registry.Healthy()` each return a fresh slice per call — safe to iterate concurrently with registry mutation, no lock held by caller.
    - Concurrent `ActiveConns` increment/decrement and health toggling from multiple goroutines is race-free under `go test -race`.
    - Placeholder `TestScaffold` removed, replaced with real tests.
  - Test approach: table-driven tests for construction/filtering; a concurrent test with N goroutines mutating `ActiveConns`, asserting the final count under `-race`.

- [TODO] S1.T4 — Implement RoundRobin selector
  - Goal: implement round-robin backend selection behind a Selector interface.
  - Files: `internal/balancer/selector.go` (Selector interface + `ErrNoHealthyBackends`), `internal/balancer/roundrobin.go`, `internal/balancer/roundrobin_test.go`
  - Depends on: S1.T3
  - Acceptance:
    - `Selector` interface: `Select(ctx, r *http.Request) (*backend.Backend, error)`.
    - `RoundRobin` selects only from `Registry.Healthy()`, uses an atomic counter (no mutex) for rotation, safe under concurrent access.
    - Returns `ErrNoHealthyBackends` when the healthy set is empty.
  - Test approach: table-driven cyclic-order test over 3 backends; empty-registry error case; concurrent test firing 1000 selects, each backend chosen within ±5% of the expected 1/3 share.

- [TODO] S1.T5 — Implement LeastConnections selector
  - Goal: pick the healthy backend with the lowest current ActiveConns.
  - Files: `internal/balancer/leastconn.go`, `internal/balancer/leastconn_test.go`
  - Depends on: S1.T4
  - Acceptance:
    - `LeastConnections` implements `Selector`.
    - Ties broken deterministically (first tied backend in registry order) so tests are reproducible.
    - Returns `ErrNoHealthyBackends` when no healthy backends.
    - Reads `ActiveConns` atomically; does not itself mutate it (mutation is the proxy's job, wired in S1.T6).
  - Test approach: table-driven test with pre-seeded `ActiveConns` values asserting the minimum is chosen; tie-break case; empty-registry case.

- [TODO] S1.T6 — Implement `internal/proxy`
  - Goal: wrap httputil.ReverseProxy so each request's Director consults the configured Selector, rewrites the target, and tracks ActiveConns around the round trip.
  - Files: `internal/proxy/proxy.go`, `internal/proxy/proxy_test.go`
  - Depends on: S1.T3, S1.T4, S1.T5
  - Acceptance:
    - `New(registry *backend.Registry, selector balancer.Selector) http.Handler` wraps `ReverseProxy`.
    - Director selects via `selector.Select`, sets `req.URL.Scheme/Host` to the chosen backend, and records which backend was chosen so `ActiveConns` can be decremented after the response completes.
    - No healthy backend → responds 503, no panic, no hang.
    - `ActiveConns` incremented before dispatch, decremented after response completes (success, error, or timeout) — no leak across many sequential requests.
    - `ActiveConns` decrement uses a response-body wrapper whose `Close()` decrements exactly once. Wrap the body in `ModifyResponse`. Do NOT decrement in `Director`, in `ModifyResponse` directly, or only in `ErrorHandler`. Test with 100 concurrent (not sequential) in-flight requests and assert `ActiveConns` returns to 0 within a small drain window.
  - Test approach: httptest.NewServer fake backends returning an identifying body; httptest-wrapped proxy in front of them; assert distribution matches the selector; no-healthy-backend → 503; concurrent-request test asserting ActiveConns returns to 0.

- [TODO] S1.T7 — Wire `cmd/l7LoadBalancer/main.go` end-to-end
  - Goal: replace the placeholder 501 handler with the real path: load config → build registry → build selector (from cfg.Algorithm) → build proxy → serve.
  - Files: `cmd/l7LoadBalancer/main.go`, `configs/example.yaml` (finalized)
  - Depends on: S1.T2, S1.T3, S1.T4, S1.T5, S1.T6
  - Acceptance:
    - `main.go` calls `config.Load(*configPath)`; logs a fatal error and exits non-zero on load/validate failure (no silent fallback).
    - Selector chosen by `cfg.Algorithm` via a small factory/switch.
    - `make run` against `configs/example.yaml` starts a proxy that distributes requests across backends per the selected algorithm.
    - Existing SIGINT/SIGTERM graceful-shutdown behavior preserved.
  - Test approach: manual smoke test, documented in the Sprint 1 session log; automated coverage lives in S1.T6/S1.T4/S1.T5 tests.

- [TODO] S1.T8 — Cross-selector regression + health-transition tests
  - Goal: cover selector behavior not already exercised by S1.T4/T5's per-selector tests — specifically dynamic health transitions and interface conformance — so both selectors are proven interchangeable.
  - Files: `internal/balancer/selector_test.go` (new, cross-cutting file)
  - Depends on: S1.T4, S1.T5
  - Acceptance:
    - Compile-time interface assertions: `var _ Selector = (*RoundRobin)(nil)` and `var _ Selector = (*LeastConnections)(nil)`.
    - Test: backend goes unhealthy mid-run → selector stops choosing it (both selectors); backend goes healthy again → resumes being chosen.
    - `go test -cover ./internal/balancer/...` recorded in the Sprint 1 session log (no enforced threshold yet — that's deferred).
  - Test approach: table-driven + a scripted health-toggle sequence per selector.

- [TODO] S1.T9 — docker-compose dummy backends
  - Goal: provide 3 lightweight backend services via docker-compose so the proxy can be exercised end-to-end, per the MILESTONES.md Sprint 1 deliverable.
  - Files: `deployments/docker/docker-compose.yml`, `deployments/docker/dummy-backend/main.go`, `deployments/docker/dummy-backend/Dockerfile`, `deployments/docker/dummy-backend/README.md`, `configs/example.yaml` (or `configs/docker.yaml`) pointing at them
  - Depends on: S1.T2
  - Acceptance:
    - `docker-compose.yml` defines 3 services, each on a distinct port, each responding to `GET /` with a body identifying itself (e.g. `{"backend":"backend-a"}`) so distribution is observable via curl.
    - `docker compose -f deployments/docker/docker-compose.yml up -d` brings up all 3 healthy.
    - Config file lists all 3 addresses.
    - Running the proxy against that config and issuing N requests hits all 3 backends (verified by grepping response bodies).
    - Dummy backends honor two env vars: `SLEEP_MS` (int, artificial latency per request in milliseconds, default 0) and `FAIL_RATE` (float 0.0-1.0, fraction of requests returning HTTP 500, default 0). Documented in `deployments/docker/dummy-backend/README.md`.
  - Test approach: no Go unit tests; a documented manual/scripted smoke test, recorded in the Sprint 1 session log.

- [TODO] S1.T10 — Sprint 1 retro / architecture doc
  - Goal: document the as-built architecture so future sprints and future agents have a reference, and close out Sprint 1.
  - Files: `docs/architecture.md` (replace the placeholder Component map section); a new ADR if any deviation from plan occurred.
  - Depends on: S1.T1 through S1.T9 all [DONE]
  - Acceptance:
    - `docs/architecture.md` describes the request path: main → config → registry → selector → proxy → backend (text or Mermaid diagram).
    - Any deviations from MILESTONES.md Sprint 1 deliverables during implementation are explicitly called out.
    - Any undocumented non-trivial design decision gets a new ADR, cross-linked from architecture.md.
  - Test approach: none (docs-only); reviewed against MILESTONES.md Sprint 1 exit criteria.

## Sprint 2 — Advanced Algorithms

Tasks will be added when Sprint 1 completes and Sprint 2 is scoped in detail. See `MILESTONES.md` for the sprint goal.

## Sprint 3, 4, 5

See `MILESTONES.md`. Tasks added per sprint.

## Session log

- 2026-08-31 — bootstrap agent — repository scaffold created per BOOTSTRAP PROMPT. All Phase 7 verification checks passed.
- 2026-09-01 — claude — S1.T0.5 Interface & Schema Freeze (Phase A): froze all Sprint 1 cross-package contracts as compiling Go stubs, wrote `docs/design/sprint-1-contracts.md` and ADR-0002. See `docs/sessions/2026-09-01-claude.md`.
