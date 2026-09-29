# ADR-0020: Benchmark tool — vegeta over wrk

- **Status**: Accepted
- **Date**: 2026-09-29
- **Deciders**: Darshan Jain (project owner) + opencode agent (S5.T4-harness, recorded when the harness was built; scope in `.scratch/s5-t1-t4-http2-benchmarks/issues/06-benchmark-harness.md`)

## Context

Sprint 5's deliverable is published, reproducible performance numbers, including
an HTTP/1.1-vs-HTTP/2 comparison. The original `MILESTONES.md` plan named wrk
for peak-throughput discovery and vegeta for latency profiling — two tools, one
story. `docs/adr/INDEX.md` reserved this ADR number for the tool choice, and the
S5 bundle spec (decision 21) rewrote the plan to a single tool:

- **S5.T6** → vegeta peak-throughput discovery (binary-search the rate ceiling).
- **S5.T7** → vegeta fixed-rate latency profiling at a fraction of discovered
  peak.

The harness (`bench/run.sh`) is the artifact this decision has to fit. It must
produce both throughput ceilings and latency distributions, over plain HTTP/1.1
and over TLS+ALPN HTTP/2, in-compose, from a clean checkout.

## Decision

**Vegeta is the only load generator. wrk is not used, and no wrk numbers are
published.**

The harness drives vegeta in-compose (`docker compose run vegeta`) for every
attack: binary-search throughput discovery, fixed-rate latency runs, and the
failure-mode scenarios. Two supporting decisions come with it:

- **Constrained Nginx match, not "Nginx at its best".** The comparison holds
  algorithm, backend count, topology, and keepalive pool equal (spec §24–§25).
  This is a property of what the harness runs, and it is what makes a gap
  attributable to implementation rather than tuning.
- **In-compose execution, never a host binary.** Both the LB and Nginx see the
  same container-to-container networking, so host-OS networking differences
  cancel out of the comparison (spec §22).

## Consequences

- Positive: one tool covers the whole matrix. Throughput discovery, latency
  profiling, and failure-mode runs share the same target format, result encoding
  (gob), and report/HDR-histogram pipeline, so `bench/run.sh` has one attack path
  and one parser instead of two.
- Positive: HTTP/2 is covered natively. vegeta negotiates HTTP/2 via ALPN when
  pointed at an `https://` target, so the protocol slice needs no separate tool
  or build.
- Positive: constant-rate attacks avoid coordinated omission — latency is
  measured against intended send times, not the client's ability to keep up — so
  a saturated run shows rising p99 rather than a falsely flat distribution.
- Positive: HDR histograms come out natively (`vegeta report -type=hdrplot`),
  which is the `.hdr` artifact the results directory commits.
- Negative: the wrk community is larger and its numbers are more familiar to a
  reviewer; the ADR has to justify the divergence. The justification is the
  HTTP/2 requirement — wrk cannot express it.
- Negative: vegeta has no built-in warmup discard; the harness runs each step
  `WARMUP_SECS` long and drops the leading results by timestamp. That is extra
  script logic (`bench/run.sh` `attack_filtered`) wrk would not need.
- Neutral: this ADR records the tool change; it changes no Go code. It is also
  why `MILESTONES.md` now reads S5.T6 = vegeta peak-throughput and S5.T7 =
  vegeta fixed-rate latency.

## Alternatives considered

- **wrk as originally planned**: rejected — wrk does not support HTTP/2. Since
  half the benchmark story is HTTP/2 performance, wrk can only cover the
  HTTP/1.1 half, forcing a second tool for the other half and making cross-
  protocol comparison depend on two different measurement programs.
- **wrk for HTTP/1.1 and vegeta for HTTP/2 (the original two-tool plan)**:
  rejected — it reintroduces exactly the attribution problem the constrained
  match exists to remove, one level up: an HTTP/1.1-vs-HTTP/2 difference could
  be a protocol difference or a wrk-vs-vegeta difference, and the two do not
  share a latency measurement model (wrk's closed-loop vs vegeta's open-loop
  constant rate). One tool, one model.
- **A custom Go load generator inside the repo**: rejected as unnecessary
  surface — it would reimplement connection pooling, rate scheduling, and HDR
  histograms that vegeta already provides, and add a binary to build and
  maintain for no measurement the harness lacks.
- **`hey` / `bombardier` / `k6`**: rejected for the same HTTP/2-coverage and
  single-tool reasons weighed against vegeta; vegeta's constant-rate model and
  native HDR histograms are the specific properties the harness wants, and its
  results stream (gob) is easy to tee for both the live time series and the
  offline report the failure slice needs.
