# ADR-0022: Honest benchmarking under generator limits

- **Status**: Accepted
- **Date**: 2026-10-01
- **Deciders**: Darshan Jain (project owner) + opencode agent (S5.T6.1, owner-approved during the S5.T6 published run)
- **Amends**: ADR-0021 (the defensive heap cap is retained; the worker bound, delivery guard and per-size seeds are the primary mechanism)

## Context

Sprint 5's benchmark is produced by a load generator (`vegeta`, ADR-0020) running
in a container beside the load balancer and Nginx. On a Docker-on-laptop rig the
generator shares the machine's CPU and memory with the system under test, so at
large payload sizes the **generator's** throughput ceiling can be reached before
the load balancer's. A harness that does not notice this reports the generator's
ceiling as the LB's peak — a measurement of the rig, published as a measurement
of the load balancer.

The published run (S5.T6) hit this twice. The 1 MB peak-search OOM-killed the
Docker VM: `vegeta`'s `-max-workers` defaults to *unbounded*, so at a 1000 req/s
target against the slow 1 MB path it spawned thousands of concurrent in-flight
requests, the LB's per-stream buffers grew with them, and vegeta + LB together
exceeded the 8 GB VM. Bounding vegeta's Go heap alone (ADR-0021) just moved the
victim to the LB. A concurrency cap fixed the memory (vegeta 203 MiB, LB 82 MiB)
but made vegeta **silently under-send** — 3,745 requests in 20 s against a 1000/s
target — so p99 looked low and the search would have called 1000/s sustainable
when the generator had only delivered ~187/s. The root cause is that the seed
rate itself (1000 req/s) is above the rig's 1 MB capacity (~200 req/s), and
without a delivery check a capped generator can manufacture a false peak at any
size.

## Decision

**Bound the generator, verify what it actually delivered, and start each payload
size from a seed calibrated to the rig's bytes-per-second ceiling.**

Three coupled mechanisms in `bench/run.sh` and `bench/docker-compose.yml`:

- **Worker/connection bound.** Every `vegeta attack` runs with `-max-workers`
  and `-max-connections` (both 1024, `VEGETA_MAX_WORKERS` /
  `VEGETA_MAX_CONNECTIONS`), so the LB is never flooded with unbounded in-flight
  requests. The `vegeta` container additionally keeps the defensive Go heap cap
  from ADR-0021 (`GOMAXPROCS=2`, `GOMEMLIMIT=3GiB`, `GOGC=50`).
- **Delivery guard.** A peak-search step counts as *sustained* only if it
  cleared the p99 and error ceilings **and** delivered at least
  `DELIVERY_MIN_RATIO_PCT` (95%) of its target rate, measured as the request
  count over the measured window. An under-delivered step fails the search, and
  the scenario is reported as `under-delivered; harness-limited; not a peak`
  rather than a bare skip. A real peak carries `peak_status=sustained`.
- **Per-size seed rates.** The search seeds per payload size because the
  generator's ceiling is bytes-per-second-bounded: 200 B and 10 KB seed at 1000
  req/s (cap 200000), 1 MB seeds at 100 req/s (cap 500, granularity 25).

## Consequences

- Positive: the published numbers measure the load balancer, not the generator.
  A step that the generator could not actually drive fails the search instead of
  passing on artificially low latency, at **any** size or rate — including
  future sizes and a faster host.
- Positive: the full matrix completes on Docker Desktop defaults on a standard
  8–16 GB laptop, so `make bench-repro` stays reproducible (the Sprint 5 exit
  criterion). The Docker VM's memory is not raised.
- Positive: "no sustainable rate on this rig" is a valid, publishable result.
  The harness can now say the rig is the limit without inventing a peak.
- Negative: a scenario the rig cannot drive is reported as skipped rather than
  with a number; the 1 MB LB/nginx cells may be empty or lower than a
  large-memory host would show. That is the honest reading, and the results
  document states it.
- Negative: the per-size seed table is a second place the rig's capacity is
  encoded; a much faster host would need the 1 MB seed raised to avoid an
  unnecessarily long search. It is a constant, changed deliberately, not a flag.
- Neutral: no load-balancer code changes, no matrix or threshold change beyond
  the per-size seed, and the result format only gains a `peak_status=` field.

## Alternatives considered

- **Drop 1 MB from the matrix.** Rejected. It removes a payload class for an
  operational reason (this rig's memory), not a technical one, and hides the
  rig's limit instead of reporting it.
- **Raise the Docker VM memory.** Rejected. It makes the harness unreproducible
  for anyone without a 32 GB+ host and risks measuring host thrashing rather than
  load balancer performance.
- **Per-size seed alone, without the delivery guard.** Rejected. It would fix
  today's 1 MB case and silently under-report any future size+rate combination
  that exceeds the rig, which is the same class of dishonest number this ADR
  exists to prevent.
- **Cap workers alone, without the delivery guard.** Rejected: a capped
  generator under-sends and reports a false peak (measured: p99 27 ms at a
  1000/s target that delivered 187/s).
