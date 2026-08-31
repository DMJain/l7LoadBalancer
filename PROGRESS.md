# PROGRESS

Live state of the project. Every agent updates this file per the protocol in `AGENTS.md`.

## Current status

**No task in progress.** Bootstrap complete. Next: begin Sprint 1, Task S1.T1.

## Sprint 1 — Foundation

- [DONE] S1.T0 — Repository scaffold (bootstrap agent, 2026-08-31T00:00:00Z, commit: 25c1952)
- [TODO] S1.T1 — Add Go dependencies: `gopkg.in/yaml.v3`, `github.com/stretchr/testify`. Run `go mod tidy`.
- [TODO] S1.T2 — Implement `internal/config`: YAML struct, loader, strict validation.
- [TODO] S1.T3 — Implement `internal/backend`: `Backend` struct, `Registry` with atomic state.
- [TODO] S1.T4 — Implement `internal/balancer`: `Selector` interface + `RoundRobin`.
- [TODO] S1.T5 — Implement `internal/balancer`: `LeastConnections`.
- [TODO] S1.T6 — Implement `internal/proxy`: `httputil.ReverseProxy` wrapper with `Director` calling `Selector`.
- [TODO] S1.T7 — Wire `cmd/l7LoadBalancer/main.go` end-to-end. `make run` distributes across backends.
- [TODO] S1.T8 — Table-driven tests for RoundRobin and LeastConnections.
- [TODO] S1.T9 — docker-compose in `deployments/docker/` with 3 dummy backends (small Go echo servers or nginx serving static).
- [TODO] S1.T10 — Sprint 1 retro: update `docs/architecture.md` with Sprint 1 diagram + decisions.

## Sprint 2 — Advanced Algorithms

Tasks will be added when Sprint 1 completes and Sprint 2 is scoped in detail. See `MILESTONES.md` for the sprint goal.

## Sprint 3, 4, 5

See `MILESTONES.md`. Tasks added per sprint.

## Session log

- 2026-08-31 — bootstrap agent — repository scaffold created per BOOTSTRAP PROMPT. All Phase 7 verification checks passed.
