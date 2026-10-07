# 07: S5.T20.6 — Cold-start guard, then full-matrix re-run

**What to build:** fix the cold start that skipped round-robin 200 B and 10 KiB through the LB, then re-run the whole matrix so every published number traces to one clean commit.

Spec: `../spec.md` — D3; `PROGRESS.md` proposal "Cold-start guard for the first peak-search step" (2026-10-02).

**Blocked by:** 06.

**Status:** ready-for-agent (the re-run itself takes about half a day of machine time)

- [ ] The cold-start guard in `bench/run.sh` per the proposal, with its test in `bench/` if the harness has one for that path.
- [ ] `make bench-repro` from a clean checkout of the commit that contains the guard; `bench/results/provenance.json` shows `git_dirty: false`.
- [ ] `bench/generate-results.sh` regenerates `RESULTS.md`; every prose figure re-checked; README headline numbers updated if they changed.
- [ ] The benchmark summary page is refreshed from the new run and has no "—".
- [ ] Verification (D9): dry-run row 9 with no "—".
