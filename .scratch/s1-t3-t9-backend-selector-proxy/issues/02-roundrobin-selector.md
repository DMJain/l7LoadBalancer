# 02: RoundRobin Selector

**What to build:** The `Selector` interface, the `ErrNoHealthyBackends` sentinel, and the first implementation (`RoundRobin`), so that a load balancer configured with `round_robin` actually distributes requests in rotating order across healthy backends — turning the config choice into real routing behavior for the first time.

**Blocked by:** 01

**Status:** ready-for-agent

- [x] `Selector` interface defined in `internal/balancer`: `Select(ctx, r *http.Request) (*backend.Backend, error)`
- [x] `ErrNoHealthyBackends` is the sentinel returned when the healthy set is empty
- [x] `RoundRobin` selects only from `Registry.Healthy()`, rotating via a lock-free `atomic.Uint64` counter (no mutex)
- [x] Compile-time assertion: `var _ Selector = (*RoundRobin)(nil)`
- [x] Table-driven test: cyclic order over 3 healthy backends
- [x] Empty-healthy-set → `ErrNoHealthyBackends`
- [x] Concurrent test: 1000 selects against 3 healthy backends land within ±5% of the expected 1/3 share each
