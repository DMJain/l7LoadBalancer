# 09: S4.T20 — Sprint 4 retro

**What to build:** `docs/sprint-4-retro.md` mirroring the Sprint 3 shape: a
deliverables-shipped table with the commit that landed each ticket, a
deviations section, exit-criteria evidence, and a what-we-differently
section. Docs-only, and after the ADR: the retro cites it as shipped
evidence. Spec: stories 35–38; the S4.T15 section.

**Blocked by:** 08 (S4.T19 — the deployment-target ADR).

**Status:** ready-for-agent

- [ ] Deliverables table: one row per close-out ticket with the commit(s)
      that landed it (claim commits omitted in favour of the implementation
      commits, per the Sprint 3 table's convention).
- [ ] Deviations section records at minimum: the R0 numbering reconciliation;
      the exit criterion evidenced in-process first (S4.T4) and at the OS
      boundary second (S4.T14); the closed and deferred proposed tickets; and
      any `validateBackendURL` wording change from S4.T17.
- [ ] Exit-criteria evidence: 1000-in-flight zero-drop (both tests cited by
      name); backend death mid-response → clean 502 (S4.T6); one-hour soak
      with the committed numbers (S4.T10: 59.66M requests, 7,175
      cancellations, 2,789 failures, 12 reloads, goroutines 35→30, post-GC
      heap 930 KB→804 KB — cite the PROGRESS entry as the source of truth).
- [ ] What-we-differently section, honest about the numbering
      reconciliation and any spec-to-implementation conflicts encountered.
- [ ] Docs-only: no code, no tests (TDD-exempt per AGENTS.md).
