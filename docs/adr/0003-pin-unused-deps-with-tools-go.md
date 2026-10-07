# ADR-0003: Pin not-yet-imported dependencies with a build-tagged tools.go

**Status:** Superseded. The file `tools.go` no longer exists. Production and test code now import every dependency directly.

## Context

The project wanted `gopkg.in/yaml.v3` and `github.com/stretchr/testify` in `go.mod` before any package imported them. `go mod tidy` removes a requirement that no package imports. With Go 1.25.1, `go get` adds both modules and the next `go mod tidy` removes them again.

## Decision

A module-root `tools.go` with a `//go:build tools` constraint blank-imported both modules:

```go
//go:build tools
package tools

import (
    _ "github.com/stretchr/testify/require"
    _ "gopkg.in/yaml.v3"
)
```

`go mod tidy` loads packages under all build tags except `ignore`, so it keeps both requirements as direct. Normal builds never set the `tools` tag, so `go build ./...`, `go vet ./...` and the binary do not include the file. The project removed the file when `internal/config` and its tests started to import the modules.

## Consequences

- Both modules had a known pinned version before the first consumer existed.
- `go build` and `go vet` stayed clean.
- testify v1.12.1 adds `go.yaml.in/yaml/v3 v3.0.5` as an `// indirect` require, and `go.sum` gets `gopkg.in/check.v1` entries.
- Accepting tidy drift was not used, because `make tidy` deletes the requirements at once.
- Adding the modules only with their first consumer was not used, because the goal was to make them available in advance.
