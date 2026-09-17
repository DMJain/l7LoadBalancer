# 02: Implement config package — Load, Validate, and tests

**What to build:** An operator writes a YAML config file with a listen address, algorithm choice, and backend list. `Load()` strictly deserializes it (unknown fields are an error, not silently ignored). `Validate()` normalizes the algorithm default (empty → `round_robin`) then checks every field: listen address is valid `host:port` syntax, at least one backend exists, backend names match `^[a-zA-Z0-9_-]+$` and are unique, backend URLs have an `http`/`https` scheme with a non-empty host and no query string or fragment, and the algorithm is in the set of currently-implemented algorithms. After `Validate()` returns nil, every field in `*Config` is guaranteed populated and correct — no downstream consumer ever needs to re-check or default any field. Invalid configs produce a clear, specific error message pointing at the exact problem.

The test suite is table-driven and serves as the executable specification of the config contract. `configs/example.yaml` is updated to a realistic 3-backend config that passes validation.

**Blocked by:** 01 (Add Go dependencies)

**Status:** ready-for-agent

## Acceptance criteria

- [ ] `Load(path)` reads YAML using `yaml.NewDecoder(f).KnownFields(true)` — not `yaml.Unmarshal`
- [ ] `Load` is pure deserialization — no defaulting, no validation
- [ ] `Load` errors are wrapped: `fmt.Errorf("config: load %s: %w", path, err)`
- [ ] `Validate()` sets `Algorithm = "round_robin"` when empty (normalize-then-validate)
- [ ] `Validate()` is fail-fast — returns the first validation error encountered
- [ ] Listen: `net.SplitHostPort` must succeed; port must parse as uint16
- [ ] Backends: at least one required
- [ ] Backend names: non-empty, match `^[a-zA-Z0-9_-]+$`, no duplicates
- [ ] Backend URLs: `url.Parse` succeeds, scheme is `http` or `https`, host is non-empty, no query string, no fragment. Paths are allowed.
- [ ] Algorithm: checked against a package-level `map[string]struct{}` of implemented algorithms (`round_robin`, `least_conn` only in Sprint 1). Case-sensitive. Unimplemented Sprint 2 algorithms (`consistent_hash`, `p2c_ewma`) are rejected.
- [ ] `configs/example.yaml` updated to 3-backend config from the frozen contract in `docs/design/sprint-1-contracts.md`
- [ ] Table-driven tests cover all 18 cases: (a) file not found, (b) invalid YAML, (c) unknown field, (d) missing listen, (e) invalid listen no port, (f) zero backends, (g) no-scheme URL, (h) URL with query, (i) URL with fragment, (j) duplicate names, (k) empty name, (l) invalid name chars, (m) unknown algorithm, (n) unimplemented algorithm, (o) case sensitivity, (p) algorithm default omitted, (q) algorithm default empty, (r) happy-path round-trip with full struct assertion
- [ ] `make test` passes
- [ ] `make test-race` passes
- [ ] `go vet ./...` passes
- [ ] `make fmt` produces no diff
- [ ] `PROGRESS.md` updated: S1.T2 marked `[DONE]` with completion timestamp
- [ ] Session log appended to `docs/sessions/`

## Implementation notes

**File-level split within the ticket:** If the test table exceeds ~500 lines, split into `config_load_test.go` (happy-path and file-level errors) and `config_validate_test.go` (field-level rejections). Same commit, cleaner organization.

**Commit history within the ticket:** Aim for ~3 atomic commits: (i) `Load` + happy-path test, (ii) `Validate` + field-level rejection tests, (iii) `example.yaml` update + PROGRESS.md closeout. `git bisect` cares about commits, not tickets.

**TDD protocol:** Tests written before implementation. Tests must compile against the existing panic stubs and fail (Red). Implementation makes them pass (Green). Refactor only if tests stay green.
