# ADR-0021: Bounded memory in the benchmark harness

**Status:** Amended by ADR-0022

## Context

Anyone must be able to reproduce the published numbers with `make bench-repro` from a clean checkout. The target is a normal laptop: Docker Desktop defaults, eight Docker vCPUs, 8–16 GB of host memory.

Early full runs failed twice in the same place. `make bench-repro` exited with 137 at `h2/<algo>/1mb/lb peak-search`. The Docker VM kernel log showed the cause:

```
Out of memory: Killed process ... (vegeta) total-vm:7416484kB anon-rss:5517476kB
oom-kill: ... task=vegeta
```

The `vegeta` container's Go heap grew to about 5 GB in an 8.2 GB VM. The VM's OOM killer stopped it, and the harness's `set -e` aborted the run. Results did not cause this, because `vegeta attack` already writes gob output to a file. The cause was the Go heap. Copying 1 MB response bodies gives a high allocation rate. The container is pinned by `cpuset` to two cores (`6-7`), but the Go scheduler and GC saw all eight VM CPUs. With no heap bound, the collector fell behind.

## Decision

**The `vegeta` container runs with a bounded Go heap, and that bound is part of the published method.** In `bench/docker-compose.yml` the `vegeta` service sets:

- `GOMAXPROCS=2`, the size of its `6-7` cpuset, so the scheduler and GC match the cores it has.
- `GOMEMLIMIT=3GiB`, a soft heap ceiling.
- `GOGC=50`, so the collector reclaims memory earlier.

The Docker VM memory is not raised. The harness must run within Docker Desktop defaults, and `bench/README.md` documents the memory ceiling.

ADR-0022 keeps this heap cap as a defensive measure. The heap cap alone moved the OOM from vegeta to the load balancer. The main fix is ADR-0022's worker bound, delivery guard and per-size seed rates.

## Consequences

- With ADR-0022, the 1 MB runs finish without OOM on standard hardware, so the benchmark stays reproducible.
- `GOMAXPROCS` aligned to the cpuset makes the generator's CPU use match its cores.
- A tighter GC costs some generator throughput. The same vegeta container drives both the LB and Nginx, so the cost does not bias the comparison. The method section states it.
- No Go code changes, and no change to the 71-run matrix, the thresholds or the result format.
- Rejected alternatives:
  - More VM memory (not reproducible without a 32 GB+ host, and it may measure host paging).
  - A smaller 1 MB matrix or lower rates (changes the published comparison and treats the symptom).
  - A custom bounded generator (see ADR-0020).
