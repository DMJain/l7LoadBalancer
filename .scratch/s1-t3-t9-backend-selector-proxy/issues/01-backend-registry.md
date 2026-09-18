# 01: Backend Registry

**What to build:** A concurrency-safe `Backend` and `Registry` in `internal/backend` that track identity, health, and active-connection count behind method-only access, so every downstream package (selectors, proxy) can read and mutate backend state safely under concurrent requests without holding a lock themselves.

**Blocked by:** None (can start immediately — S1.T2 is done)

**Status:** ready-for-agent

- [ ] `Backend` has exported `Name string`, `URL *url.URL`; unexported `healthy` (`atomic.Bool`) and `active` (`atomic.Int64`), reachable only via `IsHealthy()`, `SetHealthy(bool)`, `IncActive()`, `DecActive()`, `ActiveConns() int64`
- [ ] `NewRegistry(cfgs []config.BackendConfig) (*Registry, error)` builds backends from already-validated config; every backend starts healthy (no health checker exists yet)
- [ ] `Registry` holds an ordered slice only (no name-indexed map); `All()` and `Healthy()` each return a fresh slice per call, preserving registry order
- [ ] Concurrent `IncActive`/`DecActive` from many goroutines converges to the correct final count under `go test -race`
- [ ] Concurrent `SetHealthy` toggling alongside reads is race-free under `-race`
- [ ] Table-driven tests for construction, `All()` (returns everything regardless of health), `Healthy()` (filters correctly)
