# 03: LeastConnections Selector

**What to build:** The second `Selector` implementation, `LeastConnections`, so a load balancer configured with `least_conn` routes each request to the healthy backend currently doing the least work, rather than just taking a turn — giving operators a second, meaningfully different routing behavior to choose from.

**Blocked by:** 01 (needs the Registry only — not RoundRobin; the two selectors are independent implementations of the same interface)

**Status:** ready-for-agent

- [x] `LeastConnections` implements `Selector`, scanning `Registry.Healthy()` and picking the lowest `ActiveConns()`
- [x] Ties broken deterministically: first tied backend in registry order wins (reproducible tests)
- [x] Reads `ActiveConns()` only — never mutates it (mutation is the proxy's job, ticket 05)
- [x] Empty-healthy-set → `ErrNoHealthyBackends`
- [x] Compile-time assertion: `var _ Selector = (*LeastConnections)(nil)`
- [x] Table-driven test: pre-seeded `ActiveConns` values → minimum is chosen; explicit tie-break case; empty-registry case

**Note:** If the `Selector` interface / `ErrNoHealthyBackends` (ticket 02) hasn't landed yet when this starts, define them here instead — whichever of 02/03 lands first owns `selector.go`.
