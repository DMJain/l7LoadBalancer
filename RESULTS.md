# Benchmark results

`bench/generate-results.sh` writes every table and the methodology block from the raw output in `bench/results/`. Nobody types those figures by hand. The script also fails if a figure in the prose does not match a table cell.

The raw output comes from one uninterrupted `make bench-repro` run from a fresh clone of commit `8c2b7d4` (`8c2b7d4adf0df3afc3433458f9f4908651b4801c`). `bench/results/provenance.json` records `git_dirty: false`, so the numbers trace to exactly that code. The run was noisy and incomplete. The limitations are at the end.

## Methodology

<!-- BEGIN GENERATED: methodology -->
**Hardware.** Apple M3 (Darwin 25.6.0 arm64), 8 vCPUs and 8 GiB assigned to Docker.

**Versions.** Load balancer built with go1.25.1; Nginx 1.27.5; vegeta v12.13.0; Docker 29.0.1; Compose 2.40.3-desktop.1.

**CPU pinning.** LB and Nginx: cores 0–1; backends 1–4: cores 2–5, one each; vegeta: cores 6–7. The split is fixed and never scaled from the host, and the harness verified it at run time: the load balancer reported gomaxprocs=2, and Nginx ran 2 workers.

**Backend logging.** Per-request backend logging was off for every run (`LOG_REQUESTS=false`), so the backends' stdout was not a throughput ceiling.

**Measured wall-clock.** The run started 2026-10-01T20:09:46Z and finished 2026-10-02T00:25:40Z, a measured duration of 4h 15m 54s.
<!-- END GENERATED -->

## Core results (HTTP/2)

Each algorithm runs against Nginx over HTTP/2 at three response sizes. `roundrobin` and `leastconn` are **matched**: Nginx uses the same algorithm. `consistent-hash` and `p2c-ewma` are **nearest-equivalent**: Nginx has no exact match, and the analysis below states the gap. The `Comparison` column shows the label.

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

- **round-robin (matched).** The load-balancer runs at 200 B and 10 KiB were skipped (see Limitations). At 1 MiB the LB reached 200 req/s and Nginx 175 req/s. Nginx's peaks at 200 B (3000 req/s) and 10 KiB (2500 req/s) are far below its HTTP/1.1 peaks. This is the clearest sign that the HTTP/2 core slice ran cold.

- **least-connections (matched).** The LB leads at 200 B (6000 req/s against 5000 req/s) and at 10 KiB (3000 req/s against 2500 req/s). Nginx leads at 1 MiB (200 req/s against 150 req/s). The four backends are identical and have no added latency, so there is no imbalance to use. A near-tie is the expected result.

- **consistent-hash (nearest-equivalent).** The LB reaches 8500 req/s at 200 B, 1500 req/s at 10 KiB and 200 req/s at 1 MiB. Nginx reaches 3000 req/s, 8000 req/s and 225 req/s. Nginx hashes with ketama and **no bounded loads**. The LB spills traffic when a backend's in-flight load is more than (1+ε)×mean. So the difference partly measures the spill, not only ring speed. The hot-key section shows the spill.

- **p2c-ewma (nearest-equivalent).** The LB reaches 4500 req/s at 200 B, 4000 req/s at 10 KiB and 200 req/s at 1 MiB. Nginx reaches 8000 req/s, 5000 req/s and 175 req/s. Nginx uses `random two least_conn`, which compares active connections and has **no latency signal**. The LB weights its choice by an EWMA of observed latency. With four identical backends the two methods act the same. Only a slow backend shows the difference.

## Protocol comparison (HTTP/1.1 vs HTTP/2)

This slice runs the same matched round-robin topology over plain HTTP/1.1.

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

Over HTTP/1.1, Nginx peaks at 11500 req/s (200 B), 6000 req/s (10 KiB) and 350 req/s (1 MiB). The LB reaches 8000 req/s, 4500 req/s and 275 req/s. These are higher than the HTTP/2 core round-robin peaks (Nginx 3000 req/s and 2500 req/s at 200 B and 10 KiB). A mature HTTP/2 stack should show the opposite. So the HTTP/2 core round-robin figures are the noisy low end of this run, not a property of the algorithms.

## Failure and reload under load

The failure slice holds a steady HTTP/2 round-robin load at half the peak for 60 seconds and fires one event in the middle. There are three runs:

- **backend-kill** stops a backend.
- **no-op reload** sends `SIGHUP` with an unchanged config.
- **drain reload** removes a backend and drains it.

Both reloads must meet criteria fixed before the run: zero non-2xx responses, zero transport errors, and a post-event p99 no worse than twice the pre-event p99. The drain reload must also show that the removed backend's request count does not change between "reload applied" and the end of the run.

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

The "Time to detection" column is the width of the error window (last error minus first error). It is not the delay before the failure was noticed.

**Verdicts: `sighup-noop` PASS and `sighup-drain` PASS.** The no-op reload had zero non-2xx and zero transport errors. Its p99 was 1.752 ms before the event and 1.762 ms after. The drain reload also had zero errors, with a p99 of 1.856 ms before and 2.140 ms after. Both are inside the 2× factor. The snapshot table shows backend4 at 95710 when the reload applied and at the end. So the removed backend got no request after the swap, and no request was dropped while it drained.

## Degraded backend

The slice restarts backend3 with a 50-millisecond delay for the whole slice. All eight runs (four algorithms × two competitors) use one absolute rate: half the HTTP/2 round-robin 10 KiB LB peak. The expected result: least-connections and p2c-ewma move load away from backend3, round-robin splits evenly, and the LB's consistent-hash spills while Nginx's unbounded hash does not.

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

**Result.** Round-robin split exactly evenly (25.00% to each backend, both competitors). Consistent-hash matched the prediction. The LB spilled (32.89% to backend3, 32.56% to backend2, 31.63% to backend4), and Nginx kept 99.93% on backend3. But least-connections and p2c-ewma did **not** move away from backend3. They put 24.80% and 25.88% on it, close to an even split.

The latency explains this. The p50 of every run is near the injected delay, not near a fast backend's sub-millisecond latency. So the delay was on all four backends in this run, not only on backend3. With no fast backend, a load-aware or latency-aware algorithm had nothing to prefer. This is a fault of the run, not a verdict on the algorithms.

## Hot key

The load generator is one container with one address. The LB's consistent-hash key is the client address. So every consistent-hash run is a **hot key** case: all requests hash to one backend, and here that owner is always backend3.

Nginx's unbounded ketama sends almost everything there: 99.97% at core 200 B and 99.96% at core 10 KiB. The LB's bounded ring spills when the owner's in-flight load is more than (1+ε)×mean. At core 10 KiB throughput the owner took 82.27% and backend2 took 17.19%. At the lowest load measured (core 1 MiB, latency@30%) there was no spill (97.74%). The `Spill` column shows this for every consistent-hash run.

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

## Limitations

- **The run is incomplete.** It produced 67 of 71 result sets. Two core LB runs, round-robin at 200 B and at 10 KiB, were skipped with "no sustainable rate at the seed rate (p99)". Their rows are absent. Every other algorithm passed well above that rate in those runs, so the skips look like a cold start, not a real ceiling.

- **The throughput ceiling is soft.** The cpusets apply inside the Docker Desktop Linux VM, which shares the physical cores through a hypervisor. Pinning removes contention inside the VM and keeps the LB-vs-Nginx comparison fair. It cannot give a real core ([ADR-0022](docs/adr/0022-honest-benchmarking-under-generator-limits.md)).

- **The load generator limits large responses.** The generator has two vCPUs. At 10 KiB it tops out in the low thousands of req/s, and at 1 MiB at a few hundred. Many peak-search steps failed as under-delivered, so those peaks are limited by the rig, not by the proxy.

- **Peaks are noisy.** Core HTTP/2 round-robin Nginx peaked at 3000 req/s at 200 B. The same topology over HTTP/1.1 reached 11500 req/s. The HTTP/2 core round-robin figures are the least reliable in the matrix.

- **The degraded slice did not isolate the slow backend.** The delay reached all four backends, so the expected difference between algorithms did not appear.

- **Reproduce it.** Run `make bench-repro` from a clean checkout of `8c2b7d4` to get the raw output again. Then `bench/generate-results.sh` rebuilds this file. A second generation makes no diff.
