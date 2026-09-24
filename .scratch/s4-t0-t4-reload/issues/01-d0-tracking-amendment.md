# 01: S4.D0 — Tracking amendment (MILESTONES + PROGRESS)

**What to build:** Make the Sprint 4 reload work auditable before any of it is
built, following the Sprint 3 amendment-first precedent (S3.T10–T13.D0). A
reader of `MILESTONES.md` and `PROGRESS.md` can see every S4 reload ticket,
its acceptance criteria, and the follow-ups deliberately left out — without
opening this spec. Spec: stories 46–47, decisions D1–D2.

**Blocked by:** None (can start immediately).

**Status:** ready-for-agent

- [ ] `MILESTONES.md` Sprint 4 deliverables gain one bullet: the programmatic
      application seam (build/run/reload), sourced from the Sprint 3 retro's
      handoff. The existing zero-downtime SIGHUP reload bullet, the exit
      criteria, and the deferred-deployment-target paragraph are unchanged.
- [ ] `PROGRESS.md` gains Sprint 4 entries S4.T0, S4.T1, S4.T2, S4.T3.0,
      S4.T3, S4.T4.0, S4.T4 (all `[TODO]`), each with Goal, Depends on,
      Acceptance, and Test approach lines drawn from tickets 02–08 of this
      bundle, and a pointer to this spec.
- [ ] `PROGRESS.md` → *Proposed tickets (awaiting owner approval)* gains four
      entries: `lb_config_reloads_total{result}`; client-cancellation
      misclassified as backend failure by the proxy's error path; hot-reload of
      algorithm/health/circuit/listen/drain-window fields; pre-warming added
      backends before the swap.
- [ ] `PROGRESS.md`'s status line reads Sprint 4 in progress.
- [ ] Lands alone as `docs(milestones): add S4 reload tickets and seam deliverable`
      touching only `MILESTONES.md` and `PROGRESS.md`. Docs-only, TDD-exempt.
