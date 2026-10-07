# ADR-0022: Honest benchmarking under generator limits

**Status:** Accepted. Amends ADR-0021.

## Context

The load generator (`vegeta`, ADR-0020) runs in a container beside the load balancer and Nginx. On a Docker-on-laptop rig it shares CPU and memory with the system under test. At large payload sizes the generator can reach its own ceiling before the load balancer does. A harness that does not notice this reports the generator's ceiling as the LB's peak.

The 1 MB peak search showed this twice.

- vegeta's `-max-workers` is unbounded by default. At a 1000 req/s target on the slow 1 MB path, it opened thousands of concurrent requests. The LB's per-stream buffers grew with them, and vegeta plus the LB used more than the 8 GB VM. A heap cap on vegeta alone (ADR-0021) only moved the OOM to the LB.
- A concurrency cap fixed the memory (vegeta 203 MiB, LB 82 MiB). But vegeta then sent much less than asked: 3,745 requests in 20 s against a 1000/s target, about 187/s. The p99 looked low (27 ms), and the search would have called 1000/s sustainable.

The root cause: the 1000 req/s seed rate is far above the rig's 1 MB capacity (about 200 req/s). Without a delivery check, a capped generator can make a false peak at any size.

## Decision

**Bound the generator, check what it really delivered, and seed each payload size from the rig's bytes-per-second ceiling.** Three mechanisms in `bench/run.sh` and `bench/docker-compose.yml` work together:

- **Worker and connection bound.** Every `vegeta attack` runs with `-max-workers` and `-max-connections` of 1024 (`VEGETA_MAX_WORKERS`, `VEGETA_MAX_CONNECTIONS`). The LB never gets unbounded in-flight requests. The vegeta container keeps the ADR-0021 heap cap (`GOMAXPROCS=2`, `GOMEMLIMIT=3GiB`, `GOGC=50`).
- **Delivery guard.** A peak-search step counts as sustained only if it passes the p99 and error limits **and** delivers at least `DELIVERY_MIN_RATIO_PCT` (95%) of its target rate. Delivered rate is the request count over the measured window. A step that under-delivers fails the search, and the result reads `under-delivered; harness-limited; not a peak`. A real peak carries `peak_status=sustained`.
- **Per-size seed rates.** The generator's ceiling depends on bytes per second, so each size has its own search settings:

  | Size | Seed | Cap | Granularity |
  |---|---|---|---|
  | 200 B, 10 KB | 1000 req/s | 200000 req/s | 500 req/s |
  | 1 MB | 100 req/s | 500 req/s | 25 req/s |

## Consequences

- The published numbers measure the load balancer, not the generator. A step the generator could not drive fails the search, at any size or rate, also on a faster host.
- The full matrix completes on Docker Desktop defaults on an 8–16 GB laptop, so `make bench-repro` stays reproducible. The VM memory is not raised.
- "No sustainable rate on this rig" is a valid, publishable result.
- A scenario the rig cannot drive is reported without a number. The 1 MB cells may be empty or lower than on a large host. The results document says so.
- The seed table encodes the rig's capacity. A much faster host would need a higher 1 MB seed to avoid a long search. It is a constant, changed on purpose.
- No load-balancer code changes. The result format gains a `peak_status=` field.
- Rejected alternatives:
  - Dropping 1 MB from the matrix (hides the rig's limit).
  - More VM memory (not reproducible).
  - Per-size seeds without the delivery guard (a future size or rate could again give a false peak).
  - A worker cap without the delivery guard (measured: p99 27 ms at a 1000/s target that delivered 187/s).
