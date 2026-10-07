# Benchmarks

The benchmark compares the load balancer (LB) with Nginx 1.27.x. Both sit in front of four dummy backends, and vegeta generates the load. All containers share one flat bridge network, so the network path is the same for every service. The rig uses only vegeta, because wrk has no HTTP/2 support ([ADR-0020](../docs/adr/0020-benchmark-tool-vegeta-over-wrk.md)).

## Reproduce the published numbers

```sh
make bench-repro      # about 2 to 2.5 h on Docker Desktop
```

You need:

- Docker Desktop with Compose v2 and **at least 8 vCPUs**. The CPU split is fixed and does not scale down.
- `openssl`, for `scripts/generate-cert.sh`.
- `make`.

`make bench-repro` runs `bench/repro.sh`. It does these steps in order and stops at the first failure:

1. Check the prerequisites. It refuses to start if a tool is missing, Docker has fewer than 8 vCPUs, any container is running, or the working tree is not clean (untracked files count). Each refusal tells you how to fix it. The script never stops containers itself.
2. Generate the certificates.
3. Build the bench images.
4. Run the `smoke` slice.
5. Run the full matrix (`all`).

Then run `bench/generate-results.sh` to write the tables into `RESULTS.md`.

When you finish, remove the stack:

```sh
docker compose -f bench/docker-compose.yml down
```

## Run single slices

```sh
./bench/run.sh smoke      # quick check, not part of all
./bench/run.sh core       # 48 runs, HTTP/2
./bench/run.sh protocol   # 12 runs, HTTP/1.1
./bench/run.sh failure    # 3 runs
./bench/run.sh degraded   # 8 runs, one slow backend
./bench/run.sh all        # 71 runs, about 2 h
```

`run.sh` holds all matrix parameters (rates, durations, warmup, thresholds) as constants at the top of the file. They are not flags. Unlike `repro.sh`, `run.sh` does not refuse a dirty tree or running containers. It only records them.

Each slice prints a summary table (algorithm, size, competitor, p50, p99, throughput). It writes files named by their parameters, for example `results/core/roundrobin-10kb-nginx-throughput.txt` and `results/failure/roundrobin-10kb-sighup-noop.txt`. Protocol-slice names carry an `http11` token, for example `results/protocol/roundrobin-10kb-http11-lb-throughput.txt`.

### smoke

`smoke` starts every combination the matrix uses: HTTP/2 with all four algorithms, and HTTP/1.1 with round-robin, each against both competitors (10 attacks). Each attack runs for 2 s at 50 req/s on the 10 KiB endpoint. If any combination has less than 100% success, the slice exits nonzero and names it.

### core and protocol: peak and latency

The harness finds the peak rate in these steps:

1. Start at the seed rate for the response size.
2. Double the rate until a step fails. A step fails if p99 is above 100 ms, more than 1% of requests fail, or vegeta delivered less than 95% of the target rate.
3. Bisect down to the size's granularity.

| Size | Seed rate (req/s) | Granularity (req/s) |
|---|---|---|
| 200 B, 10 KiB | 1000 | 500 |
| 1 MiB | 100 | 25 |

The harness then measures latency at 30, 50, 70 and 90% of the peak. It drops the first 5 s of every measured step as warmup. The seed rates differ because the generator's limit is in bytes per second, not requests per second ([ADR-0022](../docs/adr/0022-honest-benchmarking-under-generator-limits.md)).

### failure

Each run uses round-robin at 10 KiB over TLS and HTTP/2, at 50% of the peak, for 60 s. The event fires at second 30.

- `backend-kill` stops `backend3`.
- `sighup-noop` sends `SIGHUP` with an unchanged config. This measures the cost of the reload path under load.
- `sighup-drain` removes `backend4` from the config and sends `SIGHUP`.

A reload run passes if it has zero non-2xx responses and zero transport errors, and if its p99 after the event is within `RELOAD_P99_FACTOR` (2×) of its p99 before the event. The drain run also requires that the `backend4` request counter does not change between the LB's `config_reloaded` log line and the end of the run. The harness reads the counter before the signal, after the reload applies, and at the end. It judges only the second interval, because traffic before the swap is normal. Each result file has a `verdict=PASS|FAIL` line. Both reload runs also write a `-timeseries.txt` file with per-second stats.

The drain run edits a working copy of the committed config. It overwrites the file with `cp`, so the inode stays the same. A rename would leave the single-file bind mount on the old file, so the run checks the inode and aborts if it changed. After the run, the harness recreates the LB on the committed config.

Failure detection depends on the active health checker. No bench config sets `health.probe_interval`, so the checker uses the default: a probe every 5 s, and 3 failures in a row eject a backend.

### degraded

The slice restarts `backend3` with a 50 ms delay. It runs the four algorithms against both competitors over TLS and HTTP/2 at 10 KiB, for 30 s each plus the 5 s warmup. It restores `backend3` to 0 ms at the end, also on failure.

All eight runs use one rate: half the LB's HTTP/2 round-robin 10 KiB peak. With `all`, the slice reuses that peak from the core slice. Run alone, it first finds the peak while the backends are still fast.

The harness reads each backend's `/stats` before and after every run, so the main result is the share of requests per backend. `/stats` skips the injected delay. The health probe to `backend3` waits 50 ms, which is far below the 2 s probe timeout, so `backend3` stays healthy.

Expected result:

- `p2c-ewma` and `leastconn` move load away from `backend3`.
- `roundrobin` splits evenly, so about a quarter of requests wait 50 ms.
- The LB's `consistent-hash` spills if `backend3` owns the hot key. Nginx's `hash $remote_addr consistent` does not spill.

## Fair comparison

The LB and Nginx use the same topology and the same keepalive pool (100). The algorithm is either the same or Nginx's nearest equivalent:

| Algorithm | Nginx config | Kind |
|---|---|---|
| round-robin | default | matched |
| least-connections | `least_conn` | matched |
| consistent-hash | `hash $remote_addr consistent` (no bounded loads) | nearest-equivalent |
| p2c-ewma | `random two least_conn` (no latency signal) | nearest-equivalent |

Each result header records the kind. Each nearest-equivalent Nginx config states its gap in its header.

### CPU pinning

Each role has fixed cores on an 8-vCPU host, so no competitor gets more or less CPU than the other:

**LB and Nginx: cores 0–1; backends 1–4: cores 2–5, one each; vegeta: cores 6–7.**

`docker-compose.yml` sets this with `cpuset`. The split never scales with the host, because a scaled split gives numbers that you cannot compare. At run time the harness checks the pinning and aborts the slice if a check fails:

- After each LB start, the LB's startup log line must show `gomaxprocs` = 2.
- After each Nginx start, the harness waits for a 200 response and then counts 2 worker processes. This matches `worker_processes 2` in every Nginx config.

### Load generator limits

The load generator can fail when the LB is not at fault. So the harness bounds it and does not publish a number it could not produce ([ADR-0021](../docs/adr/0021-bounded-memory-in-benchmark-harness.md), [ADR-0022](../docs/adr/0022-honest-benchmarking-under-generator-limits.md)).

- **Worker cap.** Every `vegeta attack` uses `-max-workers` and `-max-connections` of 1024 (`VEGETA_MAX_WORKERS`, `VEGETA_MAX_CONNECTIONS`). Without a cap, vegeta at 1000 req/s on the slow 1 MB path starts thousands of requests at once. Vegeta and the LB then ran out of memory at about 3 GB each on the 8 GB VM. The cap keeps both under a few hundred MiB.
- **Delivery check.** A step counts only if vegeta sent at least `DELIVERY_MIN_RATIO_PCT` (95%) of the target rate. A capped generator that sends too little shows false low latency, so the step fails and the search backs off. Such a result shows `under-delivered; harness-limited; not a peak`. A real peak shows `peak_status=sustained`.
- **Rate caps.** 200 B and 10 KB cap at 200000 req/s. 1 MB caps at 500 req/s, because the rig tops out near 200 req/s at 1 MB.
- **Heap limits.** The `vegeta` container sets `GOMAXPROCS=2`, `GOMEMLIMIT=3GiB` and `GOGC=50`.

"No sustainable rate on this rig" is a valid result. The harness says so when the rig, not the LB, is the limit.

### Backend TLS in the HTTP/2 slices

The HTTP/2 LB configs use `https://backend*:8080` with `tls_skip_verify: true` for the shared self-signed certificate. The health checker uses its own transport, which this setting does not reach. So the `lb` service also sets `SSL_CERT_FILE=/certs/server.crt`. The certificate lists `backend1` to `backend4`, so the probes pass. Nginx proxies to `https://backends` with `proxy_ssl_verify off`. Nginx uses HTTP/1.1 to the backends, and the LB uses HTTP/2. The client side of the comparison stays matched.

## Files

- `run.sh`: the harness.
- `repro.sh`: the one-command reproducer behind `make bench-repro`.
- `docker-compose.yml`: services `lb`, `nginx`, `backend1` to `backend4`, `vegeta`.
- `configs/http11/`, `configs/h2/`: eight LB configs, one for each algorithm and client protocol.
- `nginx/http11/roundrobin.conf`, `nginx/h2/{roundrobin,leastconn,consistent-hash,p2c-ewma}.conf`: Nginx configs with four backends, keepalive 100 and `worker_processes 2`. The HTTP/2 configs use TLS with `http2 on;`.
- `vegeta/Dockerfile`: builds a pinned vegeta for the host architecture. The common prebuilt image is amd64 only.
- `results/{core,protocol,failure,degraded}/`: the output. Each result has a `.txt` (report and metadata) and a `.hdr` histogram (it has the p99.9). Throughput and degraded results also have a `.json` vegeta report, which the generator reads. Scratch files go to the gitignored `results/.tmp/`.
- `results/provenance.json`: where the results came from (see below).
- `generate-results.sh`: builds the `RESULTS.md` tables.

### Provenance record

Each run writes `results/provenance.json` at the start and updates it at the end. An aborted slice still leaves a record.

| Field | Meaning |
|---|---|
| `git_sha` | the commit of the run |
| `git_dirty` | true if the tree had uncommitted or untracked changes (ignores `bench/results/`) |
| `slices` | the slices that ran (`all` expands to `core`, `protocol`, `failure`) |
| `start_time`, `finish_time` | UTC ISO-8601 timestamps |
| `host_os` | `uname -srm` |
| `host_cpu` | CPU model (`sysctl` on macOS, `/proc/cpuinfo` on Linux) |
| `docker_version`, `compose_version` | Docker engine and Compose plugin versions |
| `docker_cpus`, `docker_memory_bytes` | vCPUs and memory that Docker reports |
| `cpusets` | the cpuset of each service, from the resolved compose config |
| `lb_go_version`, `lb_gomaxprocs` | from the LB startup line |
| `nginx_version`, `nginx_workers` | Nginx version and worker count |
| `vegeta_version` | the pinned vegeta source tag |

The `lb_*` and `nginx_*` fields are `null` until the containers exist.

### Results generator

`bench/generate-results.sh [results-dir] [doc]` defaults to `bench/results` and `RESULTS.md`. It writes only between `<!-- BEGIN GENERATED: <section> -->` and `<!-- END GENERATED -->` markers and keeps all other text byte for byte. The sections are `methodology`, `core`, `protocol`, `failure`, `degraded` and `hot-key`. It refuses a provenance record with `git_dirty: true`. A second run gives no diff.

The generator also checks each `ms`, `µs` or `req/s` figure in the prose (an optional `≈` or `~` prefix is allowed). Each must match a table cell, or the run fails and names the figure. It does not check percentages, counts or sizes. `bench/generate-results_test.sh` runs the generator against the fixture in `bench/testdata/results-generator/`.

## Manual checks

Plain HTTP/1.1:

```sh
docker compose -f bench/docker-compose.yml up -d --build
curl -s http://127.0.0.1:8080/200b | wc -c    # through the LB
curl -s http://127.0.0.1:8090/200b | wc -c    # through Nginx
```

TLS and HTTP/2, with TLS on the backends too:

```sh
scripts/generate-cert.sh
BACKEND_TLS_CERT_FILE=/certs/server.crt BACKEND_TLS_KEY_FILE=/certs/server.key \
  LB_CONFIG=./configs/h2/roundrobin.yaml NGINX_CONF=h2/roundrobin.conf \
  docker compose -f bench/docker-compose.yml up -d --force-recreate
curl -sk --http2 https://127.0.0.1:8443/200b | wc -c   # through Nginx
```

The vegeta container idles after `up`. Run an attack inside compose, not with a vegeta on the host:

```sh
echo "GET http://lb:8080/10kb" | docker compose -f bench/docker-compose.yml \
  run --rm --entrypoint vegeta vegeta attack -rate=500 -duration=10s > results.bin
```
