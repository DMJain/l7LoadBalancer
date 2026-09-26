# AGENTS.md

Canonical guidance for any coding agent working on this repository (Claude Code, Antigravity IDE, OpenCode CLI, or others). `CLAUDE.md` and `GEMINI.md` are symlinks to this file.

**Read this file in full before touching any code.** Then read `PROGRESS.md`, `MILESTONES.md`, and `docs/adr/INDEX.md`.

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
5. `docs/adr/INDEX.md` — architecture decision records. **Read INDEX.md; open full ADRs on demand when your task touches that subsystem** before proposing anything that contradicts one.
6. `docs/sessions/` — per-session logs from previous agent sessions.

---

## MANDATORY TASK PROTOCOL — STRICT TDD

Every task, without exception, follows this order. **TDD is not optional.** Tests are written before implementation code. No exceptions, no shortcuts.

### Step 0: Orient

1. Read `AGENTS.md`, `PROGRESS.md`, `MILESTONES.md`.
2. Read `docs/design/sprint-1-contracts.md`. For Sprint 4, also read the spec at `.scratch/s4-t0-t4-reload/spec.md`.
3. Read `docs/adr/INDEX.md`. Open the full ADR only when the task touches that subsystem.
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

### Step 2.5: Scope boundary check (mandatory, no exceptions)

Before writing a single test or line of implementation, write down — in the ADR, the commit message, or a scratch note — two lists for the task you claimed in Step 1:

- **In scope**: the acceptance criteria in the task's issue file (linked from `PROGRESS.md`), or `PROGRESS.md` if no issue file exists — for *this* task ID, nothing else.
- **Out of scope**: anything adjacent that a grilling/spec/design session surfaced but that belongs to a *different* task ID, a later sprint, or `MILESTONES.md` items not yet approved.

If something in "out of scope" is tempting to build inline because it's convenient while you're already in that file — don't. Add it as a proposed ticket in `PROGRESS.md`/`MILESTONES.md` for the user to approve, and stop there. This applies especially right after a grilling or spec-design session, which routinely surfaces a whole backlog of future tickets in one sitting — that backlog is a plan, not a to-do list to execute unattended.

**Precedent**: S3.T3.5 (a circuit-state accessor) was implemented during an exploratory design session and had to be reverted because it was never an approved, claimed task. Treat that as the canonical example of what this step exists to prevent.

If you catch yourself about to implement something you did not name in "in scope" above, stop and ask the user before writing the code, even mid-task.

### Step 3: Write tests FIRST (Red phase)

9. Write the test file(s) for this task **before** any implementation code.
   - Table-driven tests where the input space is enumerable.
   - `testify/require` for setup assertions, `testify/assert` for value checks.
   - `httptest.NewServer` for integration tests.
   - Concurrency tests using goroutines + `-race` flag.
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

Non-test import edges (verified against the code):

```
cmd/l7LoadBalancer → app, config, logger
internal/app       → proxy, health, circuit, metrics, balancer, backend, config, logger
internal/proxy     → backend, balancer, logger, metrics
internal/balancer  → backend, config
internal/health    → backend, logger, metrics
internal/circuit   → backend, logger, metrics
internal/backend   → config
internal/logger    — leaf, no internal deps
internal/metrics   — leaf, no internal deps
```

**Rule**: `internal/backend` does NOT import `internal/balancer`. A `Backend` has no notion of how it's selected. If a future task is tempted to add that import, it's a sign the abstraction is leaking.

See `MILESTONES.md` for Sprint 4–5 goals and deliverables. See `docs/adr/INDEX.md` for the full decision index.


## Directory map

- `cmd/l7LoadBalancer/` — binary entry point: flags, file I/O, signals.
- `internal/app/` — the wiring graph behind `Build`/`Run`; the one place production, the chaos harness, and tests assemble the system (Sprint 4).
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
- **Errors**: wrap with `fmt.Errorf("context: %w", err)`. No sentinel `errors.New` at call sites for dynamic messages. The only branchable exported sentinel is `balancer.ErrNoHealthyBackends`; the one further exported value is `backend.ErrDrainWindowExpired`, a `context.Cause` comparison value, not a branchable condition (ADR-0016 decision 2).
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

For every implementation task:

```
1. Write _test.go           ← RED: tests compile, tests FAIL
2. Run: go test ./...       ← Confirm failure (panic or assertion)
3. Write implementation     ← GREEN: minimum code to pass
4. Run: make test-race      ← Confirm pass + no races
5. Refactor                 ← REFACTOR: clean up
6. Run: make test-race      ← Confirm still passing
```

**What "minimum code to pass" means**: Don't gold-plate. Write the simplest correct implementation. Optimize only when benchmarks (Sprint 5) show a need. The refactor phase is for readability and structure, not premature optimization.

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

Per the user's discipline: do NOT set up any of these until Sprint 5 or later, and only if the user asks:

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
- Do not add features not in `MILESTONES.md`, and do not implement work belonging to a future ticket or sprint while working on the current one — even if a grilling/spec session just surfaced it and it seems convenient to build inline. Propose it to the user first; if accepted, add it to `PROGRESS.md`/`MILESTONES.md` as its own ticket before coding. See Step 2.5 above.
- **Do not skip tests to move faster.** Tests are written first. Always.
- Do not commit if `make test-race` fails.
- Do not deviate from frozen contracts without writing an ADR first.
- Do not silently change function signatures, struct fields, or error types that are documented in `docs/design/sprint-1-contracts.md`.


## Agent skills

### Issue tracker

Local markdown under `.scratch/<feature-slug>/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five canonical role names (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context (`CONTEXT.md` + `docs/adr/` at repo root). See `docs/agents/domain.md`.

### Cross-tool skill availability

Skills are copied into a gitignored, project-level `.agents/skills/` (in addition to the home-level `~/.agents/skills/`) so Antigravity IDE and OpenCode CLI can use them too. If a skill isn't registered by the tool you're using, its slash form won't actually run it. See `docs/agents/skills.md`.

### LSP (OpenCode)

LSP is off by default in OpenCode. A tracked `opencode.jsonc` at the repo root sets `"lsp": true`, giving OpenCode agents real gopls diagnostics for `.go` files instead of grep-only navigation. See `docs/agents/lsp.md`.
