# ADR-0006: Add Backend.SetHealthy, amending ADR-0002 decision 5

- **Status**: Accepted
- **Date**: 2026-09-18
- **Deciders**: Darshan Jain (project owner)

## Context

ADR-0002 decision 5 froze `Backend`'s health surface to `IsHealthy() bool`
only — a getter — because health was meant to be written exclusively by
Sprint 3's health checker (active probes + passive outlier detection), which
does not exist yet.

S1.T8 (cross-selector regression tests) requires proving that both
`RoundRobin` and `LeastConnections` stop selecting a backend once it goes
unhealthy mid-run and resume once it recovers — a genuine health
*transition*, not just a fixed initial state. With no exported mutator,
package `balancer` (and any test outside package `backend`) has no way to
flip a backend's health at all before Sprint 3 lands.

## Decision

1. Add `func (b *Backend) SetHealthy(healthy bool)` to
   `internal/backend/backend.go`, storing directly into the existing
   `atomic.Bool`. No new field, no error return — flipping health cannot
   fail.
2. This method is the permanent contract, not a temporary test shim:
   Sprint 3's health-checker goroutines (one per backend, per AGENTS.md)
   will call this exact method directly on the `*Backend` they hold,
   symmetric with how the proxy already calls `IncActive`/`DecActive`
   directly on the `*Backend` it selected.
3. Placement on `Backend` rather than `Registry` (the rejected alternative)
   is deliberate: a `Registry.SetHealthy(name string, healthy bool) error`
   would force every writer — including the future per-backend
   health-check goroutine — to round-trip through a name lookup on every
   tick, and `Registry` doesn't otherwise mediate `Backend`'s mutable
   state (`IncActive`/`DecActive` already bypass it).
4. This ADR amends ADR-0002 decision 5 only: `Backend`'s exported surface
   is now `IsHealthy` / `SetHealthy` / `IncActive` / `DecActive` /
   `ActiveConns`, not just the original four. Nothing else in ADR-0002
   changes.

## Consequences

- Positive: S1.T8's health-transition tests are possible without reaching
  into package-internal state or awkward workarounds.
- Positive: zero rework needed when Sprint 3's real health checker
  arrives — it calls the exact method S1.T3/T8 already tested against.
- Negative: `Backend`'s health can now be set by any caller holding a
  `*Backend`, not just "the health checker" — Sprint 1 has no such caller
  to misuse it, but a doc comment on `SetHealthy` states it is owned by the
  health-check subsystem by convention; this is not enforced by the type
  system.
- Neutral: the encapsulation goal from ADR-0002 (swap `atomic.Bool` for a
  richer state enum later) is unaffected — callers still go through a
  method, not the field.

## Alternatives considered

- **`Registry.SetHealthy(name, healthy) error`**: rejected — asymmetric
  with `IncActive`/`DecActive`, and adds a per-tick name lookup Sprint 3's
  per-backend goroutines don't need.
- **No mutator; move the health-transition test into package `backend`**:
  rejected — S1.T8 is explicitly a `balancer`-package cross-selector test
  per PROGRESS.md; moving it would misplace the test relative to what it's
  actually testing (selector behavior, not backend internals).
- **Build-tagged test-only exported func**: rejected — more complex than a
  permanent, symmetric method for no real benefit, and would need to be
  thrown away once Sprint 3 lands anyway.
