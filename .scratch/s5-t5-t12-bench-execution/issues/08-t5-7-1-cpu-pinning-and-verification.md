# 08: CPU pinning and run-time verification (S5.T5.7.1)

**What to build:** Each role in the bench stack runs on fixed vCPUs, and the harness proves at run time that both competitors really see two cores, refusing to measure otherwise. After this, a throughput difference between competitors can't come from one of them being starved or over-provisioned. TDD-exempt (compose and shell).

**Blocked by:** 04 (the smoke gate), 07 (the `gomaxprocs` field)

**Status:** ready-for-agent

Spec: `../spec.md` (Fairness)

- [ ] Compose cpusets:
  - LB and Nginx on cores 0–1, shared because only one competitor is under load at a time;
  - backend1–4 on cores 2, 3, 4, 5, one each;
  - vegeta on cores 6–7.
- [ ] The compose header states the split in one sentence, reused verbatim as the methodology statement: "LB and Nginx: cores 0–1; backends 1–4: cores 2–5, one each; vegeta: cores 6–7."
- [ ] The split is fixed and never scaled from the available CPU count.
- [ ] Whenever the harness (re)creates the LB, it waits for readiness, reads `gomaxprocs` from the startup line in the LB's container logs, and aborts the slice unless it is 2.
- [ ] Whenever the harness (re)creates Nginx, it first waits until Nginx serves a 200 through its listener (workers may not have forked at container start), then counts worker processes. The slice aborts unless the count is 2.
- [ ] Both aborts name the failed check and the observed value.
- [ ] The bench README documents the split and both checks.
- [ ] Verified:
  - `docker compose config` is clean;
  - `shellcheck -S style` and `bash -n` pass;
  - a green `./bench/run.sh smoke` shows both checks passing;
  - negative check: a local, uncommitted `worker_processes 4` makes the harness abort naming the Nginx worker check.

  Both results are recorded in the session log.
