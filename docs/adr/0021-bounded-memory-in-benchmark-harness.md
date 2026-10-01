# ADR-0021: Bounded memory in the benchmark harness

- **Status**: Accepted
- **Date**: 2026-10-01
- **Deciders**: Darshan Jain (project owner) + opencode agent (S5.T6.1, owner-approved during the S5.T6 published run)

## Context

Sprint 5's exit criterion is published numbers that a stranger can reproduce by
running `make bench-repro` from a clean checkout. The Sprint 5 bundle spec sizes
that run at ~2–2.5 h "on Docker Desktop" — a standard developer laptop, not a
dedicated benchmark host. `MILESTONES.md` states the same target: eight Docker
vCPUs, standard Docker Desktop defaults.

The first attempts at the published run (S5.T6) were killed twice, identically:
`make bench-repro` exited 137 at `h2/<algo>/1mb/lb peak-search`, runs 17–19 of
71. The Docker VM kernel log shows the cause:

```
Out of memory: Killed process ... (vegeta) total-vm:7416484kB anon-rss:5517476kB
oom-kill: ... task=vegeta
```

The victim is the one-off `vegeta` container. Its Go heap climbs to ~5 GB
against the VM's 8.2 GB total, so the VM's global OOM killer fires and the
`docker compose run vegeta` child dies; the harness's `set -e` then aborts.

The cause is not result accumulation — `vegeta attack` already redirects its
gob output straight to a file (`bench/run.sh`). It is the Go runtime's heap: a
high allocation rate from copying 1 MB response bodies, on a container pinned by
`cpuset` to two cores (`6-7`) while the Go scheduler and GC still see the VM's
eight CPUs. With no heap bound, the collector cannot keep up and the heap
balloons until the VM OOMs.

The harness is the reproducibility artifact. It must fit the default
environment it advertises; a benchmark that only completes on an oversized
Docker VM is not reproducible.

## Decision

**The `vegeta` load-generator container runs under a bounded Go heap, and that
bound is part of the published methodology.**

In `bench/docker-compose.yml`, the `vegeta` service gains:

- `GOMEMLIMIT` — a soft heap ceiling sized so the process stays under ~4 GB.
- `GOGC` — a lower GC target, so the collector reclaims earlier rather than
  letting the heap ride to the limit.
- `GOMAXPROCS` — set to the `6-7` cpuset size, aligning the scheduler and GC with
  the cores actually available.

If bounding the heap proves insufficient, the fallback is to stream encoded
results to disk rather than hold them in memory. In practice the attack already
streams to a file, so the heap bound is the fix.

The host's Docker VM memory is **not** raised. The harness must run within
Docker Desktop's defaults on an 8–16 GB laptop, and `bench/README.md` documents
the memory ceiling and that target.

## Consequences

- Positive: the 1 MB throughput runs complete without OOM, so `make bench-repro`
  satisfies the Sprint 5 exit criterion on standard hardware, and the
  "reproducible benchmark" claim holds for anyone who clones the repo.
- Positive: `GOMAXPROCS` aligned to the cpuset removes the scheduler/GC mismatch
  that let the heap grow unbounded, which also makes the generator's CPU usage
  match the cores it was allocated.
- Negative: a tighter GC (`GOGC`/`GOMEMLIMIT`) trades some generator throughput
  for memory. The published numbers are measured under that trade, so the
  methodology section states it. The ceiling applies equally to the load
  balancer and to Nginx — both are driven by the same vegeta container — so it
  does not bias the head-to-head comparison.
- Neutral: no Go code changes, and no change to the 71-run matrix, the
  thresholds, or the result format. This ADR records a compose-level resource
  bound only.

## Alternatives considered

- **Raise the Docker Desktop VM memory.** Rejected. It makes the benchmark
  unreproducible for anyone without a 32 GB+ host, and it risks measuring host
  thrashing — the VM paging under a bloated generator — rather than load
  balancer performance. The published run should fit the environment the README
  tells the reader to use.
- **Reduce the 1 MB matrix or the attack rates.** Rejected. It changes the
  published comparison and the response-size matrix the spec fixes, and it
  treats the symptom (large payloads) rather than the cause (an unbounded
  generator heap).
- **A custom, deliberately bounded Go load generator.** Rejected on the
  ADR-0020 grounds: it reimplements connection pooling, rate scheduling, and
  HDR histograms that vegeta provides, for no measurement the harness lacks.
  Binding vegeta's own heap is the smaller change.
