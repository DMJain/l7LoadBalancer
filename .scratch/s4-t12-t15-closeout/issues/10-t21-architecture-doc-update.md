# 10: S4.T21 — Architecture doc update

**What to build:** The additive Sprint 4 update to `docs/architecture.md` —
the subsystems, the decision-index rows, and the completeness header — so
the architecture reference stays true after the close-out. Spec: stories
39–41; the S4.T15 section.

**Blocked by:** 08 (S4.T19 — the decision-index rows cover ADR-0015 through
ADR-0019).

**Status:** ready-for-agent

- [ ] Sprint 4 sections added: the app seam, reload and drain, connection
      lifecycle, transport tuning, soak results, env interpolation, and the
      e2e SIGHUP test.
- [ ] Decision-index rows added for ADR-0015 through ADR-0019.
- [ ] The header's "Sprints 1–3 are complete" reference is updated to
      reflect the Sprint 4 close-out.
- [ ] The soak results are recorded with their numbers, so the
      production-resilience claims have evidence.
- [ ] Docs-only: no code, no tests (TDD-exempt per AGENTS.md).
