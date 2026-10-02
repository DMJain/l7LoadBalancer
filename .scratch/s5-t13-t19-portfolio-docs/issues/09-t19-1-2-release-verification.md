# 09: S5.T19.1.2 — Release verification

**What to build:** a pass or fail record showing the repository builds, tests, reads and runs as its README says, from a fresh clone.

Spec: `../spec.md` — *Hygiene pass: full checklist* (items 1–4, 10–12, 18–19).

**Blocked by:** 08 (S5.T19.1.1).

**Status:** ready-for-agent

## In scope / out of scope (AGENTS.md Step 2.5)

- **In scope:** the checks below and a pass or fail record for each.
- **Out of scope:** re-running the 4 h 16 min benchmark reproduction; fixing a failure beyond a trivial text fix (a larger failure becomes a proposed ticket and stops the pass); filling the owner's slots; pushing anything.

## Acceptance criteria

- [ ] **Tree and build:** the working tree is clean apart from this ticket's own changes; `go build ./...` and `go vet ./...` are clean; formatting produces no diff; `go mod tidy` produces no diff. Output recorded.
- [ ] **Tests:** the full test run and the race-detector run are green. Output recorded.
- [ ] **Placeholder scan** over the shipped docs: every TODO, FIXME, "Video pending" and unresolved owner slot listed with file and line. Owner slots are reported, not filled.
- [ ] **Dead-link check** over the README and the docs: every relative link and every ADR link resolves. The checking script lives in the scratchpad and is committed only if the owner approves it.
- [ ] **Number-to-claim trace:** every number in the README, the design-decisions document and the retrospective is listed with the claim (C1–C7, or an owner-approved N-claim) that approves it. A number with no claim is a finding, not silently fixed.
- [ ] **Reader-check records:** the session logs for the README, design-decisions, retrospective and architecture documents each contain a reader-check list; a missing one is a finding.
- [ ] **Benchmark provenance:** commit `8c2b7d4` resolves as a commit; the results document and the provenance record both still name it; the README states which commit the published numbers come from. The benchmark reproduction is not re-run.
- [ ] **Fresh-clone quickstart:** clone the repository into the scratchpad, then run the README's quickstart commands exactly as written, then `make demo-up`, then the demo acceptance check passing, then teardown. The benchmark stack and the demo stack share port 8080 and never run together. Output recorded, including any deviation from the README text.
- [ ] A pass or fail line for each of the 19 items in the spec's hygiene checklist (items 5–9 and 13–17 are carried from the previous ticket), with a final summary of what blocks the tag.
- [ ] Docs and config only (TDD-exempt). The claim commit and any work commit are shown to the owner for approval first.
