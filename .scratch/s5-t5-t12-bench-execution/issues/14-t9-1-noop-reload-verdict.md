# 14: No-op reload with a pass/fail verdict (S5.T9.1)

**What to build:** The existing unchanged-config SIGHUP run becomes a **no-op reload** run, judged against criteria that are constants fixed before any run. The result file states PASS or FAIL rather than leaving a judgment call to the writeup. It measures the bare cost of the reload path under load, and it lays the verdict groundwork that the drain reload (15) builds on. TDD-exempt.

**Blocked by:** 13

**Status:** ready-for-agent

Spec: `../spec.md` (Reload runs). Glossary: **No-op reload**, **Reload**.

- [ ] A constant `RELOAD_P99_FACTOR=2` sits with the harness's other constants. The steady state is unchanged: h2, roundrobin, 10 KiB, 50% of peak, 60 s, event at T+30s.
- [ ] **PASS iff**:
  - zero non-2xx responses and zero transport errors over the whole run;
  - post-event p99 ≤ `RELOAD_P99_FACTOR` × the **pre-event p99 of the same run**, where the pre-event window runs from the end of warmup to T+30s.
- [ ] The result file carries both windows' p99, the error counts, and a `verdict=PASS|FAIL` line naming any failed criterion. The summary table shows the verdict.
- [ ] The run and its filename are renamed from the unchanged-config form to `sighup-noop`. backend-kill is untouched.
- [ ] Verified: `shellcheck -S style` and `bash -n` pass; a green smoke; a targeted `./bench/run.sh failure` shows the no-op reload PASSing. A negative check, with the factor set to 0 locally and uncommitted, shows FAIL naming the p99 criterion. Both are recorded in the session log.
