# 07: S3.T13 — Sprint 3 retro and additive `docs/architecture.md` update

**What to build:** The Sprint 3 closeout artifacts. Two files touched:
a new `docs/sprint-3-retro.md` and additive updates to `docs/architecture.md`.
No code changes, no new ADRs — the retro's shape becomes the template by
existing (matching S2.T8's precedent).

**`docs/sprint-3-retro.md`** — mirrors the S2.T8 shape. Section list, in
order:

1. **Deliverables shipped** — every Sprint 3 task ID with a one-line summary
   and a link to the commit or PR that landed it. Covers T0.1–T9, T6.5, and
   the four new tickets T10–T13.
2. **Deviations from MILESTONES/spec** — five paragraphs, one per known
   deviation:
   1. **S3.T6.5 inserted mid-sprint** — reinstatement gate `==` → `>=` fix,
      a bug in already-shipped S3.T1 code. Cite the fix commit and S3.T6.5's
      `PROGRESS.md` entry.
   2. **T8/T9 chaos-test assembly duplication** — external `chaos_test`
      package duplicates Sprint 3 assembly; the "Sprint 4 `Run(ctx, cfg)`
      seam will converge this" pointer is a known debt. Cite the T8 and T9
      review notes and both ticket entries.
   3. **Half-Open scan-promotion log gap** — Half-Open promotion won by a
      `Registry.Selectable()` scan is never logged. Permanent gap ADR-0013
      documents; retro records it here rather than in the handoff so its
      "known and accepted" status is unambiguous.
   4. **T10/T11/T12/T13 mid-sprint scope additions** — the four demo-stack
      tickets themselves were scope additions. Paragraph explicitly names
      the precedent: *"mid-sprint scope additions require a `MILESTONES.md`
      amendment commit before any code lands, per S3.T10–T13's
      amendment-first ordering (ticket 01 of this bundle)."*
   5. **T9's three owner-approved spec-to-implementation conflicts** —
      adapted in tests rather than in production code. Cite the T9 review
      summary.
3. **ADR sweep** — an audit-only table. Rows: every Sprint 3 ADR (ADR-0004
   through ADR-0014, plus the ADR-0005 amendment from ticket 05 and the
   ADR-0013 decision 18 amendment from ticket 06). Columns: *ADR / has
   corresponding code? / code matches ADR text?* Any gap is a **blocker** —
   this ticket cannot mark DONE until the gap is closed or explicitly
   deferred with justification recorded in this same section. The table
   also verifies the inverse: for each Sprint 3 non-trivial decision (from
   this bundle's spec D-list and each earlier bundle spec's D-list), an ADR
   or `AGENTS.md` reference exists.
4. **Architectural gaps left open** — items that ADR-0013 and the review
   history acknowledge as permanent gaps or deferred by design: the
   Half-Open scan-promotion log gap; the deployment-target ADR still
   deferred per ADR-0005; the two manual-verification-pending shell smokes
   (`eviction.sh`, `circuit.sh`).
5. **Sprint 4 handoff** — enumerated debt inventory Sprint 4 inherits. Read
   as the starting inventory for Sprint 4's kickoff:
   - The `Run(ctx, cfg)` seam refactor and the chaos-test-assembly
     convergence path it unlocks.
   - The Half-Open scan-promotion log gap (pinned at two test layers per
     ADR-0013 decision 13).
   - The deployment-target ADR still deferred per ADR-0005.
   - The two manual-verification-pending shell smokes.
   - Any test-coverage gaps surfaced by the ADR sweep above.

**`docs/architecture.md`** — additive-only update. Sprint 1/2 sections stay
untouched. New/refreshed sections cover the six Sprint 3 subsystems:

1. Active health checking (per-backend goroutine, N-consecutive gate,
   reinstatement gate at `>=` per S3.T6.5).
2. Passive outlier detection (sliding window, ejection threshold,
   ejected-guard-driven single log-and-gauge per transition).
3. Circuit breaker (three-state per-backend, CAS-based snapshot,
   Registry-mediated admission per ADR-0012).
4. Observability pipeline (push-only leaf collector, private Prometheus
   registry, edge-triggered shared signals per ADR-0013).
5. Health endpoint (three probe paths, structured JSON, third listener
   mirroring `metricsSrv` per ADR-0014).
6. Container demo stack (LB Dockerfile, root compose, additive to two
   existing composes per ADR-0013 decision 18).

Cross-references to ADR-0011 through ADR-0014 and to the two amendments.

**Blocked by:** 03, 05, 06. Tickets 02 and 04 are transitive predecessors
(they gate 03 and 05 respectively) and are not listed here to keep the
direct-blocker list minimal.

**Status:** done

- [x] `docs/sprint-3-retro.md` exists with the five sections in the order
      above.
- [x] The Deliverables Shipped section covers every task ID from S3.T0.1
      through S3.T9 plus S3.T6.5, and the four new tickets (T10, T11, T12,
      T13), each with a one-line summary and a commit or PR link.
- [x] The Deviations section contains exactly the five paragraphs listed
      above. Item 4 explicitly names the amendment-first precedent using
      language a future retro can cite verbatim.
- [x] The ADR-sweep table lists every Sprint 3 ADR (0004–0014) plus the
      two amendments; every row is either "code exists and matches" or
      carries an explicitly justified deferral. No blank cells; no
      undeferred gaps.
- [x] The ADR-sweep table also verifies inverse coverage: every non-trivial
      Sprint 3 decision (this bundle's D-list plus each earlier bundle's
      D-list from `.scratch/s3-*/spec.md`) is either backed by an ADR or
      by a documented `AGENTS.md` or inline-comment reference.
- [x] The Architectural Gaps section names the Half-Open scan-promotion
      log gap, the deferred deployment-target ADR, and the two manual-
      verification-pending shell smokes.
- [x] The Sprint 4 Handoff section enumerates every item in the
      "Handoff" bullet list of the spec, worded so Sprint 4's kickoff
      prompt can quote it verbatim as its starting debt inventory.
- [x] `docs/architecture.md` gains six new/refreshed sections covering
      the Sprint 3 subsystems above, with cross-references to ADR-0011
      through ADR-0014 and the two amendments.
- [x] `docs/architecture.md`'s Sprint 1 and Sprint 2 sections are unchanged
      by this ticket (a `git diff` restricted to those sections shows no
      hunks).
- [x] No new ADR is created by this ticket.
- [x] No code files touched.
- [x] `PROGRESS.md` gets the standard closeout: this ticket marked `[DONE]`,
      Sprint 3 declared complete, links back to spec decisions D39–D44 for
      provenance.
