# 01: Add Go dependencies

**What to build:** Add the two third-party dependencies Sprint 1 needs — `gopkg.in/yaml.v3` for YAML parsing and `github.com/stretchr/testify` for test assertions — so that downstream tasks can import them without dependency wrangling. This is a tooling-level change: `go.mod` and `go.sum` are the only files touched. The existing build and vet checks continue to pass.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [x] `go.mod` requires `gopkg.in/yaml.v3` and `github.com/stretchr/testify`
- [x] `go.sum` generated and committed
- [x] `go mod tidy` produces no further diff after this task
- [x] `go build ./...` passes
- [x] `go vet ./...` passes
- [ ] No files other than `go.mod` and `go.sum` are modified
      — `tools.go` added per ADR-0003; `AGENTS.md`/`PROGRESS.md`/session log
      changes are protocol-mandated by `AGENTS.md` Steps 16/22.
- [x] `PROGRESS.md` updated: S1.T1 marked `[DONE]` with completion timestamp
- [x] Committed as `chore(deps): add yaml.v3 and testify`

## Comments

**2026-09-17 — opencode — resolved via ADR-0003.** The criteria
"`go.mod` requires both deps" and "`go mod tidy` produces no further diff"
cannot both hold while the deps are unused: `go mod tidy` prunes modules no
package imports (verified on Go 1.25.1). Resolved by adding a build-tagged
`tools.go` with blank imports, so tidy retains them. This touches one file
beyond `go.mod`/`go.sum`; recorded in
`docs/adr/0003-pin-unused-deps-with-tools-go.md`. `tools.go` is deleted in
S1.T2 once the real imports land. Verified: `go build ./...`,
`go vet ./...`, `gofmt -l .`, `go mod tidy` all clean.
