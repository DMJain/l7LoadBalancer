# 06: Benchmark execution harness (`run.sh` + configs + ADR-0020)

**What to build:** `./bench/run.sh all` executes the full 50-run benchmark matrix and produces reproducible results. Peak throughput is discovered via automated binary search (doubling rate until p99 > 100ms or errors > 1%, then narrowing). Results land as `.txt` summaries and `.hdr` histogram files in `bench/results/{core,protocol,failure}/` with parameter-encoded filenames. Eight self-contained YAML benchmark configs cover all algorithm × protocol combinations. Failure-mode benchmarks demonstrate backend-kill recovery and zero-drop SIGHUP reload under load. ADR-0020 documents the vegeta-over-wrk decision.

**Blocked by:** 05 (benchmark infrastructure)

**Status:** ready-for-agent

- [ ] Eight benchmark config files in `bench/configs/{http11,h2}/{roundrobin,leastconn,consistent-hash,p2c-ewma}.yaml` — fully self-contained, no templating
- [ ] `bench/run.sh` with slices: `core`, `protocol`, `failure`, `all` (default)
- [ ] `./bench/run.sh all` satisfies Sprint 5 exit criteria ("reproduces all published numbers")
- [ ] Binary-search peak throughput: seed 1000 req/s, double until p99 > 100ms or errors > 1%, binary search between last-good and first-bad, converge within 500 req/s. Warmup (first 3–5s) discarded per step.
- [ ] Thresholds are constants at top of `run.sh`, not flags
- [ ] Core matrix (36 runs): 2 matched algorithms × 3 sizes × 2 competitors × 2 load types = 24 head-to-head, plus 2 solo algorithms × 3 sizes × 2 load types = 12
- [ ] Protocol comparison (12 runs): roundrobin × 3 sizes × 2 competitors × 2 load types × HTTP/1.1
- [ ] Failure-mode: backend-kill (roundrobin, 10KB, 50% of peak, `docker compose stop backend3` at T+30s, 60s total). Measure: time-to-detection, error count, p99 recovery, total errors.
- [ ] Failure-mode: SIGHUP-under-load (same steady-state, `kill -HUP 1` at T+30s with unchanged config, 60s total). Measure: drop count (target: zero), p99 spike.
- [ ] Results output as `.txt` + `.hdr` in `bench/results/{core,protocol,failure}/` with parameter-encoded filenames
- [ ] Each slice prints summary table to stdout: algorithm, size, competitor, p50, p99, throughput
- [ ] Script is single source of truth for benchmark parameters (rates, durations, warmup)
- [ ] ADR-0020 written: vegeta-over-wrk rationale (HTTP/2 coverage, coordinated omission avoidance, single-tool consistency)
- [ ] MILESTONES.md amended: S5.T6 → vegeta peak-throughput, S5.T7 → vegeta fixed-rate latency
