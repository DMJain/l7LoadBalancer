# PROGRESS

Live state of the project. Every agent updates this file per the protocol in `AGENTS.md`.

## Current status

**Sprint 1 complete.** S1.T0 through S1.T10 (including S1.T0.5) are all [DONE]. Sprint 2 (Advanced Algorithms) is next; its tasks will be scoped when it starts.

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

- [DONE] S1.T2 — Implement `internal/config` (opencode, started 2026-09-17T22:12:47Z, completed 2026-09-17T22:14:08Z)
  - Goal: load and strictly validate the YAML config (listen address + backend list + algorithm choice) into a typed struct, as the canonical config source for the registry and proxy.
  - Files: `internal/config/config.go`, `internal/config/config_load_test.go`, `internal/config/config_validate_test.go`, `configs/example.yaml` (updated to a realistic 3-backend example)
  - Depends on: S1.T1
  - Acceptance:
    - `Config` struct: `Listen string`; `Backends []BackendConfig` (`Name`, `URL` string fields at minimum); `Algorithm string`, defaulting to `"round_robin"` when empty.
    - `Load(path string) (*Config, error)` reads + unmarshals YAML.
    - Unknown YAML fields cause `Load` to return an error. Use `yaml.NewDecoder(f).KnownFields(true)`; do not use `yaml.Unmarshal` directly.
    - `Validate() error` rejects: empty `Listen`, zero backends, a backend whose `URL` fails `url.Parse` or has no host, duplicate backend names, unrecognized `Algorithm` value.
    - Errors wrapped `fmt.Errorf("config: ...: %w", err)` per AGENTS.md.
    - `configs/example.yaml` updated to a valid 3-backend config that passes `Validate()`.
  - Test approach: table-driven tests covering valid config, missing listen, empty backends, malformed URL, duplicate names, unknown algorithm, unknown field. testify/require for setup, testify/assert for values.

- [DONE] S1.T2-fix — Address S1.T2 code-review audit findings (opencode, started 2026-09-17T22:29:55Z, completed 2026-09-17T22:31:12Z)
  - Goal: close out the two-axis audit of S1.T2 — reject uppercase URL schemes (real bug), add an `example.yaml` round-trip test, record ADR-0004 for the frozen-comment/implemented-set deviation, align tracking files, and apply small cleanups.
  - Files: `internal/config/config.go`, `internal/config/config_validate_test.go`, `internal/config/config_load_test.go`, `docs/adr/0004-reject-unimplemented-algorithms-in-validate.md`, `AGENTS.md`, `PROGRESS.md`, `.scratch/s1-t1-t2-deps-and-config/issues/02-implement-config-package.md`, `docs/sessions/2026-09-18-opencode.md`
  - Depends on: S1.T2
  - Acceptance: uppercase `HTTP://`/`HTTPS://` backend URLs rejected by `Validate` with a scheme error; `configs/example.yaml` round-trips through `Load`+`Validate` in a test; ADR-0004 written and indexed in AGENTS.md; S1.T2 Files bullet corrected; issue 02 acceptance boxes ticked; session log commit list completed and audit summary appended; `b` loop var renamed; no `.scratch/`-pointing comments in `config.go`; `make test`, `make test-race`, `go vet`, `make fmt`, `go mod tidy` clean.
  - Test approach: two new rejection cases in the `TestValidate` table; one new `TestExampleConfig` integration test.

- [DONE] S1.T2.6 — ADR-0005: Scope of "production-grade" (claude, started 2026-09-18T07:50:00Z, completed 2026-09-18T07:55:00Z)
  - Goal: write down, in one canonical place, what this project's "production-grade" framing (AGENTS.md) does and does not claim, before Sprint 1 implementation and the eventual README/Sprint 5 design-decisions doc build further on an undefined term.
  - Files: `docs/adr/0005-scope-of-production-grade.md`
  - Depends on: none (docs-only, no code dependency)
  - Acceptance: ADR states what "production-grade" means (demonstrates production L7 LB patterns, every non-trivial decision defensible, honest reproducible Nginx benchmarking) and what it explicitly excludes (adversarial-traffic hardening/WAF, TLS cert rotation, kernel/OS tuning, SLO instrumentation/alerting, formal security review, multi-tenancy, secrets management beyond env-var interpolation, disaster recovery, capacity planning/SLA, multi-region validation, and a settled deployment target — deployment target explicitly deferred to Sprint 4/5). Decided and dated by the project owner; no invented prior provenance.
  - Test approach: none — docs-only (AGENTS.md TDD exception).

- [DONE] S1.T3 — Implement `internal/backend` (opencode, started 2026-09-18T09:29:18Z, completed 2026-09-18T09:30:24Z)
  - Goal: define `Backend` and a concurrency-safe `Registry` tracking identity, health, and active-connection count, since balancer and proxy both read/mutate this under concurrent requests.
  - Files: `internal/backend/backend.go`, `internal/backend/registry.go`, `internal/backend/backend_test.go` (replaces the placeholder `TestScaffold`), `internal/backend/registry_test.go`
  - Depends on: S1.T2
  - Acceptance:
    - `Backend`: exported `Name string` and `URL *url.URL`; unexported `healthy` (`atomic.Bool`) and `active` (`atomic.Int64`) fields, reachable only via methods `IsHealthy()`, `SetHealthy(bool)`, `IncActive()`, `DecActive()`, `ActiveConns() int64`. `SetHealthy` is added per ADR-0006 (amends ADR-0002 decision 5) — needed now so S1.T8 can drive health transitions before Sprint 3's health checker exists; Sprint 3 reuses it unchanged.
    - `Backend` exposes an `IsHealthy() bool` method; downstream code (balancer, proxy) accesses health only via this method, never via the `healthy` field directly. This isolates the field type from callers so Sprint 3 can replace `atomic.Bool` with a state enum without touching balancer or proxy code.
    - `NewRegistry(cfgs []config.BackendConfig) (*Registry, error)` builds backends from validated config; every backend starts `healthy = true` (commented as such — there is no health checker yet to set it any other way, and Sprint 1's exit criteria requires all 3 backends reachable from the start).
    - `Registry` holds an ordered slice only (no name-indexed map — nothing through Sprint 3 needs O(1) lookup by name; add it in Sprint 4 when reload-diffing needs it). `Registry.All()` and `Registry.Healthy()` each return a fresh slice per call, preserving that order (required for `LeastConnections`' deterministic tie-break) — safe to iterate concurrently with registry mutation, no lock held by caller.
    - Concurrent `ActiveConns` increment/decrement and health toggling from multiple goroutines is race-free under `go test -race`.
    - Placeholder `TestScaffold` removed, replaced with real tests.
  - Test approach: table-driven tests for construction/filtering; a concurrent test with N goroutines mutating `ActiveConns`, asserting the final count under `-race`.

- [DONE] S1.T4 — Implement RoundRobin selector (opencode, started 2026-09-18T09:38:54Z, completed 2026-09-18T09:40:23Z)
  - Goal: implement round-robin backend selection behind a Selector interface.
  - Files: `internal/balancer/selector.go` (Selector interface + `ErrNoHealthyBackends`), `internal/balancer/roundrobin.go`, `internal/balancer/roundrobin_test.go`
  - Depends on: S1.T3
  - Acceptance:
    - `Selector` interface: `Select(ctx, r *http.Request) (*backend.Backend, error)`.
    - `RoundRobin` selects only from `Registry.Healthy()`, uses an atomic counter (no mutex) for rotation, safe under concurrent access.
    - Returns `ErrNoHealthyBackends` when the healthy set is empty.
  - Test approach: table-driven cyclic-order test over 3 backends; empty-registry error case; concurrent test firing 1000 selects, each backend chosen within ±5% of the expected 1/3 share.

- [DONE] S1.T5 — Implement LeastConnections selector (opencode, started 2026-09-18T09:47:42Z, completed 2026-09-18T09:48:15Z)
  - Goal: pick the healthy backend with the lowest current ActiveConns.
  - Files: `internal/balancer/leastconn.go`, `internal/balancer/leastconn_test.go`
  - Depends on: S1.T4
  - Acceptance:
    - `LeastConnections` implements `Selector`.
    - Ties broken deterministically (first tied backend in registry order) so tests are reproducible.
    - Returns `ErrNoHealthyBackends` when no healthy backends.
    - Reads `ActiveConns` atomically; does not itself mutate it (mutation is the proxy's job, wired in S1.T6).
  - Test approach: table-driven test with pre-seeded `ActiveConns` values asserting the minimum is chosen; tie-break case; empty-registry case.

- [DONE] S1.T6 — Implement `internal/proxy` (opencode, started 2026-09-18T16:55:16Z, completed 2026-09-18T16:58:11Z)
  - Goal: wrap httputil.ReverseProxy so each request's Director consults the configured Selector, rewrites the target, and tracks ActiveConns around the round trip.
  - Files: `internal/proxy/proxy.go`, `internal/proxy/proxy_test.go`, `docs/adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md`, `internal/config/config.go` (stale path-prefix comment corrected), `AGENTS.md` (ADR-0007 indexed)
  - Depends on: S1.T3, S1.T4, S1.T5
  - Acceptance:
    - `New(registry *backend.Registry, selector balancer.Selector) http.Handler` wraps `ReverseProxy`.
    - `ServeHTTP` calls `selector.Select` itself — not `Director` — and short-circuits a 503 immediately when it returns `ErrNoHealthyBackends`, bypassing `ReverseProxy` entirely for that path (`Director`'s `func(*http.Request)` signature has no way to write a response). On success, `ServeHTTP` calls `IncActive()` and attaches the chosen `*backend.Backend` to the request context via an unexported context-key type; `Director` reads it back from context and only sets `req.URL.Scheme/Host` — it does not call `Select`. `ModifyResponse` and `ErrorHandler` read the same backend off `resp.Request.Context()` / `req.Context()` to drive `DecActive`.
    - No healthy backend → responds 503, no panic, no hang.
    - `ActiveConns` incremented before dispatch, decremented after response completes (success, error, or timeout) — no leak across many sequential requests.
    - `ActiveConns` decrement uses a response-body wrapper whose `Close()` decrements exactly once. Wrap the body in `ModifyResponse`. Do NOT decrement in `Director`, in `ModifyResponse` directly, or only in `ErrorHandler`. Test with 100 concurrent (not sequential) in-flight requests and assert `ActiveConns` returns to 0 within a small drain window.
    - Every request emits one "request complete" `slog` line using the canonical fields (`backend`, `method`, `status`, `latency_ms`, `remote_addr`, `path`) per `docs/design/sprint-1-contracts.md` — on the success path, the 503 short-circuit path, and the `ErrorHandler` path alike. No separate "request start" line in Sprint 1.
  - Test approach: httptest.NewServer fake backends returning an identifying body; httptest-wrapped proxy in front of them; assert distribution matches the selector; no-healthy-backend → 503; concurrent-request test asserting ActiveConns returns to 0.

- [DONE] S1.T7 — Wire `cmd/l7LoadBalancer/main.go` end-to-end (opencode, started 2026-09-18T18:13:48Z, completed 2026-09-18T18:17:45Z)
  - Goal: replace the placeholder 501 handler with the real path: load config → build registry → build selector (from cfg.Algorithm) → build proxy → serve.
  - Files: `cmd/l7LoadBalancer/main.go`, `internal/balancer/selector.go` (`NewFromConfig` implemented), `internal/balancer/factory_test.go` (new), `configs/example.yaml` (unchanged — already valid)
  - Depends on: S1.T2, S1.T3, S1.T4, S1.T5, S1.T6
  - Acceptance:
    - `main.go` calls `config.Load(*configPath)`; logs a fatal error and exits non-zero on load/validate failure (no silent fallback).
    - Selector chosen by `cfg.Algorithm` via a small factory/switch.
    - `make run` against `configs/example.yaml` starts a proxy that distributes requests across backends per the selected algorithm.
    - Existing SIGINT/SIGTERM graceful-shutdown behavior preserved.
  - Test approach: manual smoke test, documented in the Sprint 1 session log; automated coverage lives in S1.T6/S1.T4/S1.T5 tests plus the new `NewFromConfig` factory tests.
  - Deviation (documented in session log, no ADR needed): the scaffold's `-addr` flag was removed — `listen` is part of the frozen YAML schema and `config.Validate` checks it, so `cfg.Listen` is the single source of truth. The factory is implemented in `balancer` per ADR-0002, and it rejects unknown algorithms (including the empty string) rather than defaulting.

- [DONE] S1.T8 — Cross-selector regression + health-transition tests (opencode, started 2026-09-18T17:55:14Z, completed 2026-09-18T17:56:20Z)
  - Goal: cover selector behavior not already exercised by S1.T4/T5's per-selector tests — specifically dynamic health transitions and interface conformance — so both selectors are proven interchangeable.
  - Files: `internal/balancer/selector_test.go` (new, cross-cutting file)
  - Depends on: S1.T4, S1.T5
  - Acceptance:
    - Compile-time interface assertions: `var _ Selector = (*RoundRobin)(nil)` and `var _ Selector = (*LeastConnections)(nil)`.
    - Test: backend goes unhealthy mid-run → selector stops choosing it (both selectors); backend goes healthy again → resumes being chosen.
    - `go test -cover ./internal/balancer/...` recorded in the Sprint 1 session log (no enforced threshold yet — that's deferred).
  - Test approach: table-driven + a scripted health-toggle sequence per selector.

- [DONE] S1.T9 — docker-compose dummy backends (opencode, started 2026-09-18T09:53:23Z, completed 2026-09-18T09:55:57Z)
  - Goal: provide 3 lightweight backend services via docker-compose so the proxy can be exercised end-to-end, per the MILESTONES.md Sprint 1 deliverable.
  - Files: `deployments/docker/docker-compose.yml`, `deployments/docker/dummy-backend/main.go`, `deployments/docker/dummy-backend/Dockerfile`, `deployments/docker/dummy-backend/README.md`
  - Depends on: S1.T2
  - Acceptance:
    - `docker-compose.yml` defines 3 services only (no LB service — that's run locally via `make run`; Sprint 5 has its own separate "LB + Nginx + 4 backends" bench compose, kept distinct so the two don't drift against each other), each on a distinct port mapped to host `9001`/`9002`/`9003` so the existing `configs/example.yaml` works against them unmodified — no new config file.
    - Each service responds to `GET /` with a body identifying itself (e.g. `{"backend":"backend-a"}`) so distribution is observable via curl.
    - `docker compose -f deployments/docker/docker-compose.yml up -d` brings up all 3 healthy.
    - Running the proxy against `configs/example.yaml` and issuing N requests hits all 3 backends (verified by grepping response bodies).
    - Dummy backends honor two env vars: `SLEEP_MS` (int, artificial latency per request in milliseconds, default 0) and `FAIL_RATE` (float 0.0-1.0, fraction of requests returning HTTP 500, default 0). Documented in `deployments/docker/dummy-backend/README.md`. `docker-compose.yml` gives each of the 3 services distinct non-zero defaults (not all zero) so `docker compose up` demonstrates uneven latency/failure — and therefore visible `LeastConnections` vs. `RoundRobin` differences — without a manual override.
  - Test approach: no Go unit tests; a documented manual/scripted smoke test, recorded in the Sprint 1 session log.

- [DONE] S1.T10 — Sprint 1 retro / architecture doc (claude, started 2026-09-18T19:53:55Z, completed 2026-09-18T19:54:39Z)
  - Goal: document the as-built architecture so future sprints and future agents have a reference, and close out Sprint 1.
  - Files: `docs/architecture.md` (replace the placeholder Component map section); a new ADR if any deviation from plan occurred.
  - Depends on: S1.T1 through S1.T9 all [DONE]
  - Acceptance:
    - `docs/architecture.md` describes the request path: main → config → registry → selector → proxy → backend (text or Mermaid diagram).
    - Any deviations from MILESTONES.md Sprint 1 deliverables during implementation are explicitly called out.
    - Any undocumented non-trivial design decision gets a new ADR, cross-linked from architecture.md.
  - Test approach: none (docs-only); reviewed against MILESTONES.md Sprint 1 exit criteria.

## Sprint 2 — Advanced Algorithms

Scoped in `.scratch/s2-t1-t2-consistent-hash-bounded-loads/` as one spec plus three implementation tickets. The spec groups the work as S2.T1 (ring primitive + unexported naive comparator) and S2.T2 (bounded-loads selector + config wiring), but PROGRESS tracks the finer-grained tickets so each closes independently: issue 01 = ring primitive, issue 02 = `naiveConsistentHash`, issue 03 = `ConsistentHashBoundedLoads`.

- [DONE] S2.T1.1 — Consistent-hash ring primitive (issue 01) (opencode, started 2026-09-19T06:18:15Z, completed 2026-09-19T06:20:58Z, ring with fnv→fmix64 hashing, 150 vnodes/backend, index-first vnode keys, iter.Seq candidate walk; ADR-0008)
  - Goal: an unexported, deterministic hash ring in `internal/balancer` that maps a hash key to a backend via virtual nodes, exposing an ordered candidate walk as a Go 1.23 `iter.Seq[*backend.Backend]` — the shared foundation both consistent-hash selectors (issues 02 and 03) build on. Placement only: no `Select`, no health or capacity awareness.
  - Files: `internal/balancer/ring.go`, `internal/balancer/ring_test.go`, `docs/adr/0008-consistent-hash-ring-pipeline-and-vnode-layout.md`, `AGENTS.md` (ADR indexed), `docs/architecture.md` (ADR indexed + ring noted), `docs/sessions/2026-09-19-opencode.md`
  - Depends on: none
  - Acceptance:
    - Unexported `ring` type, built once at construction from `Registry.All()` (every backend, regardless of health) — no rebuild API; membership changes are Sprint 4's problem.
    - Hash pipeline: stdlib `hash/fnv` `New64a()` → `Sum64()` → fixed Murmur3-style `fmix64` finalizer; same pipeline used for vnode placement and (later tickets) request-key hashing.
    - Virtual-node keys built `<replica-index>:<backend-name>` (index first); 150 vnodes per backend.
    - Candidate walk from a key's ring position wraps around and yields each distinct backend once, as `iter.Seq[*backend.Backend]` (not a predicate callback).
    - Table-driven tests: same key maps to the same backend repeatedly; adding/removing a backend remaps only ~`1/n` of keys; vnode placement reasonably uniform under a large random-key sample.
    - ADR-0008 records the hash pipeline, vnode key order, and vnode count, each citing measured evidence.
  - Test approach: direct ring-level tests (no selector, no HTTP), `testify/require` for setup and `assert` for values, deterministic fixtures so assertions cannot flake.

- [DONE] S2.T1.2 — `naiveConsistentHash` selector (issue 02) (opencode, started 2026-09-19T06:58:41Z, completed 2026-09-19T07:45:36Z, unexported health-aware/load-blind Selector + shared requestHashKey + binary DCE guard)
  - Goal: unexported, health-aware but load-blind `Selector` over the ring, walk skipping unhealthy candidates; deliberately never reachable via `NewFromConfig`/`implementedAlgorithms`; exists as the empirical comparator for bounded-loads.
  - Files: `internal/balancer/naive_consistent_hash.go`, `internal/balancer/naive_consistent_hash_test.go`, `internal/balancer/hashkey.go`, `internal/balancer/hashkey_test.go`, `internal/balancer/ring_test.go` (`randomClientIP`→`randomIP` rename, no behavior change), `cmd/l7LoadBalancer/dce_test.go` (new), `PROGRESS.md`, `.scratch/s2-t1-t2-consistent-hash-bounded-loads/issues/02-naive-consistent-hash-selector.md`, `docs/sessions/2026-09-19-opencode.md`
  - Depends on: S2.T1.1
  - Acceptance:
    - Unexported `naiveConsistentHash` satisfies `Selector` (compile-time assertion) and holds only the immutable ring; `Select` returns the first healthy candidate from the client-IP ring walk, or `ErrNoHealthyBackends` when the walk is exhausted (including a zero-backend registry).
    - `requestHashKey(*http.Request)` derives the hash key from `RemoteAddr` with the port stripped — shared with S2.T2 — returning unparseable/empty addresses unchanged, documented as intentionally concentrating upstream bugs on one backend.
    - The type is in neither `NewFromConfig`'s switch nor `config`'s implemented set; its doc comment states why it is unwired, why it is not named `ConsistentHash`, and points at ADR-0008. No new ADR: ticket 02's decisions are recorded there and in the doc comment, with the bounded-loads evidence reserved for ADR-0009 (S2.T2).
    - `cmd/l7LoadBalancer/dce_test.go` builds the production binary for a pinned linux/amd64 target and asserts the type name is absent, with a `RoundRobin` positive control so the negative assertion cannot be vacuous — turning "the linker drops it" into a checked invariant.
    - Tests: stable affinity per client IP (incl. port-stripped), key-matters teeth check (400 octet-diverse IPs, 4 backends, each ≥10%), health transition (skip → resume), empty healthy set → `ErrNoHealthyBackends`, concurrent `-race` selects.
  - Test approach: direct instantiation against a real `*backend.Registry`, deterministic fixed seeds, table-driven where enumerable, no mocks. `go test -cover ./internal/balancer/...` → 93.9%.

- [DONE] S2.T2 — `ConsistentHashBoundedLoads` + config wiring (issue 03) (opencode, started 2026-09-19T07:53:59Z, completed 2026-09-19T07:57:56Z)
  - Goal: exported `Selector` wired to the `consistent_hash` identifier, reusing the ring with a `(1 + ε)` per-candidate capacity check (ε = 0.25), least-loaded fallback, and a fixed-seed hot-key comparative test plus a build-tagged offline reproducer; ADR-0009 records the bounded-loads decisions and evidence.
  - Files: `internal/balancer/consistent_hash.go` (stub replaced), `internal/balancer/consistent_hash_test.go`, `internal/balancer/hotkey_test.go`, `internal/balancer/hotkey_reproducer_test.go` (`//go:build offline`), `internal/balancer/selector.go` (factory case), `internal/balancer/selector_test.go` (shared `requestForAddr`/`selectAddr` helpers), `internal/balancer/factory_test.go`, `internal/config/config.go` (`implementedAlgorithms`), `internal/config/config_validate_test.go`, `docs/adr/0009-consistent-hash-bounded-loads-capacity-and-evidence.md`, `AGENTS.md` (ADR indexed + as-built bounded-loads description), `MILESTONES.md` (deliverable wording), `docs/architecture.md` (ADR indexed + Sprint 2 selection note), `PROGRESS.md`, `.scratch/s2-t1-t2-consistent-hash-bounded-loads/issues/03-consistent-hash-bounded-loads.md`, `docs/sessions/2026-09-19-opencode.md`
  - Acceptance: exported `ConsistentHashBoundedLoads` implements `Selector` (compile-time assertion) and is wired to `consistent_hash` in both `config.implementedAlgorithms` and `NewFromConfig`; same ring/iterator/hash key as issue 02; per-candidate skip is `!IsHealthy() || ActiveConns() > capacity`; capacity is `max(1, ceil(avg_active_over_healthy * 1.25))` with `<=` admission; the walk is the ring's one-pass iterator and the least-loaded-seen fallback is defensive only; `ErrNoHealthyBackends` is the sole error condition; ε/vnode count/key source stay Go constants. Tests: over-capacity skip, `<=` boundary (2 admitted at cap 2, 3 skipped at cap 2), idle-system first request, stable affinity, health transition, empty healthy set, no-mutation, concurrent `-race` select, key-matters teeth check. Fixed-seed (2253) hot-key test: naive busiest 4,005 / bounded 3,126 of 10,000, asserted in [3,800,4,200] / [3,100,3,130]. `offline`-tagged reproducer sweeps 60 seeds: naive busiest min 29.9% / p10 34.0% / median 40.0% / p90 47.8% / max 51.3% / mean 40.4%, bounded 30.0–31.3%; capacity check found an admissible candidate at every one of 600,000 selections — fallback never needed. ADR-0009 records epsilon, load metric, capacity formula, probing/fallback, and the evidence.
  - Test approach: direct instantiation against real `*backend.Registry` fixtures, deterministic fixed seeds, table-driven where enumerable, no mocks; `httptest.NewRequest` with a controlled `RemoteAddr`. `go test -cover ./internal/balancer/...` → 94.3%.
  - Depends on: S2.T1.1, S2.T1.2

## Sprint 3, 4, 5

See `MILESTONES.md`. Tasks added per sprint.

## Session log

- 2026-08-31 — bootstrap agent — repository scaffold created per BOOTSTRAP PROMPT. All Phase 7 verification checks passed.
- 2026-09-01 — claude — S1.T0.5 Interface & Schema Freeze (Phase A): froze all Sprint 1 cross-package contracts as compiling Go stubs, wrote `docs/design/sprint-1-contracts.md` and ADR-0002. See `docs/sessions/2026-09-01-claude.md`.
- 2026-09-18 — opencode — S1.T2 Implement `internal/config`: strict YAML load (`KnownFields(true)`), fail-fast `Validate()` with algorithm default and full field checks, table-driven tests (21 cases), `configs/example.yaml` rewritten to the frozen 3-backend example, `tools.go` deleted now that both deps are imported for real. See `docs/sessions/2026-09-18-opencode.md`.
- 2026-09-18 — opencode — S1.T2-fix: addressed the S1.T2 code-review audit — reject uppercase URL schemes, add `TestExampleConfig` round-trip test, record ADR-0004, align tracking files, rename loop var. See `docs/sessions/2026-09-18-opencode.md`.
- 2026-09-18 — opencode — S1.T5 LeastConnections: linear scan of `Registry.Healthy()` for the lowest `ActiveConns()` (read-only), first-in-registry-order tie-break; table-driven min/tie/unhealthy/empty tests plus determinism and no-mutation tests. See `docs/sessions/2026-09-18-opencode.md`.
- 2026-09-18 — opencode — S1.T9 docker-compose dummy backends: stdlib-only Go service with `SLEEP_MS`/`FAIL_RATE` chaos knobs and a `-name` identity flag, three compose services on host ports `9001`/`9002`/`9003` with distinct non-zero defaults; `docker compose up -d --build` smoke-tested (all 3 healthy, identity + latency + failure rate verified). See `docs/sessions/2026-09-18-opencode.md`.
- 2026-09-18 — opencode — S1.T8 cross-selector health-transition tests: new `internal/balancer/selector_test.go` proving both `RoundRobin` and `LeastConnections` stop choosing a backend on `SetHealthy(false)` and resume on `SetHealthy(true)`, with a mutation check confirming the test has teeth; `go test -cover ./internal/balancer/...` baseline recorded at 77.3% (100% on every implemented function; the rest is Sprint 2 / S1.T7 stubs). See `docs/sessions/2026-09-18-opencode.md`.
- 2026-09-18 — opencode — S1.T7 main.go wiring: implemented `balancer.NewFromConfig` (config-string → selector switch, unknown/empty rejected) with Red-first tests, and rewired `cmd/l7LoadBalancer/main.go` to `config.Load`/`Validate` → `backend.NewRegistry` → `balancer.NewFromConfig` → `proxy.New` → `http.Server` (fatal + exit 1 on any failure, `logger.New` dedup, SIGINT/SIGTERM preserved, scaffold `-addr` flag removed in favour of `cfg.Listen`). Manual smoke: docker backends + `round_robin` curl cycle `a,b,c,a,b,c,a,b,c`, `least_conn` concurrent spread 3/3/3, backend 500 logged at WARN, bad config exits 1. `go test -cover ./internal/balancer/...` → 84.0%. See `docs/sessions/2026-09-18-opencode.md`.
- 2026-09-19 — claude — S1.T10 Sprint 1 retro / architecture doc: rewrote `docs/architecture.md` (overview, ASCII request path, component map, 7-ADR decision index, two-entry deviations audit, ADR-0006/0007 forward-pointers, "deliberately not here yet" list), added `docs/sessions/2026-09-19-claude.md` as the retro, and closed out Sprint 1. Zero new ADRs — three candidates were evaluated against the three-part bar and none qualified. Corrected a factually wrong citation the issue carried (`sprint-1-contracts.md:115` does not mention Transport). See `docs/sessions/2026-09-19-claude.md`.
- 2026-09-19 — opencode — S2.T1.1 consistent-hash ring primitive (issue 01): unexported placement-only `ring` in `internal/balancer` built once from `Registry.All()` (150 vnodes/backend), FNV-1a-64 → `fmix64` pipeline shared by later request-key hashing, `index:name` vnode keys, and an `iter.Seq[*backend.Backend]` candidate walk wrapping the ring and yielding each backend once. Five ring-level tests (stable mapping, ~1/n minimal disruption, ±10% vnode uniformity, distinct-once walk, empty ring). ADR-0008 records the pipeline/key-order/vnode-count decisions and reproduces the design-session evidence: raw FNV-1a-64 maps a /24 subnet's 256 addresses onto 3 of 4 backends, `name:index` spreads 6.1–36.2% vs `index:name`'s 23.0–26.8% pre-finalizer. See `docs/sessions/2026-09-19-opencode.md`.
- 2026-09-19 — opencode — S2.T2 bounded-loads selector (issue 03): replaced the `ConsistentHashBoundedLoads` stub with the ring-reusing capacity selector (ε = 0.25, load = `ActiveConns()` over healthy, capacity `max(1, ceil(avg × 1.25))`, `<=` admission, one-pass walk, defensive least-loaded fallback, `ErrNoHealthyBackends` sole error) and wired `consistent_hash` into `config.implementedAlgorithms` + `NewFromConfig`. Ten new selector tests; a fixed-seed (2253) hot-key comparative test (naive 4,005 vs bounded 3,126 of 10,000); an `offline`-tagged 60-seed reproducer (naive busiest 29.9–51.3%, bounded 30.0–31.3%, fallback needed 0 of 600,000 selections); ADR-0009 records the decisions and evidence. `go test -cover ./internal/balancer/...` → 94.3%. See `docs/sessions/2026-09-19-opencode.md`.
