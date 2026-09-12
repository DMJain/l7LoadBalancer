# AGENTS.md

Canonical guidance for any coding agent working on this repository (Claude Code, Antigravity IDE, OpenCode CLI, or others). `CLAUDE.md` and `GEMINI.md` are symlinks to this file.

## Project

**l7LoadBalancer** — a Layer 7 HTTP load balancer in Go. Portfolio project. Target: senior-backend interviews at top-tier companies. Owner: Darshan Jain.

## Where to look first

1. `PROGRESS.md` — current sprint, in-progress and next tasks, session-by-session activity log.
2. `MILESTONES.md` — sprints, goals, deliverables, exit criteria.
3. `docs/architecture.md` — architecture doc (fills in as sprints complete).
4. `docs/adr/` — architecture decision records. Read them before proposing anything that contradicts one.
5. `docs/sessions/` — per-session logs from previous agent sessions.

## MANDATORY TASK PROTOCOL

Every task, without exception, follows this order:

1. Read `AGENTS.md`, `PROGRESS.md`, `MILESTONES.md`.
2. If any task is `[IN_PROGRESS]` by a different agent or session, STOP and ask the user.
3. Update `PROGRESS.md`: mark task `[IN_PROGRESS]` with agent name, ISO timestamp, one-line description. Commit alone as `chore(progress): start <task>`.
4. Do the work. Keep changes focused.
5. Update `PROGRESS.md`: mark task `[DONE]` with completion timestamp.
6. Commit code + PROGRESS.md update together as ONE atomic commit.
7. Append to `docs/sessions/<YYYY-MM-DD>-<agent>.md`.
8. If a design decision was made, write an ADR.

## Architecture summary (fills in over time)

Currently: scaffold only. First real code lands in Sprint 1 (see `MILESTONES.md`). The design layers a load-balancing policy over `net/http/httputil.ReverseProxy`. Selection strategy is pluggable via a `BackendSelector` interface. Backend health, circuit breaking, and metrics wrap the request path.

## Directory map

- `cmd/l7LoadBalancer/` — binary entry point.
- `internal/proxy/` — the reverse-proxy handler, request path wiring.
- `internal/backend/` — backend struct, registry, state management.
- `internal/balancer/` — `BackendSelector` interface + implementations (roundrobin, leastconn, consistent-hash-bounded, p2c-ewma).
- `internal/health/` — active health checks + passive outlier detection.
- `internal/circuit/` — per-backend circuit breaker state machine.
- `internal/config/` — YAML loading, validation, hot-reload (atomic pointer swap).
- `internal/metrics/` — Prometheus registration and instruments.
- `internal/logger/` — `log/slog` setup.
- `configs/` — example configuration files.
- `deployments/docker/` — dockerfiles, docker-compose for dummy backends and Nginx comparison.
- `bench/` — wrk / vegeta benchmark scripts and results.
- `docs/` — architecture, ADRs, session logs.
- `scripts/` — helper scripts.

## Commands

All commands via `make`. Run `make help` to list them. Common:

- `make build` — build the binary into `bin/l7LoadBalancer`.
- `make run` — build and run with `configs/example.yaml`.
- `make test` — run all tests.
- `make test-race` — run tests with `-race`.
- `make bench` — run Go benchmarks.
- `make fmt` — `gofmt` and `goimports`.
- `make tidy` — `go mod tidy`.
- `make clean` — remove build artifacts.

## Code conventions

- **Language**: Go 1.22+.
- **Style**: `gofmt` + `goimports`. No exceptions.
- **Packages**: lowercase, single word where possible. No `util`, no `common`.
- **Errors**: wrap with `fmt.Errorf("context: %w", err)`. No sentinel `errors.New` at call sites for dynamic messages.
- **Contexts**: every request-scoped function takes `context.Context` as first arg.
- **Interfaces**: define at the consumer, not the producer. Small interfaces preferred.
- **Concurrency**: prefer channels for coordination, atomics for counters, mutex for state. Document the concurrency model at the top of every file with shared state.
- **Logging**: `log/slog` only. Structured key-value. No `fmt.Println` outside of tests.
- **Testing**: table-driven where the input space is enumerable. `testify/require` for setup assertions, `testify/assert` for value checks. `httptest.NewServer` for integration.

## Git commit convention

Conventional Commits:
- `feat:` new capability
- `fix:` bug fix
- `refactor:` internal restructuring, no behavior change
- `test:` tests only
- `docs:` docs only
- `chore:` scaffold, tooling, deps
- `bench:` benchmark work
- `perf:` performance improvement

Commit messages: imperative mood, lowercase after prefix, no trailing period. Reference sprint/task from `PROGRESS.md` when relevant.

## Explicitly deferred until working prototype exists

Per the user's discipline: do NOT set up any of these until Sprint 3 or later, and only if the user asks:

- Pre-commit hooks
- CI (GitHub Actions, etc.)
- MCP servers
- Linters beyond `gofmt` / `goimports` (e.g. no `golangci-lint` config yet)
- Dependabot / Renovate

## What NOT to do without asking

- Do not change `go.mod` module path.
- Do not add heavyweight dependencies (frameworks, ORMs, config libraries like Viper).
- Do not restructure `internal/` package boundaries.
- Do not add features not in `MILESTONES.md`. Propose them to the user first; if accepted, add to `MILESTONES.md` before coding.
- Do not skip tests to move faster.
- Do not commit if `make test-race` fails.

## Agent skills

### Issue tracker

Local markdown under `.scratch/<feature-slug>/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five canonical role names (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context (`CONTEXT.md` + `docs/adr/` at repo root). See `docs/agents/domain.md`.

### Cross-tool skill availability

Skills are copied into a gitignored, project-level `.agents/skills/` (in addition to the home-level `~/.agents/skills/`) so Antigravity IDE and OpenCode CLI can use them too. If a skill isn't registered by the tool you're using, its slash form won't actually run it. See `docs/agents/skills.md`.
