# Spec: S1.T1 + S1.T2 — Go Dependencies & Config Package

Status: ready-for-agent

---

## Problem Statement

The load balancer has frozen cross-package contracts (stubs with panic bodies) but no runtime dependencies and no working config loader. An operator cannot yet write a YAML file, point the LB at it, and get a validated, populated `*Config` back. Until the config package works, no downstream task (backend registry, selectors, proxy wiring) can be implemented.

## Solution

Two tasks, executed sequentially:

1. **S1.T1**: Pull in the two third-party dependencies Sprint 1 needs — `gopkg.in/yaml.v3` (YAML parsing) and `github.com/stretchr/testify` (test assertions) — so later tasks aren't blocked on dependency wrangling.

2. **S1.T2**: Implement `Load()` and `Validate()` in `internal/config`, replacing the panic stubs with working code. Update `configs/example.yaml` to a realistic 3-backend example that passes validation. Write comprehensive table-driven tests covering every edge of the contract.

After both tasks, an operator can write a YAML config file and the LB can load, normalize, and validate it — producing a `*Config` where every field is populated and correct, or a clear error message pointing at the exact problem.

## User Stories

1. As a load balancer operator, I want to write a YAML config file with a listen address, algorithm, and backend list, so that I can configure the LB declaratively without recompiling.
2. As a load balancer operator, I want unknown YAML fields (typos like `listn` instead of `listen`) to produce a clear error at load time, so that I catch config mistakes before the LB starts.
3. As a load balancer operator, I want the algorithm to default to `round_robin` when I omit the `algorithm` field, so that I don't have to specify the most common choice.
4. As a load balancer operator, I want an empty `algorithm: ""` field to behave identically to omitting it, so that the defaulting behavior is consistent regardless of how I express "use the default."
5. As a load balancer operator, I want validation to reject an empty listen address, so that I don't discover the problem at bind time after all other setup has completed.
6. As a load balancer operator, I want validation to reject a listen address that isn't valid `host:port` syntax (e.g. `foobar` with no port, or port `99999` outside uint16 range), so that I get a config-level error instead of a cryptic OS bind error at startup.
7. As a load balancer operator, I want validation to require at least one backend, so that the LB doesn't start in a state where it can't route anything.
8. As a load balancer operator, I want validation to reject backend URLs without an `http` or `https` scheme, so that schemeless strings like `localhost:9001` (which `url.Parse` silently misparses) don't produce confusing runtime behavior.
9. As a load balancer operator, I want validation to reject backend URLs with query strings or fragments, so that ambiguous reverse-proxy behavior (merge query? override?) is prevented at the config boundary.
10. As a load balancer operator, I want validation to allow backend URLs with paths (e.g. `http://internal:9001/v2`), so that I can route to backends that serve from a subpath.
11. As a load balancer operator, I want validation to reject duplicate backend names, so that log lines, metrics labels, and health status are always unambiguous.
12. As a load balancer operator, I want validation to reject empty backend names, so that downstream observability (Prometheus labels, structured log fields) is never silently unattributed.
13. As a load balancer operator, I want validation to reject backend names with characters outside `[a-zA-Z0-9_-]`, so that names are safe across all downstream contexts (Prometheus labels, log grep, future admin UIs) without escaping.
14. As a load balancer operator, I want validation to reject algorithm strings that aren't currently implemented (e.g. `consistent_hash` in Sprint 1), so that I learn at config time — not at runtime — that my config won't work.
15. As a load balancer operator, I want validation to be case-sensitive for algorithm strings, so that `Round_Robin` is rejected and the canonical `round_robin` is the only accepted form.
16. As a load balancer operator, I want validation to reject completely unrecognized algorithm strings (e.g. `random`), so that typos are caught early.
17. As a downstream developer (implementing backend registry, selectors, or proxy), I want `Validate()` to guarantee that after it returns `nil`, every field in `*Config` is populated and valid, so that I never need to re-check or default any field.
18. As a test author, I want the config test suite to serve as an executable specification of the config contract, so that I can read the test table and understand exactly what the parser accepts and rejects.

## Implementation Decisions

### S1.T1 — Dependencies

- Add `gopkg.in/yaml.v3` and `github.com/stretchr/testify` via `go get`.
- Commit `go.mod` and `go.sum` together. No other files touched.
- Verify with `go build ./...` and `go vet ./...` — both must still pass.
- This is a dependency-only task: no code changes, no YAML changes, no tests.

### S1.T2 — Config package

#### `Load(path string) (*Config, error)`

- Open the file. If it doesn't exist, return an error wrapping the underlying OS error (so `errors.Is(err, os.ErrNotExist)` works for callers who need it).
- Use `yaml.NewDecoder(f).KnownFields(true)` — **not** `yaml.Unmarshal`. `KnownFields(true)` rejects unknown YAML fields at decode time, catching typos like `listn` instead of `listen`.
- `Load` is pure deserialization. It does not normalize, default, or validate. Callers must call `Validate()` separately.
- Error wrapping format: `fmt.Errorf("config: load %s: %w", path, err)`.

#### `Validate() error`

- **Normalize first, then validate.** When `c.Algorithm == ""`, set it to `AlgorithmRoundRobin` (`"round_robin"`). After `Validate()` returns `nil`, `c.Algorithm` is guaranteed non-empty and valid. This is a post-condition invariant: every downstream consumer reads it and never duplicates the defaulting logic.
- The name `Validate()` mildly lies because it mutates. For Sprint 1 with one mutation, the extra method split (`Normalize()` + `Validate()`) isn't worth it. If defaulting logic grows (Sprint 3 health-check defaults, Sprint 4 timeout defaults), consider splitting then.
- **Fail-fast.** Return the first validation error encountered. Reasons: YAGNI with five checks and an operator at a startup log; simpler test surface (one error per failure case); easier to migrate to collect-all later than the reverse.
- **Validation order**: Listen → backends count → individual backend validation (name, URL) → name uniqueness → algorithm.

#### Listen address validation

- Use `net.SplitHostPort(c.Listen)` — if it fails, return a config error. This catches `foobar` (no colon), `1.2.3.4` (no port), `http://:8080` (accidentally pasted URL).
- If `SplitHostPort` succeeds, parse the port string as a uint16 (`strconv.ParseUint(port, 10, 16)`). This catches port `99999` out of range.
- **Do not** attempt to bind, resolve DNS, or check port availability. That's runtime state, not config text. Validation is a pure function of the input string.
- The dividing line: syntactic checks (is the string a valid `host:port`?) in `Validate()`; runtime checks (is the port bindable?) at `http.Server.ListenAndServe`.

#### Backend name validation

- Reject empty names. An empty `backend=""` in Prometheus labels produces useless unattributed time series.
- Require names to match `^[a-zA-Z0-9_-]+$`. This restricts to the intersection of what's safe across all downstream contexts: Prometheus label values, structured log field values, grep targets, future admin UI display. Names with dots, colons, slashes, quotes, spaces, or non-ASCII are rejected.
- Check uniqueness via a `map[string]struct{}` accumulator. Duplicate names are rejected.

#### Backend URL validation

- Parse with `url.Parse`. If parsing fails, reject.
- Require `u.Scheme` to be exactly `"http"` or `"https"`. Reject schemeless URLs — `url.Parse("localhost:9001")` silently parses `localhost` as the scheme and `9001` as the opaque path.
- Require `u.Host` to be non-empty after parsing.
- Reject URLs with a query string (`u.RawQuery != ""`). Reverse proxy behavior with a backend-URL query is ambiguous (merge with request query? override?). Force the operator to be explicit.
- Reject URLs with a fragment (`u.Fragment != ""`). Same rationale.
- Allow paths. `httputil.ReverseProxy` handles path prefix joining correctly in its default Director.

#### Algorithm validation

- Represent the set of currently-implemented algorithms as a package-level `map[string]struct{}` — e.g. `var implementedAlgorithms = map[string]struct{}{AlgorithmRoundRobin: {}, AlgorithmLeastConn: {}}`.
- After defaulting (empty → `round_robin`), check membership in this set. Reject anything not in it — including Sprint 2 algorithms (`consistent_hash`, `p2c_ewma`) that are defined as constants but not yet implemented.
- This encodes "will this work?" not "does this parse?" — validation catches broken configs at the earliest point where a good error message can be given.
- Case-sensitive: `Round_Robin` is rejected; only `round_robin` is accepted.
- When Sprint 2 lands, adding two entries to the map is a one-line diff, atomic with the sprint that implements them.
- Use `map[string]struct{}` over `map[string]bool` — idiomatic Go "set", clearer intent, avoids the ambiguity of what `false` means.

#### `configs/example.yaml`

- Replace the placeholder (empty backends) with the 3-backend example from the frozen contract in `docs/design/sprint-1-contracts.md`.
- Must pass `Load()` + `Validate()` with no error.

#### Error wrapping convention

- All errors: `fmt.Errorf("config: <context>: %w", err)`. No exported sentinel errors. Callers don't need to distinguish "empty listen" from "bad URL" programmatically — they render the message.
- The one exception is the underlying OS error from file open, which remains inspectable via `errors.Is(err, os.ErrNotExist)` through the wrapping chain.

## Testing Decisions

### Seam

The testing seam is the **`internal/config` package boundary** — the two exported functions `Load(path)` and `(*Config).Validate()`. Tests exercise them end-to-end: write YAML to temp files, call `Load`, call `Validate`, assert on the returned `*Config` or error. No mocks. No internal function testing. This is the highest and only seam needed.

### What makes a good test here

- Tests exercise **external behavior**: "given this YAML input, does `Load` + `Validate` produce this output or this error?" They do not test internal implementation details (e.g., which order fields are checked internally, or whether a regex or a loop is used for name validation).
- Each test case is a **table entry** — a struct with a name, input YAML string, and expected outcome (either a populated `*Config` for success cases, or an error substring/`errors.Is` target for failure cases).
- `testify/require` for setup assertions (file creation, Load succeeding when expected). `testify/assert` for value checks.

### Test cases

| ID | Case | Input shape | Expected outcome |
|----|-------|-------------|------------------|
| a | File not found | Non-existent path | Error wrapping `os.ErrNotExist` |
| b | Invalid YAML syntax | Broken YAML | Decode error from `Load` |
| c | Unknown YAML field | `listn: ":8080"` | Load error (KnownFields) |
| d | Missing listen | `listen: ""` | Validation error |
| e | Invalid listen (no port) | `listen: "foobar"` | Validation error |
| f | Zero backends | `backends: []` | Validation error |
| g | Malformed URL (no scheme) | `url: "localhost:9001"` | Validation error |
| h | URL with query string | `url: "http://x:9001?debug=1"` | Validation error |
| i | URL with fragment | `url: "http://x:9001#foo"` | Validation error |
| j | Duplicate backend names | Two backends named `"a"` | Validation error |
| k | Empty backend name | `name: ""` | Validation error |
| l | Invalid backend name chars | `name: "backend.primary"` | Validation error |
| m | Unknown algorithm | `algorithm: "random"` | Validation error |
| n | Unimplemented algorithm | `algorithm: "p2c_ewma"` | Validation error |
| o | Case sensitivity | `algorithm: "Round_Robin"` | Validation error |
| p | Algorithm defaulting (omitted) | No `algorithm` key | `cfg.Algorithm == "round_robin"` after Validate |
| q | Algorithm defaulting (empty) | `algorithm: ""` | Same as (p) |
| r | Happy-path round-trip | Full valid 3-backend config | All fields populated, algorithm defaulted if omitted, URL parsed correctly |

### Prior art

- `internal/backend/backend_test.go` has a scaffold placeholder (`TestScaffold`). The config tests will be the first real test file in the project and will set the pattern for all subsequent test files.
- The table-driven style with `testify` follows the convention specified in `AGENTS.md`.

### TDD protocol

- Tests written **before** implementation. Test file committed alone (optional but encouraged). Tests must compile against the existing panic stubs and **fail** (Red phase).
- Implementation written to make all tests pass (Green phase).
- `make test`, `make test-race`, `go vet ./...`, `make fmt` — all clean before closing.

## Out of Scope

- **S1.T3 through S1.T10**: Backend registry, selectors, proxy, wiring, docker-compose, retro. All downstream of S1.T2 but not part of this spec.
- **Hot-reload**: Config is immutable after init in Sprint 1. `atomic.Pointer[Config]` swap is Sprint 4.
- **Collect-all validation**: Fail-fast for Sprint 1. Revisit in Sprint 4 if hot-reload operators need batch diagnostics.
- **`Normalize()` / `Validate()` split**: One mutation (algorithm defaulting) doesn't justify the extra method. Revisit if defaulting logic grows in Sprint 3+.
- **Backend URL path-joining semantics**: Allowed by validation, but the actual path-joining behavior is the proxy's concern (S1.T6), not config's.
- **Sprint 2 algorithm acceptance**: `consistent_hash` and `p2c_ewma` are rejected by Sprint 1 validation. They become accepted when Sprint 2 adds them to the implemented set.

## Further Notes

### Frozen contracts

All interfaces, struct shapes, and method signatures are frozen by S1.T0.5 and documented in `docs/design/sprint-1-contracts.md` and ADR-0002. Any deviation requires a superseding ADR or explicit user sign-off — not a silent change.

### Key principle from the grilling session

**Validation encodes "will this work?", not "does this parse?"** Everything knowable from the config text alone is checked at validation time. Everything that depends on runtime state (port bindability, backend reachability, DNS resolution) is deferred to startup. The dividing line is: pure function of the input → validate; requires environment → defer.

### Coupling rationale for algorithm set

The `implementedAlgorithms` map lives in `config` (not injected by the caller) because `config` already defines the algorithm constants — knowing which ones are currently live is a natural concern for this package. This is healthy coupling (reflects real domain structure) not accidental coupling (leaking unrelated internals). If a future scenario requires decoupling (e.g., a standalone config linter that ships separately from the LB binary), refactor then.
