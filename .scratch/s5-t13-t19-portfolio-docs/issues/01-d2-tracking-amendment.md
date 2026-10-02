# 01: S5.D2 — Tracking amendment: promote the portfolio-documentation tickets

**What to build:** the project's tracking files record the approved portfolio-documentation bundle, so a reader of the progress file and the milestones file can see every ticket, its blocking edges, and what stays proposed. Docs-only, no code (TDD-exempt per the project protocol).

Spec: `../spec.md` — *Scope and numbering*.

**Blocked by:** None (can start immediately, after the bundle's spec commit).

**Status:** ready-for-agent

## In scope / out of scope (AGENTS.md Step 2.5)

- **In scope:** the edits below to the progress file and the milestones file, and the matching edit to the S5.T17.2 issue file.
- **Out of scope:** writing any deliverable document; fixing the "Time to detection" column (only proposed here); promoting S5.T18 or any other proposal; any ADR (no new design decision is made).

## Exact wording

### Milestones file — Sprint 5 deliverables

Replace the three bullets that read "README with architecture diagram…", "`docs/design-decisions.md` — rationale-heavy doc…" and "`docs/what-id-do-differently.md` — honest retrospective." with:

```
- S5.T13 — `README.md` rewrite (three-sentence summary, Mermaid request-path diagram, quickstart, local live-demo section with a marked video slot and ADR-0023 for why there is no public URL, headline results limited to owner-approved claims) and `docs/architecture.md` diagrams (request path; package graph derived from `go list`).
- S5.T14 — `docs/design-decisions.md`: seven topics (`net/http/httputil` over frameworks, bounded-loads consistent hashing, P2C-EWMA including its no-decay limitation, reload architecture, failure-mode interaction, honest Nginx comparison, concurrency model), each problem → options → choice → cost, each linking its ADRs and evidence.
- S5.T15 — `docs/what-id-do-differently.md`: honest retrospective, every item citing a source, with marked slots for owner-written items.
- S5.T19 — final portfolio pass: S5.T19.1.1 safe fixes and report-first scans, S5.T19.1.2 release verification (including a fresh-clone quickstart run), and S5.T19.2 the annotated `v0.1.0` tag, gated on the recorded demo video. No history rewrite.
- Reader-facing documents (S5.T13–T15) follow the writing rules in the bundle spec.
```

### Milestones file — Sprint 5 exit criteria

Keep the existing README and design-decisions criteria. Append:

```
- The repository is tagged `v0.1.0` (annotated) only after the hygiene and release-verification tickets pass and the README links a real recorded demo video.
```

### Progress file — Proposed tickets

Strike the existing "S5.T13–T15 (proposed 2026-09-30, not approved)" bullet and append:

```
**Promoted by S5.D2 (2026-10-02): approved by the owner** as S5.T13 (README and diagrams), S5.T14 (`docs/design-decisions.md`), S5.T15 (`docs/what-id-do-differently.md`), with S5.T19 added; see *Sprint 5 — Portfolio Documentation*.
```

Add one proposed bullet:

```
- **Relabel the "Time to detection" column (proposed 2026-10-02 by the S5.T13–T19 grilling, not approved)**: the failure scenario's `time_to_detection_s` is computed as the last failed-request offset minus the first, i.e. the width of the error window (9 ms in the published run), not the delay between the failure and its detection (about 0.7 s in the same run: stop at 29 s, first error at 29.734 s). The label misleads a reader. A fix touches the benchmark script and the results generator and regenerates `RESULTS.md`, so it needs its own ticket and a regeneration check. Until then the README says, where it links `RESULTS.md`, that the column measures the error-window width.
```

S5.T18 (P2C-EWMA gauge), the cold-start guard, the degraded-scenario leak, the probe-counting item and the narrative-checker item stay proposed and unchanged.

### Progress file — new section after "Sprint 5 — Local Live Demo (S5.T16–T17)"

```
## Sprint 5 — Portfolio Documentation (S5.T13–T15, S5.T19)

Scoped in `.scratch/s5-t13-t19-portfolio-docs/` as one bundle spec plus ten tickets. The bundle's `spec.md` is the authoritative scope boundary, including the approved headline-claims list (C1–C7) and the writing rules. Decided in the S5.T13–T19 grilling (2026-10-02). The owner's pre-grilling IDs (S5.T18–T21) were renumbered because S5.T13–T15 already existed as proposals and S5.T18 is the proposed P2C gauge. Order: S5.D2 → S5.T14.1 → S5.T14.2 → S5.T14.3 → S5.T15 → S5.T13.2 → S5.T19.1.1 → S5.T19.1.2 → S5.T19.2. S5.T13.1 (diagrams) is blocked only by S5.D2 and runs in parallel; S5.T13.2 waits for it. S5.T19.2 is also blocked by S5.T17.2 (owner-recorded).

- [TODO] S5.D2 — Tracking amendment
  - Spec: `.scratch/s5-t13-t19-portfolio-docs/issues/01-d2-tracking-amendment.md`
  - Depends: none
  - Acceptance: as in the issue file.

- [TODO] S5.T14.1 — Evidence dossier for the design document
  - Spec: `.scratch/s5-t13-t19-portfolio-docs/issues/02-t14-1-evidence-dossier.md`
  - Depends: S5.D2
  - Acceptance: as in the issue file.

- [TODO] S5.T14.2 — `docs/design-decisions.md`, topics 1–4
  - Spec: `.scratch/s5-t13-t19-portfolio-docs/issues/03-t14-2-design-decisions-1-4.md`
  - Depends: S5.T14.1
  - Acceptance: as in the issue file.

- [TODO] S5.T14.3 — `docs/design-decisions.md`, topics 5–7 and close-out
  - Spec: `.scratch/s5-t13-t19-portfolio-docs/issues/04-t14-3-design-decisions-5-7.md`
  - Depends: S5.T14.2
  - Acceptance: as in the issue file.

- [TODO] S5.T15 — `docs/what-id-do-differently.md`
  - Spec: `.scratch/s5-t13-t19-portfolio-docs/issues/05-t15-what-id-do-differently.md`
  - Depends: S5.T14.3
  - Acceptance: as in the issue file.

- [TODO] S5.T13.1 — Architecture diagrams
  - Spec: `.scratch/s5-t13-t19-portfolio-docs/issues/06-t13-1-architecture-diagrams.md`
  - Depends: S5.D2
  - Acceptance: as in the issue file.

- [TODO] S5.T13.2 — README rewrite
  - Spec: `.scratch/s5-t13-t19-portfolio-docs/issues/07-t13-2-readme.md`
  - Depends: S5.T15, S5.T13.1
  - Acceptance: as in the issue file.

- [TODO] S5.T19.1.1 — Safe fixes and report-first scans
  - Spec: `.scratch/s5-t13-t19-portfolio-docs/issues/08-t19-1-1-fixes-and-scans.md`
  - Depends: S5.T13.2
  - Acceptance: as in the issue file.

- [TODO] S5.T19.1.2 — Release verification
  - Spec: `.scratch/s5-t13-t19-portfolio-docs/issues/09-t19-1-2-release-verification.md`
  - Depends: S5.T19.1.1
  - Acceptance: as in the issue file.

- [TODO] S5.T19.2 — Tag `v0.1.0` (ready-for-human at the confirmation step)
  - Spec: `.scratch/s5-t13-t19-portfolio-docs/issues/10-t19-2-tag-v0-1-0.md`
  - Depends: S5.T19.1.2, S5.T17.2
  - Acceptance: as in the issue file.
```

### Progress file and S5.T17.2 issue file

Add to the S5.T17.2 *Acceptance* line: `; fills the video slot in the README (S5.T13.2 leaves a marked slot)`. In the S5.T17.2 issue file, replace its last bullet ("Linked from the demo README (and from the README once S5.T13 lands).") with "Linked from the demo README, and placed in the README's marked video slot."

## Acceptance criteria

- [ ] Milestones deliverables and exit criteria amended exactly as above.
- [ ] Progress file: S5.T13–T15 struck through with the promotion note; the "Time to detection" proposal added; the new section added with ten entries and the blocking edges stated above; S5.T18 and the other proposals untouched.
- [ ] S5.T17.2's acceptance line and its issue file mention the README video slot.
- [ ] Every issue-file path named in the progress entries exists.
- [ ] No other file changes. Recorded as docs-only (TDD-exempt).
- [ ] The claim commit (`chore(progress): start S5.D2`) and the work commit are each shown to the owner for approval before they are made.
