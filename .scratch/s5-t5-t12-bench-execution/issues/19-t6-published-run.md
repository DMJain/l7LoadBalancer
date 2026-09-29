# 19: Published run (S5.T6, absorbs S5.T7)

**What to build:** The one benchmark run whose numbers get published: produced by `make bench-repro`, from a fresh clone, on the final harness, and committed as raw output only. It evidences the Sprint 5 exit criterion by performing it. Peak-throughput discovery (formerly S5.T6) and the fixed-rate latency sweep (formerly S5.T7) run together here, because the sweep's rates are fractions of the peak found in the same invocation. No code changes.

**Blocked by:** 15 (the harness is final)

**Status:** ready-for-agent

Spec: `../spec.md` (Published run)

- [ ] It runs from a fresh `git clone` of a commit where tickets 02–15 are done. `make bench-repro` exits 0, preflight and smoke included.
- [ ] Expect about 2–2.5 hours on Docker Desktop. The run is not interrupted, and the progress lines show it's alive.
- [ ] Output: 71 result sets (core 48, protocol 12, failure 3, degraded 8) plus a provenance record with `git_dirty: false`.
- [ ] The commit contains **only** the raw results directory plus the `PROGRESS.md` close-out. No tables, no narrative, no hand edits. The provenance `git_sha` equals the parent of the results commit. The results are brought back from the clone by fetch or equivalent, never retyped.
- [ ] The `PROGRESS.md` entry records the observed wall-clock time, from the provenance record, and the reload verdicts.
- [ ] If the numbers look noisy or implausibly low, say so in the entry and stop. Moving to a Linux benchmark host is the owner's decision and outside this ticket.
