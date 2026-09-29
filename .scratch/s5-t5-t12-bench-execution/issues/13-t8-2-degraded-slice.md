# 13: `degraded` slice — static slow backend (S5.T8.2)

**What to build:** A new `degraded` slice in which one backend is 50 ms slow for the whole run. It shows which algorithms route around a degraded backend and which don't, and the headline result is the per-backend request share, not just aggregate latency. All eight runs use the same absolute rate, so their results can be compared. TDD-exempt.

**Blocked by:** 12

**Status:** ready-for-agent

Spec: `../spec.md` (Degraded slice)

- [ ] backend3 is recreated with a 50 ms injected delay. The other backends stay at 0. backend3 is restored to 0 when the slice ends, **including on failure**.
- [ ] 8 runs: {roundrobin, leastconn, consistent-hash, p2c-ewma} × {lb, nginx}, h2, 10 KiB. Each is a 30-second measurement plus the standard 5-second warmup, which is discarded.
- [ ] **One absolute rate for all 8**: 50% of the h2/roundrobin/10 KiB **LB** peak. It's taken from the same invocation's core result when one exists. When `degraded` runs alone, the peak search for that single case runs first.
- [ ] Per-backend distribution for every run, recorded as counts and shares, using ticket 12's helper. Consistent-hash runs also record the hot-key owner and whether spill occurred.
- [ ] Results go under a `degraded` results subdirectory (`.txt` + `.hdr`), with parameter-encoded names and the `comparison=` label. Summary table columns: algorithm, competitor, p50, p99, and the share for each backend.
- [ ] `degraded` is part of `all`. The header comments and README say `all` = 70 at this point.
- [ ] The README states the expected shape: p2c-ewma and leastconn shift load away from backend3; roundrobin splits evenly; the LB's consistent-hash spills if backend3 owns the hot key, and Nginx's doesn't. It also notes that health probes bypass the delay, so backend3 is never ejected.
- [ ] Verified: `shellcheck -S style` and `bash -n` pass; a green smoke; `./bench/run.sh degraded` run on its own, with durations reduced locally and uncommitted if needed (stated in the session log). The run finds its own peak, restores backend3, and shows the distribution differing by algorithm in the expected direction.
