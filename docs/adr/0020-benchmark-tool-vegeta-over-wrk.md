# ADR-0020: Benchmark tool: vegeta over wrk

**Status:** Accepted

## Context

The project publishes reproducible performance numbers, including an HTTP/1.1 versus HTTP/2 comparison. The harness (`bench/run.sh`) must give throughput ceilings and latency distributions, over plain HTTP/1.1 and over TLS+ALPN HTTP/2, inside compose, from a clean checkout.

## Decision

**vegeta is the only load generator. wrk is not used, and no wrk numbers are published.**

The harness runs vegeta inside compose (`docker compose run vegeta`) for every attack:

- peak-throughput discovery by binary search on the rate,
- fixed-rate latency profiling at a fraction of the discovered peak,
- the failure-mode scenarios.

Two rules come with it:

- **Constrained Nginx match, not "Nginx at its best".** The comparison keeps algorithm, backend count, topology and keepalive pool equal. A gap is then due to the implementation, not to tuning.
- **In-compose runs, never a host binary.** The LB and Nginx see the same container-to-container network, so host networking differences cancel out.

## Consequences

- One tool covers the whole matrix. Discovery, latency and failure runs share one target format, one result encoding (gob) and one report pipeline. `bench/run.sh` has one attack path and one parser.
- vegeta negotiates HTTP/2 through ALPN on an `https://` target, so HTTP/2 needs no second tool.
- Constant-rate attacks avoid coordinated omission. Latency is measured from the planned send time, so a saturated run shows a rising p99, not a false flat line.
- vegeta writes HDR histograms (`vegeta report -type=hdrplot`), which are the `.hdr` files in the results.
- wrk is more familiar to reviewers. The reason for the change is HTTP/2: wrk does not support it.
- vegeta has no warmup discard. Each step runs `WARMUP_SECS` longer, and the harness drops the first results by timestamp (`attack_filtered` in `bench/run.sh`).
- Rejected alternatives:
  - wrk alone (no HTTP/2).
  - wrk for HTTP/1.1 plus vegeta for HTTP/2 (a protocol difference could then be a tool difference, because wrk is closed-loop and vegeta is open-loop).
  - A custom Go generator (reimplements pooling, rate scheduling and HDR histograms).
  - `hey`, `bombardier` or `k6` (the same single-tool and HTTP/2 reasons, and vegeta's constant-rate model and gob stream fit the harness).
