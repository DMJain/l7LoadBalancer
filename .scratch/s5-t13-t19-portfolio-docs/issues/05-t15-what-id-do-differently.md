# 05: S5.T15 — `docs/what-id-do-differently.md`

**What to build:** an honest list of the project's weaknesses and what the project would do instead, each item tied to a source a reviewer can check, so a reviewer sees real self-assessment and not a polished surface.

Spec: `../spec.md` — *Writing rules*, *Retrospective*.

**Blocked by:** 04 (S5.T14.3).

**Status:** ready-for-agent

## In scope / out of scope (AGENTS.md Step 2.5)

- **In scope:** the single retrospective document.
- **Out of scope:** fixing any listed weakness (each is already a proposal or recorded); items in the owner's own voice (left as marked slots); new measurements; rewriting the earlier sprint retros; a recommendation to rewrite history.

## Acceptance criteria

- [ ] Each item has four parts: what happened, what it cost, what the project would do instead, and a source (an ADR, a tracking entry, a retro, a results section, or a commit hash).
- [ ] Required items are all present, each with a source: no-decay EWMA; no cold-start guard for the first peak-search step; degraded-scenario leak with cause marked UNVERIFIED and no numbers; rig-limited benchmarking; stale P2C state after a load-balancer switch; the "Time to detection" column measuring the error-window width; health probes counted as backend arrivals; the narrative number check missing tables at section boundaries; the attribution trailers.
- [ ] The trailer item states facts only: seven commits (`25c1952`, `fd5ce03`, `9c25a11`, `c7de908`, `84b7dab`, `7939ade`, `cf68104`), all ancestors of the published-benchmark commit `8c2b7d4`, all already pushed, and the commit-message hook deferred by project policy. It recommends no rewrite.
- [ ] Owner-voice items are left as empty marked slots, at least one at the end of the document. Nothing is written as the owner's opinion.
- [ ] Neutral voice ("the project"); no first person.
- [ ] Every number traces to an approved claim; no new figures; the degraded scenario appears without numbers.
- [ ] Writing rules hold: each term defined at first use in this document (EWMA, cold start, rig-limited, peak, health probe, and any others used); no ticket IDs (items cite the behaviour with an ADR or tracking-entry link); no unexplained "slice", "cell", "core slice" or "bisect"; no marketing words; the audience is never announced.
- [ ] Every ADR, commit and results-section citation resolves.
- [ ] The reader check is done and logged: every term that needed a definition and where it is defined; a term used before its definition fails.
- [ ] Docs-only (TDD-exempt). The claim commit and the work commit are each shown to the owner for approval first.
