# 01: Amend MILESTONES.md with S3.T10–T13 deliverables and exit criteria

**What to build:** `MILESTONES.md`'s Sprint 3 section currently lists only
health/circuit/metrics/dashboard deliverables. Sprint 3 has grown to include a
containerized load balancer, a one-command demo stack, a health endpoint
contract, and a sprint retro. Amend the milestone file so its Sprint 3
deliverables and exit criteria unambiguously cover the four new tickets
(S3.T10–T13). No code touched. Establishes the precedent — cited later in
S3.T13's retro — that mid-sprint scope additions require a `MILESTONES.md`
amendment commit *before* any ticket code lands.

**Blocked by:** None (can start immediately).

**Status:** done

- [x] `MILESTONES.md` Sprint 3 "Deliverables" list adds: multi-stage LB
      Dockerfile; repo-root `docker-compose.yml` (LB + backends + Prometheus +
      Grafana); orchestrator-probe-shaped health endpoint contract; Sprint 3
      retro.
- [x] `MILESTONES.md` Sprint 3 "Exit criteria" list adds: (i) LB runs as a
      container via `docker compose up` at repo root with backends, Prometheus,
      and Grafana; (ii) health endpoint returns structured JSON with liveness
      and readiness semantics; (iii) Sprint 3 retro written.
- [x] The Sprint 4 milestone paragraph on the deferred deployment-target ADR is
      preserved verbatim — this amendment does not resolve or preempt that
      choice.
- [x] Change lands as a standalone commit `docs(milestones): add S3.T10–T13
      deliverables and exit criteria` (`6a85c36`). No other files touched.
- [x] `PROGRESS.md` gains a one-line entry for this ticket following the
      standard `[DONE] S3.T…` protocol so the "amendment-first" precedent is
      auditable in the tactical log too.
