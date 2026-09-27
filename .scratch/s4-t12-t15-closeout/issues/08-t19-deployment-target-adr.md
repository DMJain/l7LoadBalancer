# 08: S4.T19 — ADR: deployment target decision

**What to build:** `docs/adr/0019` recording the deployment-target decision —
bare binary vs Docker vs Kubernetes — with the trade-offs argued from the
system actually built, and SO_REUSEPORT process handoff recorded as
explicitly post-Sprint-5. Docs-only. The grilling settled *when* and *why*
(the informing work now exists); the target itself is the owner's call in the
ADR-writing session. Spec: stories 30–34; the S4.T14 section.

**Blocked by:** 05 (S4.T16 — the ADR follows the code work it is informed
by).

**Status:** ready-for-agent

- [ ] Context cites ADR-0005 and its 2026-09-22 amendment (the demo stack
      does not settle the target), ADR-0014 (health-endpoint contract and
      orchestrator-probe semantics), ADR-0015 (in-process reload; why no
      socket handoff exists), the distroless image and `probe` subcommand
      (S3.T10), and the compose demo stack (S3.T11).
- [ ] Options section: bare binary, Docker, Kubernetes — each argued against
      the system's real properties (three listeners, SIGHUP in-process
      reload, self-probe HEALTHCHECK, no external state, no clustering).
- [ ] Decision and consequences, made by the owner in the ADR session.
- [ ] SO_REUSEPORT process handoff recorded as a Post-Sprint-5 extension,
      citing the MILESTONES list.
- [ ] Index row added to `docs/adr/INDEX.md`.
- [ ] PROGRESS entry records the ADR as closing the Sprint 4 deployment
      deliverable deferred from Sprint 1.
- [ ] Docs-only: no code, no tests (TDD-exempt per AGENTS.md).
