# 01: Rename `SetHealthy` to `MarkHealthy`/`MarkUnhealthy`

**What to build:** Split `Backend.SetHealthy(bool)` into `MarkHealthy()` and
`MarkUnhealthy()`, so the recovery asymmetry ADR-0011 establishes (only
active health checks may prove a backend healthy again; either active or
passive detection may mark it unhealthy) is visible at the call site instead
of only documented in prose. Prefactor: unblocks both S3.T1 (active health
checks) and S3.T2 (passive outlier detection), neither of which needs the
other's substance, only this rename.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] `Backend.SetHealthy(bool)` removed; `Backend.MarkHealthy()` and
      `Backend.MarkUnhealthy()` added in its place, same underlying
      `atomic.Bool` field
- [ ] Every existing caller updated to the new names (S1.T8's cross-selector
      health-transition tests currently call `SetHealthy` directly)
- [ ] No behavior change: a backend's health-transition semantics are
      identical to today, only the method names and call-site conventions
      differ — decision already recorded in ADR-0011 (amends ADR-0006, which
      itself amends ADR-0002 decision 5); no new ADR needed for this ticket
- [ ] `make test` and `make test-race` pass unchanged
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on completion

## Comments
