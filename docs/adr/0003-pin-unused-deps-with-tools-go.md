# ADR-0003: Pin not-yet-imported dependencies with a build-tagged tools.go

- **Status**: Accepted
- **Date**: 2026-09-17
- **Deciders**: project owner + opencode agent (S1.T1)

## Context

S1.T1 adds `gopkg.in/yaml.v3` and `github.com/stretchr/testify` so that
downstream Sprint 1 tasks can import them without dependency wrangling.
The task's acceptance criteria require both that:

1. `go.mod` requires both modules and `go.sum` is committed, and
2. `go mod tidy` produces no further diff after the task.

These two requirements cannot both hold while the modules are unused,
because `go mod tidy` prunes requirements that no package in the main
module imports. Verified locally against Go 1.25.1:

```
$ go get gopkg.in/yaml.v3 github.com/stretchr/testify   # adds both
$ go mod tidy                                           # removes both
```

The modules become used only in S1.T2 (config package + its tests).

S1.T1's plan text in `spec.md` says "No other files touched", which is
in tension with the `go mod tidy` criterion. `docs/design/sprint-1-contracts.md`
requires that deviations from the frozen plan be recorded in an ADR rather
than made silently.

## Decision

Add a module-root `tools.go` carrying the `//go:build tools` constraint and
blank imports of the two modules:

```go
//go:build tools
package tools

import (
    _ "github.com/stretchr/testify/require"
    _ "gopkg.in/yaml.v3"
)
```

`go mod tidy` loads packages under all build configurations (except
`ignore`), so it sees these imports and retains both requirements as
direct. The `tools` tag is never set for normal builds, so the file is
excluded from `go build ./...`, `go vet ./...`, and the shipped binary.

The file is removed in S1.T2 once `internal/config` and its tests import
the modules for real; at that point the imports are redundant.

## Consequences

- Positive: S1.T1's `go mod tidy` acceptance criterion is actually
  satisfiable, and both modules are pinned at a known version before any
  consumer exists.
- Positive: `go build ./...` and `go vet ./...` remain clean (the file is
  build-tag-excluded).
- Negative: one extra file beyond the "only `go.mod`/`go.sum`" wording in
  `spec.md`. It is short-lived — deleted in S1.T2.
- Negative: `go.sum` also gains `go.yaml.in/yaml/v3` and
  `gopkg.in/check.v1` entries pulled in transitively by testify v1.12.1.
- Neutral: no behavior change; `main.go` still returns 501.

## Alternatives considered

- **Commit only `go.mod`/`go.sum` from `go get`, accept tidy drift**:
  rejected — `go mod tidy` immediately deletes the requirements, so the
  "go.mod requires them" criterion fails as soon as anyone runs `make tidy`.
- **Add the real imports now by implementing S1.T2 in the same change**:
  rejected — out of scope for S1.T1; the ticket boundary exists so the two
  changes stay independently reviewable/bisectable.
- **Leave the modules out of `go.mod` and let S1.T2 add them**: rejected —
  defeats the ticket's purpose of unblocking downstream work.
