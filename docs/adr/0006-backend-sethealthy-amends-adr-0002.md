# ADR-0006: Add Backend.SetHealthy, amending ADR-0002 decision 5

**Status:** Superseded by ADR-0011. ADR-0011 decision 2 replaces `SetHealthy(bool)` with `MarkHealthy()` and `MarkUnhealthy()`. The placement reasons below still apply to those methods.

## Context

ADR-0002 decision 5 gave `Backend` only a health getter, `IsHealthy()`. Selector tests in package `balancer` must show that a selector stops choosing a backend when the backend becomes unhealthy, and starts again when it recovers. Without an exported mutator, code outside package `backend` cannot change health.

## Decision

1. Add `func (b *Backend) SetHealthy(healthy bool)`. It stores into the existing `atomic.Bool`. It has no error return, because a health change cannot fail.
2. This is the permanent contract, not a test helper. The health checker calls it directly on the `*Backend` it holds, in the same way that the proxy calls `IncActive`/`DecActive` on the `*Backend` it selected.
3. The method is on `Backend`, not `Registry`. A `Registry.SetHealthy(name string, healthy bool) error` makes each writer do a name lookup on each health check. `Registry` also does not control the other mutable `Backend` state.
4. This amends only ADR-0002 decision 5.

## Consequences

- Selector tests can change health without access to package internals.
- The health checker uses the same method that the tests use.
- Any caller with a `*Backend` can change its health. The doc comment says that the health subsystem owns the method. The type system does not enforce this.
- The `atomic.Bool` stays behind a method, so the project can still replace it with a richer state type.
- A build-tagged, test-only function was not used. It is more complex than a permanent method and gives no benefit.
