# 01: Tracking amendment (S5.D0)

**What to build:** Update the tracking files so that any agent opening `PROGRESS.md` sees every ticket in this bundle, what blocks it, and why two of the original IDs no longer exist. After this, a ticket in the bundle can be claimed under AGENTS.md Step 1. Docs only: TDD-exempt.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

Spec: `../spec.md`. Precedent: S4.D0 and S4.T12 (amendment-first tracking).

- [ ] The Sprint 5 section of `PROGRESS.md` gains a bundle paragraph naming this spec and all 20 tickets. Each ticket (02–20) gets a `[TODO]` entry with its S5 ID, title, spec link and `Depends:` line, matching this bundle's blocking edges.
- [ ] `PROGRESS.md` records **S5.T7 as merged into S5.T6**, because the latency sweep's rates are fractions of the peak found in the same invocation.
- [ ] `PROGRESS.md` records **S5.T11 as dropped per ADR-0019**, which rejects orchestration manifests for a target the project doesn't adopt.
- [ ] Under "Proposed tickets (awaiting owner approval)", `PROGRESS.md` lists **S5.T13–T15** (README architecture diagram, design-decisions doc, what-I'd-do-differently doc) as proposed. They are not approved.
- [ ] The Sprint 5 deliverables in `MILESTONES.md` mention the nearest-equivalent Nginx comparisons, the `degraded` slice, the no-op and drain reload runs, and `make bench-repro` as the reproducer behind the exit criterion.
- [ ] The four `CONTEXT.md` glossary entries added during the grilling (**No-op reload**, **Competitor**, **Nearest-equivalent**, **Hot key**) and the spec are committed with this ticket.
- [ ] No code or harness changes.
