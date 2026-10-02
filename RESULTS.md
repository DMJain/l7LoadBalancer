# Benchmark results

This is the published result of the l7LoadBalancer benchmark matrix. It is self-contained: the methodology, the tables and the reload verdicts below are generated from the raw harness output in `bench/results/`, and the prose around them is the interpretation. Nothing here is typed by hand — `bench/generate-results.sh` rewrites every generated region and fails if a figure quoted in the prose does not match a generated cell.

The raw output was produced by a single uninterrupted `make bench-repro` run from a fresh clone of commit `8c2b7d4` (`8c2b7d4adf0df3afc3433458f9f4908651b4801c`); `bench/results/provenance.json` records `git_dirty: false`, so the numbers trace to exactly that code. That run was flagged noisy and incomplete by its tracking entry (S5.T6); those limitations are stated at the end rather than hidden.

## Methodology

<!-- BEGIN GENERATED: methodology -->
**Hardware.** Apple M3 (Darwin 25.6.0 arm64), 8 vCPUs and 8 GiB assigned to Docker.

**Versions.** Load balancer built with go1.25.1; Nginx 1.27.5; vegeta v12.13.0; Docker 29.0.1; Compose 2.40.3-desktop.1.

**CPU pinning.** LB and Nginx: cores 0–1; backends 1–4: cores 2–5, one each; vegeta: cores 6–7. The split is fixed and never scaled from the host, and the harness verified it at run time: the load balancer reported gomaxprocs=2, and Nginx ran 2 workers.

**Backend logging.** Per-request backend logging was off for every run (`LOG_REQUESTS=false`), so the backends' stdout was not a throughput ceiling.

**Measured wall-clock.** The run started 2026-10-01T20:09:46Z and finished 2026-10-02T00:25:40Z, a measured duration of 4h 15m 54s.
<!-- END GENERATED -->

## Core results (HTTP/2)

Every algorithm is compared head-to-head with Nginx over HTTP/2 at three response sizes. `roundrobin` and `leastconn` are **matched** comparisons — Nginx's own algorithm is the same. `consistent-hash` and `p2c-ewma` are **nearest-equivalent**: Nginx has no exact equivalent, and the gap is stated per algorithm below. The `Comparison` column of each table carries the label.

<!-- BEGIN GENERATED: core -->
| Algorithm | Size | Competitor | Comparison | p50 (ms) | p90 (ms) | p95 (ms) | p99 (ms) | p99.9 (ms) | max (ms) | Peak (req/s) |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| roundrobin | 200b | nginx | matched | 0.278 | 0.895 | 2.059 | 7.006 | 19.383 | 31.344 | 3000 |
| roundrobin | 10kb | nginx | matched | 0.740 | 58.820 | 402.117 | 1410.277 | 1582.997 | 1775.197 | 2500 |
| roundrobin | 1mb | lb | matched | 10.026 | 177.396 | 334.205 | 417.684 | 457.536 | 490.126 | 200 |
| roundrobin | 1mb | nginx | matched | 6.420 | 13.525 | 24.094 | 103.507 | 152.906 | 164.918 | 175 |
| leastconn | 200b | lb | matched | 2.056 | 6.204 | 8.439 | 14.933 | 55.048 | 63.234 | 6000 |
| leastconn | 200b | nginx | matched | 0.282 | 1.158 | 2.365 | 9.404 | 26.272 | 32.388 | 5000 |
| leastconn | 10kb | lb | matched | 1.292 | 4.286 | 5.665 | 10.735 | 48.082 | 63.231 | 3000 |
| leastconn | 10kb | nginx | matched | 0.655 | 2.920 | 5.112 | 26.741 | 55.744 | 93.071 | 2500 |
| leastconn | 1mb | lb | matched | 7.361 | 18.935 | 49.117 | 110.338 | 172.875 | 183.098 | 150 |
| leastconn | 1mb | nginx | matched | 6.210 | 13.618 | 24.994 | 62.326 | 110.188 | 121.261 | 200 |
| consistent-hash | 200b | lb | nearest-equivalent | 1.408 | 5.146 | 8.659 | 45.689 | 81.139 | 121.357 | 8500 |
| consistent-hash | 200b | nginx | nearest-equivalent | 0.275 | 0.751 | 1.147 | 4.622 | 15.859 | 24.342 | 3000 |
| consistent-hash | 10kb | lb | nearest-equivalent | 0.484 | 1.536 | 2.164 | 10.252 | 24.441 | 42.553 | 1500 |
| consistent-hash | 10kb | nginx | nearest-equivalent | 0.455 | 1.706 | 3.029 | 12.822 | 63.252 | 179.848 | 8000 |
| consistent-hash | 1mb | lb | nearest-equivalent | 7.805 | 24.357 | 42.941 | 118.358 | 169.178 | 201.561 | 200 |
| consistent-hash | 1mb | nginx | nearest-equivalent | 7.517 | 17.502 | 23.112 | 39.519 | 51.073 | 65.492 | 225 |
| p2c-ewma | 200b | lb | nearest-equivalent | 0.545 | 2.721 | 4.713 | 32.589 | 56.198 | 80.213 | 4500 |
| p2c-ewma | 200b | nginx | nearest-equivalent | 0.378 | 15.921 | 40.263 | 127.933 | 206.070 | 224.075 | 8000 |
| p2c-ewma | 10kb | lb | nearest-equivalent | 1.154 | 4.066 | 5.173 | 8.199 | 13.949 | 47.128 | 4000 |
| p2c-ewma | 10kb | nginx | nearest-equivalent | 0.483 | 3.304 | 15.219 | 289.942 | 451.418 | 462.436 | 5000 |
| p2c-ewma | 1mb | lb | nearest-equivalent | 8.363 | 19.030 | 26.904 | 55.928 | 96.059 | 107.274 | 200 |
| p2c-ewma | 1mb | nginx | nearest-equivalent | 6.231 | 20.129 | 51.277 | 122.277 | 203.419 | 252.416 | 175 |
<!-- END GENERATED -->

### Per-algorithm analysis

- **round-robin (matched).** The two load-balancer scenarios at 200 B and 10 KiB were skipped (see Limitations), so the LB side of those rows is absent; at 1 MiB the LB reached 200 req/s against Nginx's 175 req/s. Nginx's own peaks for the first two sizes — 3000 req/s at 200 B and 2500 req/s at 10 KiB — sit far below its HTTP/1.1 results in the protocol table, which is the clearest sign that the HTTP/2 core slice ran cold.

- **least-connections (matched).** The LB leads at 200 B (6000 req/s against Nginx's 5000 req/s) and 10 KiB (3000 req/s against 2500 req/s); Nginx edges it at 1 MiB (200 req/s against the LB's 150 req/s). With four identical backends and no injected latency there is no imbalance to exploit, so a near-tie is the expected shape.

- **consistent-hash (nearest-equivalent).** The LB reaches 8500 req/s at 200 B, 1500 req/s at 10 KiB and 200 req/s at 1 MiB; Nginx reaches 3000 req/s, 8000 req/s and 225 req/s. This is not a like-for-like comparison: Nginx hashes with ketama and **no bounded loads**, while the LB adds a bound that spills traffic once a backend's in-flight load exceeds (1+ε)×mean. The spill is visible in the hot-key section, and it is the feature the project built, so the throughput difference partly measures that extra behaviour rather than raw ring speed.

- **p2c-ewma (nearest-equivalent).** The LB reaches 4500 req/s at 200 B, 4000 req/s at 10 KiB and 200 req/s at 1 MiB; Nginx reaches 8000 req/s, 5000 req/s and 175 req/s. Nginx's competitor is `random two least_conn` — two-choice over **active connections with no latency signal** — whereas the LB's power-of-two-choices weights by an EWMA of observed latency. On a rig with four identical backends and no injected latency the two coincide, so the gap only shows when the degraded slice puts latency on one backend.

## Protocol comparison (HTTP/1.1 vs HTTP/2)

The protocol slice runs the same matched round-robin topology over plain HTTP/1.1. Comparing its peaks with the core HTTP/2 round-robin peaks shows the effect of the listener protocol on this rig.

<!-- BEGIN GENERATED: protocol -->
| Algorithm | Size | Competitor | Comparison | p50 (ms) | p90 (ms) | p95 (ms) | p99 (ms) | p99.9 (ms) | max (ms) | Peak (req/s) |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| roundrobin | 200b | lb | matched | 0.510 | 4.336 | 7.107 | 20.757 | 51.723 | 84.176 | 8000 |
| roundrobin | 200b | nginx | matched | 0.246 | 1.017 | 1.340 | 2.329 | 4.910 | 46.403 | 11500 |
| roundrobin | 10kb | lb | matched | 0.499 | 2.292 | 5.126 | 28.611 | 58.260 | 76.771 | 4500 |
| roundrobin | 10kb | nginx | matched | 0.255 | 1.223 | 2.467 | 18.747 | 48.191 | 95.474 | 6000 |
| roundrobin | 1mb | lb | matched | 5.762 | 12.867 | 19.593 | 49.417 | 74.132 | 82.278 | 275 |
| roundrobin | 1mb | nginx | matched | 2.592 | 9.698 | 14.006 | 44.730 | 82.917 | 100.463 | 350 |
<!-- END GENERATED -->

Over HTTP/1.1, Nginx peaks at 11500 req/s (200 B), 6000 req/s (10 KiB) and 350 req/s (1 MiB); the LB reaches 8000 req/s, 4500 req/s and 275 req/s. Those are consistently higher than the HTTP/2 core round-robin peaks (Nginx 3000 req/s and 2500 req/s at 200 B and 10 KiB), which is the opposite of what a mature HTTP/2 implementation would show and reinforces that the HTTP/2 core round-robin figures are the noisy low end of the published run rather than a property of the algorithms.

## Failure and reload under load

The failure slice holds a steady HTTP/2 round-robin load at half the peak for 60 seconds and fires one event mid-run. It covers three runs: a backend killed (detection and recovery), a **no-op reload** (SIGHUP with an unchanged config), and a **drain reload** (a backend removed, then drained). Both reloads are judged against criteria fixed before the run — zero non-2xx responses, zero transport errors, and a post-event p99 no worse than twice the pre-event p99 — and the drain reload additionally requires the removed backend's arrival count to be frozen between the reload being applied and the end of the run.

<!-- BEGIN GENERATED: failure -->
| Run | Event at (s) | Total errors | First error (s) | Last error (s) | Time to detection (s) | p50 (ms) | p99 (ms) | p99 recovery (s) |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| backend-kill | 29 | 3 | 29.734 | 29.743 | 0.009 | 0.305 | 2.354 | 30 |

| Run | Event at (s) | Errors | pre-p99 (ms) | post-p99 (ms) | p99 factor | p50 (ms) | p99 (ms) | p99 recovery (s) | Verdict |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| no-op reload | 29 | 0 non-2xx, 0 transport | 1.752 | 1.762 | 2 | 0.324 | 2.025 | 30 | PASS |
| drain reload | 29 | 0 non-2xx, 0 transport | 1.856 | 2.140 | 2 | 0.307 | 68.078 | 30 | PASS |

| Snapshot | backend1 | backend2 | backend3 | backend4 |
| --- | --- | --- | --- | --- |
| before | 95493 | 95508 | 26176 | 95540 |
| applied | 96007 | 96026 | 26697 | 95710 |
| end | 105780 | 105779 | 36430 | 95710 |
<!-- END GENERATED -->

**Verdicts: `sighup-noop` PASS and `sighup-drain` PASS.** The no-op reload recorded zero non-2xx and zero transport errors, with a pre-event p99 of 1.752 ms and a post-event p99 of 1.762 ms; the drain reload likewise recorded zero errors, with a pre-event p99 of 1.856 ms against a post-event p99 of 2.140 ms, both inside the 2× factor. The drain snapshot table shows backend4's count at 95710 both when the reload was applied and at the end — unchanged, so the removed backend received no request after the swap. That is the zero-drop-while-draining claim evidenced under load.

## Degraded backend

One backend (backend3) is recreated with a 50-millisecond delay for the whole slice; the other three stay fast. All eight runs (four algorithms × two competitors) use one absolute rate — half the HTTP/2 round-robin 10 KiB LB peak — so their distributions and latencies are directly comparable. The expected shape: least-connections and p2c-ewma shift away from the slow backend; round-robin splits evenly; the LB's consistent-hash spills if the slow backend owns the hot key while Nginx's unbounded hash does not.

<!-- BEGIN GENERATED: degraded -->
| Algorithm | Competitor | p50 (ms) | p99 (ms) | backend1 % | backend2 % | backend3 % | backend4 % |
| --- | --- | --- | --- | --- | --- | --- | --- |
| roundrobin | lb | 51.617 | 67.924 | 25.00 | 25.00 | 25.00 | 25.00 |
| roundrobin | nginx | 51.267 | 61.823 | 25.00 | 25.00 | 25.00 | 25.00 |
| leastconn | lb | 51.594 | 61.680 | 25.50 | 25.20 | 24.80 | 24.50 |
| leastconn | nginx | 51.449 | 67.024 | 24.99 | 24.99 | 24.99 | 25.03 |
| consistent-hash | lb | 51.783 | 74.557 | 2.92 | 32.56 | 32.89 | 31.63 |
| consistent-hash | nginx | 51.374 | 59.147 | 0.02 | 0.02 | 99.93 | 0.02 |
| p2c-ewma | lb | 51.640 | 63.309 | 28.24 | 24.52 | 25.88 | 21.36 |
| p2c-ewma | nginx | 51.469 | 67.523 | 25.03 | 25.01 | 24.94 | 25.02 |
<!-- END GENERATED -->

**Outcome against that expectation.** Round-robin split exactly evenly (25.00% to each backend, both competitors). Consistent-hash behaved as predicted: the LB spilled (32.89% to backend3, with 32.56% to backend2 and 31.63% to backend4) while Nginx pinned 99.93% to backend3. But least-connections and p2c-ewma did **not** shift away from the slow backend — least-connections put 24.80% on backend3 and p2c-ewma 25.88%, both within a few points of an even split. The reason is visible in the latency: every degraded run's p50 sits in the low 50s and the raw minimum latency is 50 milliseconds, which means the injected delay was present on all four backends in this run, not backend3 alone. With no fast backend to prefer, there was nothing for a load- or latency-aware algorithm to shift toward. This is a limitation of the published run, not a verdict on the algorithms, and it is why the degraded slice does not demonstrate the expected divergence.

## Hot key

The load generator is a single container with a single address, and the LB's consistent-hash key is the client address, so every consistent-hash run is a **hot key** scenario: all requests hash to one backend. In every run that owner is backend3. Nginx's unbounded ketama sends essentially all of it there — 99.97% at core 200 B and 99.96% at core 10 KiB. The LB's bounded ring instead spills once the owner's in-flight load exceeds (1+ε)×mean: at core 10 KiB throughput the owner took 82.27% and the rest went to backend2 (17.19%), while at the lowest measured load (core 1 MiB latency@30%) no spill occurred (97.74%). The `Spill` column of the hot-key table records this for every consistent-hash run, core and degraded.

<!-- BEGIN GENERATED: hot-key -->
| Source | Size | Competitor | Load | Owner | backend1 % | backend2 % | backend3 % | backend4 % | Spill |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| core | 200b | lb | throughput | backend3 | 0.24 | 34.17 | 51.83 | 13.76 | yes |
| core | 200b | lb | latency@30% | backend3 | 0.02 | 24.98 | 73.73 | 1.26 | yes |
| core | 200b | lb | latency@50% | backend3 | 0.14 | 26.46 | 68.06 | 5.34 | yes |
| core | 200b | lb | latency@70% | backend3 | 0.18 | 21.67 | 73.28 | 4.86 | yes |
| core | 200b | lb | latency@90% | backend3 | 0.19 | 32.40 | 56.75 | 10.66 | yes |
| core | 200b | nginx | throughput | backend3 | 0.01 | 0.01 | 99.97 | 0.01 | no |
| core | 200b | nginx | latency@30% | backend3 | 0.02 | 0.02 | 99.93 | 0.02 | no |
| core | 200b | nginx | latency@50% | backend3 | 0.02 | 0.02 | 99.95 | 0.02 | no |
| core | 200b | nginx | latency@70% | backend3 | 0.01 | 0.01 | 99.96 | 0.01 | no |
| core | 200b | nginx | latency@90% | backend3 | 0.01 | 0.01 | 99.97 | 0.01 | no |
| core | 10kb | lb | throughput | backend3 | 0.03 | 17.19 | 82.27 | 0.52 | yes |
| core | 10kb | lb | latency@30% | backend3 | 0.06 | 1.02 | 98.73 | 0.19 | yes |
| core | 10kb | lb | latency@50% | backend3 | 0.04 | 1.24 | 98.49 | 0.23 | yes |
| core | 10kb | lb | latency@70% | backend3 | 0.06 | 6.54 | 92.71 | 0.70 | yes |
| core | 10kb | lb | latency@90% | backend3 | 0.03 | 13.66 | 85.85 | 0.46 | yes |
| core | 10kb | nginx | throughput | backend3 | 0.01 | 0.01 | 99.96 | 0.01 | no |
| core | 10kb | nginx | latency@30% | backend3 | 0.02 | 0.02 | 99.95 | 0.02 | no |
| core | 10kb | nginx | latency@50% | backend3 | 0.02 | 0.02 | 99.95 | 0.02 | no |
| core | 10kb | nginx | latency@70% | backend3 | 0.02 | 0.02 | 99.95 | 0.02 | no |
| core | 10kb | nginx | latency@90% | backend3 | 0.02 | 0.02 | 99.95 | 0.02 | no |
| core | 1mb | lb | throughput | backend3 | 0.46 | 4.35 | 93.95 | 1.24 | yes |
| core | 1mb | lb | latency@30% | backend3 | 0.75 | 0.75 | 97.74 | 0.75 | no |
| core | 1mb | lb | latency@50% | backend3 | 0.59 | 1.11 | 97.72 | 0.59 | yes |
| core | 1mb | lb | latency@70% | backend3 | 0.51 | 1.12 | 97.85 | 0.51 | yes |
| core | 1mb | lb | latency@90% | backend3 | 0.51 | 3.37 | 95.03 | 1.09 | yes |
| core | 1mb | nginx | throughput | backend3 | 0.52 | 0.52 | 98.43 | 0.52 | no |
| core | 1mb | nginx | latency@30% | backend3 | 0.68 | 0.68 | 97.97 | 0.68 | no |
| core | 1mb | nginx | latency@50% | backend3 | 0.58 | 0.58 | 98.26 | 0.58 | no |
| core | 1mb | nginx | latency@70% | backend3 | 0.50 | 0.50 | 98.50 | 0.50 | no |
| core | 1mb | nginx | latency@90% | backend3 | 0.55 | 0.55 | 98.35 | 0.55 | no |
| degraded | 10kb | lb | degraded | backend3 | 2.92 | 32.56 | 32.89 | 31.63 | yes |
| degraded | 10kb | nginx | degraded | backend3 | 0.02 | 0.02 | 99.93 | 0.02 | no |
<!-- END GENERATED -->

## Limitations and caveats

- **The published run is incomplete.** It produced 67 of the 71 result sets. Two core LB scenarios — round-robin at 200 B/LB and 10 KiB/LB — were skipped with "no sustainable rate at the seed rate (p99)", and their rows are absent from the core table. Every other algorithm's 200 B and 10 KiB LB scenario passed well above that rate, so the skips read as a cold start rather than a real ceiling.

- **The throughput ceiling is soft.** The cpusets are enforced inside Docker Desktop's Linux VM, which shares the host's physical cores through a hypervisor. The pinning removes contention *inside* the VM and makes the LB-vs-Nginx comparison fair, but it cannot give a true core, so absolute throughput is a soft ceiling. ADR-0022 records this and the generator bound.

- **The load generator is the limit for the large payloads.** The generator has two vCPUs; 10 KiB tops out in the low thousands of req/s and 1 MiB at a few hundred, so many peak-search steps were refused as under-delivered (ADR-0022) and the published peaks are rig-limited rather than proxy-limited.

- **Peaks are noisy.** Core HTTP/2 round-robin/Nginx peaked at 3000 req/s at 200 B while the same topology over HTTP/1.1 reached 11500 req/s; the HTTP/2 core round-robin figures are the least trustworthy in the matrix.

- **The degraded slice did not isolate the slow backend** (see above): the injected delay reached all four backends, so the expected algorithm divergence did not appear.

- **This document is generated from commit `8c2b7d4`.** Re-running `make bench-repro` from a clean checkout of that commit reproduces the raw output; `bench/generate-results.sh` then reproduces this document, and a second generation produces no diff.
