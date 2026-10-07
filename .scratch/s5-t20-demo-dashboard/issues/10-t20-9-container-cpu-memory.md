# 10: S5.T20.9 — CPU and memory of the LB and backends on the demo dashboard

**What to build:** dashboard panels that show how hard the load balancer and each backend are working: CPU (cores in use) and memory, per container, live.

Spec: owner request (2026-10-08). Approach: the control service reads Docker's read-only stats endpoint for an allowlist of containers (the LB and the four backends) and serves them on `/metrics` for the demo Prometheus. A privileged cAdvisor container was tried and dropped: it needs a privileged container with the Docker VM's filesystem mounted.

**Blocked by:** 09.

**Status:** ready-for-agent

- [ ] ADR-0025 amendment: read-only Engine stats for the allowlist (backends and LBs, container names derived, never from a request); `/metrics` with `demo_container_cpu_seconds_total`, `demo_container_memory_bytes`, `demo_container_stats_up`.
- [ ] The LB container gets an explicit `container_name` (`l7loadbalancer-demo-lb`).
- [ ] Engine `stats` call with `stream=false&one-shot=true`; memory is usage minus inactive file cache (as `docker stats` shows it).
- [ ] Tests, Red-first, against the Engine stand-in: values, allowlist only, a failed read reported as `up 0` with no value series.
- [ ] Demo Prometheus scrapes the control service; the demo dashboard gains "CPU by container" and "Memory by container"; the control page's iframe fits.
- [ ] Verified live: under load the LB and backend CPU rise and both panels show data.
