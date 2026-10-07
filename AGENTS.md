# AGENTS.md

Contributor guide for this repository. `CLAUDE.md` and `GEMINI.md` are symlinks to this file.

## Project

l7LoadBalancer is a Layer 7 HTTP load balancer in Go. The request path uses the standard library (`net/http`, `net/http/httputil`). The one exception is h2c, which uses `golang.org/x/net/http2/h2c`.

The load balancer puts a pluggable backend-selection policy on top of `httputil.ReverseProxy`. `ReverseProxy` handles hop-by-hop headers, `X-Forwarded-For`, buffering and flushing. The project work is in selection, concurrency, health checks, circuit breaking and hot reload.

```
Client → http.Server → Proxy.ServeHTTP → Selector.Select → ReverseProxy.Director → Backend → Response
                                                                                         ↓
                                                                          ModifyResponse (body wrapper)
                                                                                         ↓
                                                                              DecActive on Close
```

## Where to look

- `README.md`: overview, quick start, results.
- `CONTEXT.md`: domain glossary. Use its terms.
- `docs/architecture.md`: architecture, interfaces, YAML schema, concurrency ownership.
- `docs/adr/INDEX.md`: architecture decision records. Read the full ADR before you change the subsystem it covers.

## Package dependency graph

The graph is acyclic. Non-test import edges:

```
cmd/l7LoadBalancer → app, config, logger
internal/app       → proxy, health, circuit, metrics, balancer, backend, config, logger
internal/proxy     → backend, balancer, logger, metrics
internal/balancer  → backend, config
internal/health    → backend, logger, metrics
internal/circuit   → backend, logger, metrics
internal/backend   → config
internal/logger    — leaf
internal/metrics   — leaf
```

**Rule**: `internal/backend` must not import `internal/balancer`. A `Backend` does not know how it is selected.

## Directory map

- `cmd/l7LoadBalancer/`: binary entry point. Flags, file I/O, signals.
- `internal/app/`: wiring for `Build`/`Run`. Production, the chaos harness and tests use it to assemble the system.
- `internal/proxy/`: reverse-proxy handler and request path.
- `internal/backend/`: backend struct, registry, state.
- `internal/balancer/`: `Selector` interface and implementations (round-robin, least-connections, consistent-hash with bounded loads, p2c-ewma).
- `internal/health/`: active health checks and passive outlier detection.
- `internal/circuit/`: per-backend circuit breaker.
- `internal/config/`: YAML load, validation, hot reload.
- `internal/metrics/`: Prometheus instruments.
- `internal/logger/`: `log/slog` setup and field names.
- `configs/`: example configuration files.
- `deployments/docker/`: dummy backends, Nginx comparison, chaos, observability.
- `demo/`: local live demo stack. All ports bind to `127.0.0.1`.
- `bench/`: benchmark scripts and results.
- `docs/`: architecture, ADRs, design notes.
- `scripts/`: helper scripts.

## Commands

Run `make help` for the full list.

- `make build`: build `bin/l7LoadBalancer`.
- `make run`: build and run with `configs/example.yaml`.
- `make test`: run all tests.
- `make test-race`: run all tests with `-race`.
- `make vet`: run `go vet ./...`.
- `make fmt`: run `gofmt` and `goimports`.
- `make bench`: run Go benchmarks.
- `make tidy`: run `go mod tidy`.
- `make clean`: remove build artifacts.

## Code conventions

- Go 1.25.1. Format with `gofmt` and `goimports`.
- Package names are lowercase, one word where possible. Do not use `util` or `common`.
- Wrap errors with `fmt.Errorf("context: %w", err)`. Do not make `errors.New` values for dynamic messages.
- The only exported sentinel for callers to branch on is `balancer.ErrNoHealthyBackends`. `backend.ErrDrainWindowExpired` is a `context.Cause` value, not a branch condition (ADR-0016).
- Request-scoped functions take `context.Context` as the first argument.
- Define interfaces at the consumer. Keep them small. `balancer.Selector` is the exception (ADR-0002).
- Use channels for coordination, atomics for counters and a mutex for shared state. Put a comment at the top of each file with shared state that describes its concurrency model.
- Log with `log/slog` only, as structured key-value pairs. Use the field names in `internal/logger/doc.go`. Do not use `fmt.Println` outside tests.

## Tests first

Write the tests before the code.

- Use table-driven tests where the inputs are enumerable.
- Use `testify/require` for setup checks and `testify/assert` for value checks.
- Use `httptest.NewServer` for integration tests.
- Test concurrent code with goroutines and `-race`.
- Commit tests with the code, or before it. Never after.

Before each commit:

1. Run `make test-race`. Do not commit if it fails.
2. Run `make vet`.
3. Run `make fmt`. The diff must be empty.

## Commits

Use Conventional Commits: `feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `chore:`, `bench:`, `perf:`. Use the imperative mood, lowercase after the prefix and no trailing period. Example: `feat(config): implement Load and Validate`.

## Ask before you

- Add a heavy dependency (frameworks, ORMs, config libraries such as Viper).
- Change the `go.mod` module path.
- Restructure the package boundaries in `internal/`.
- Change a documented interface, struct field or error type.
- Make a non-trivial design change. Write an ADR in `docs/adr/` first and add it to `docs/adr/INDEX.md`.
- Add a feature that nobody asked for.
