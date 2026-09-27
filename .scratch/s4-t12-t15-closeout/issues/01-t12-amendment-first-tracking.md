# 01: S4.T12 — Amendment-first: tracking for the close-out

**What to build:** The tracking amendment for the Sprint 4 close-out, landed
alone before any close-out code (amendment-first precedent of S4.D0/S4.D1).
PROGRESS.md gains a "Sprint 4 — Close-out" section with one row per close-out
ticket (S4.T12–S4.T21) and their acceptance criteria; MILESTONES.md's Sprint 4
deliverables gain one line for interpolation (inserted after the
connection-pool-tuning bullet): "Config env-var interpolation for backend URLs
(`${VAR}`), so deployment secrets stay out of the config file and validation
errors never echo resolved values." The stale "hot-reload rejected, not
ignored" proposed ticket is closed (already implemented across
S4.T1/T4.0/T7/T8 — `NonBackendChanges` names `reload`, `server`, `transport`
and every original non-backend field), and the reload-outcome counter metric
is deferred to Sprint 5's benchmarking scope. Both closures are recorded in
PROGRESS. Docs-only: no code, no tests (TDD-exempt per AGENTS.md).

**Blocked by:** S4.T11 (retry-policy ADR — the connection-lifecycle bundle is
closed; this bundle opens on it).

**Status:** ready-for-agent

- [ ] PROGRESS.md gains the "Sprint 4 — Close-out" section with the ten
      ticket rows (S4.T12–S4.T21) and their acceptance criteria.
- [ ] MILESTONES.md Sprint 4 deliverables gain the interpolation line.
- [ ] The stale hot-reload proposed ticket is closed with the reason recorded.
- [ ] The reload-outcome counter metric is deferred to Sprint 5 with the
      reason recorded.
- [ ] The amendment lands as one commit touching only PROGRESS.md and
      MILESTONES.md; no code.
