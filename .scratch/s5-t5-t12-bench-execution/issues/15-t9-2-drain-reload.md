# 15: Drain reload (S5.T9.2)

**What to build:** A **drain reload** run removes backend4 under load and proves two things: no request is dropped while it drains, and it is never selected again once the reload is applied, verified from the backend's own arrival counter. This is the real "zero drops while draining" claim. With it, the failure slice is 3 runs and `all` is 71. TDD-exempt.

**Blocked by:** 14 (verdict framework), 06 (arrival counter)

**Status:** ready-for-agent

Spec: `../spec.md` (Reload runs). Glossary: **Drain reload**, **Draining**, **Drain window**, **Removed backend**.

- [ ] For the reload runs, the LB mounts a working copy of the committed h2 roundrobin config in the results scratch area. The committed configs are never modified.
- [ ] The backend4-less config is derived from the committed file at run time. No new committed config.
- [ ] **In-place rewrite, as an acceptance criterion**: the working copy is rewritten by `cp` over the existing file (same inode), **never** by `mv` or temp-file-then-rename. A single-file bind mount keeps pointing at the original inode, so a rename would make the LB re-read the old config and the run would pass while testing nothing. The harness asserts that the inode is unchanged across the rewrite and aborts otherwise.
- [ ] At T+30s, rewrite the config and send SIGHUP. Arrival counter snapshots are taken (1) just before SIGHUP, (2) once the LB logs the reload as applied, and (3) at the end.
- [ ] **PASS iff**:
  - ticket 14's criteria hold (zero non-2xx and transport errors; the p99 factor against the run's own pre-event window);
  - backend4's snapshot 3 minus snapshot 2 = 0.

  Snapshot 2 minus snapshot 1 is recorded but not judged. A drain-window cancellation surfaces as a 502, so zero non-2xx covers it.
- [ ] The result file carries all measurements, the three snapshots, and a `verdict=PASS|FAIL` line naming any failed criterion. The summary shows the verdict.
- [ ] After the run, the LB is recreated on the committed config.
- [ ] Header comments and README state that the failure slice has 3 runs and `all` has 71.
- [ ] Verified: `shellcheck -S style` and `bash -n` pass; a green smoke; a targeted `./bench/run.sh failure` with both reload runs PASSing. A negative check, with `cp` swapped for `mv` locally and uncommitted, shows the inode assertion aborting the run. Both are recorded in the session log.
