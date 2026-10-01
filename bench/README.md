# Benchmarks

The benchmark rig: the LB, Nginx (pinned 1.27.x), four dummy backends, and a
vegeta load generator on a single flat bridge network, so container-to-container
networking is identical for every service and the LB-vs-Nginx comparison is
consistent across hosts. vegeta is the only load generator — wrk has no HTTP/2
support ([ADR-0020](../docs/adr/0020-benchmark-tool-vegeta-over-wrk.md)).

`./bench/run.sh all` runs the full 71-run matrix and writes reproducible
`.txt` summaries, `.json` reports and `.hdr` histograms under `bench/results/`.

## Layout

- `run.sh` — the execution harness and the single source of truth for the
  matrix parameters (rates, durations, warmup, thresholds). Slices: `core`
  (48 runs, HTTP/2), `protocol` (12 runs, HTTP/1.1), `failure` (3 runs),
  `degraded` (8 runs, one backend slow), `all` (71 runs), and `smoke` (10 short
  attacks, a preflight that is deliberately **not** part of `all`).
- `docker-compose.yml` — the topology: `lb`, `nginx`, `backend1`–`backend4`, `vegeta`.
- `nginx/http11/roundrobin.conf`, `nginx/h2/{roundrobin,leastconn,consistent-hash,p2c-ewma}.conf`
  — Nginx plain HTTP/1.1 and TLS+HTTP/2 (`http2 on;`), four backends, keepalive
  100, `worker_processes 2`. `roundrobin` and `leastconn` (`least_conn`) are
  **matched**; `consistent-hash` (`hash $remote_addr consistent`, no bounded
  loads) and `p2c-ewma` (`random two least_conn`, no latency signal) are
  **nearest-equivalent** and their headers state the gap. Organised by protocol
  then algorithm, mirroring `configs/`.
- `configs/http11/`, `configs/h2/` — eight self-contained LB configs, one per
  algorithm × client protocol (`roundrobin`, `leastconn`, `consistent-hash`,
  `p2c-ewma`). No templating; each is readable in isolation.
- `vegeta/Dockerfile` — pinned vegeta built for the host architecture (the
  popular prebuilt image is amd64-only and would run under emulation on arm64).
- `results/{core,protocol,failure,degraded}/` — the harness's output; `.txt` +
  `.hdr` are committed for every result, plus a `.json` vegeta report beside each
  `save_result` set (the core/protocol throughput results and every degraded
  result). Raw `.gob`/`.csv` scratch lands in the gitignored `results/.tmp/`. The
  `.json` is the machine-readable source the results generator reads its
  percentiles from (S5.T10.1); the `.txt` carries the same report human-readable
  plus the metadata header, and the `.hdr` the p99.9 the JSON report lacks. The
  latency sweep and the failure summary files are read for their `.txt` metadata
  only, so they carry no `.json`.
- `results/provenance.json` — the machine-readable record of the invocation that
  produced the results beside it (see *Provenance record*).

## Running

The whole matrix from the repo root:

```sh
./bench/run.sh all      # ~2h; use core|protocol|failure|degraded to run one slice
```

Before a full run, `./bench/run.sh smoke` is the preflight gate: it brings up
every (protocol, algorithm, competitor) combination the matrix uses — h2 × all
four algorithms and HTTP/1.1 × round-robin, each against both competitors (10
attacks) — and runs a 2 s, 50 req/s attack on the 10 KiB endpoint. Any
combination below 100% success fails the slice with a nonzero exit naming it.
`smoke` is not part of `all`; its rate, duration and size are constants at the
top of `run.sh`.

Each slice prints a summary table (algorithm, size, competitor, p50, p99,
throughput) and writes per-run files named by their parameters, e.g.
`results/core/roundrobin-10kb-nginx-throughput.txt` (with `.json` and `.hdr`
siblings) and `results/failure/roundrobin-10kb-sighup-noop.txt`. Protocol-slice
filenames carry an `http11` token (spec §30), e.g.
`results/protocol/roundrobin-10kb-http11-lb-throughput.txt`; the latency `.txt`
holds one report per rate, with a `-latency-<pct>.hdr` histogram beside it.

Peak throughput is discovered automatically: seed at 1000 req/s, double until
p99 exceeds 100 ms or the error rate exceeds 1%, then bisect to within 500 req/s.
Latency is then profiled at 30/50/70/90% of that peak. The first 5 s of every
measured step is discarded as warmup. These are constants at the top of
`run.sh`, not flags.

Failure mode runs round-robin at 10 KB over TLS+HTTP/2 at 50% of discovered
peak for 60 s. `backend-kill` stops `backend3` at T+30 s. The two **reload**
runs send a SIGHUP to the LB at T+30 s and are judged against criteria fixed
before the run: zero non-2xx responses and zero transport errors over the whole
run, and post-event p99 within `RELOAD_P99_FACTOR` (2×) of the same run's
pre-event p99 (post-warmup to the event). Each result file carries both windows'
p99, the error counts and a `verdict=PASS|FAIL` line naming any failed
criterion, and the summary table shows the verdict. Both runs write a
`-timeseries.txt` (per-second cumulative stats) beside the summary.

- **no-op reload** (`sighup-noop`) re-reads an unchanged config, measuring the
  bare cost of the reload path under load.
- **drain reload** (`sighup-drain`) rewrites the mounted config in place — `cp`
  over the same inode, never a rename — to drop `backend4`, then SIGHUPs. It
  mounts a working copy of the committed round-robin h2 config in the results
  scratch area, so the committed configs are never modified. It passes only if
  the no-op criteria hold **and** `backend4`'s own arrival counter does not move
  between "reload applied" (the LB's `config_reloaded` line) and the end of the
  run. The counter is sampled just before the signal, once the reload is
  applied, and at the end; all three snapshots and both deltas are recorded, but
  only the second interval is judged — the first is legitimate pre-swap traffic.
  After the run the LB is recreated on the committed config. A rename instead of
  a `cp` trips an inode assertion and aborts the run, because the single-file
  bind mount would keep pointing at the old file and the run would test nothing.

Detection time is dominated by the active health checker's cadence: no bench
config sets `health.probe_interval`, so runs use the default 5 s with 3
consecutive failures before ejection (set it in a config to change it).

Nginx and the LB are compared under a constrained match — same topology, same
keepalive pool, and either the same algorithm (**matched**: round-robin,
least-connections) or Nginx's nearest equivalent (**nearest-equivalent**:
consistent-hash without bounded loads, p2c-ewma without a latency signal) — so a
gap is attributable to implementation, not tuning. Each result header records
which kind its comparison was, and every nearest-equivalent config states its
gap ([ADR-0020](../docs/adr/0020-benchmark-tool-vegeta-over-wrk.md)).

## Degraded slice

`./bench/run.sh degraded` recreates `backend3` with a 50 ms injected delay for
the whole slice, runs the four algorithms × both competitors over TLS+HTTP/2 on
the 10 KiB endpoint (8 runs, 30 s each plus the standard 5 s warmup), and
restores `backend3` to 0 ms when it ends — including on failure. All eight runs
use one absolute rate, half the h2/round-robin/10 KiB **LB** peak, so their
distributions and latencies are directly comparable; when `all` runs, that peak
is reused from the core slice in the same invocation, and when `degraded` runs
alone it is discovered first with the backends still fast.

Unlike the core slice, every degraded result is bracketed by `/stats` reads, so
the headline is the per-backend request share (counts and percentages), with the
summary table showing p50, p99 and all four shares. Its files live under
`results/degraded/`. The expected shape, which the numbers should show:

- **p2c-ewma** and **leastconn** shift load away from the slow `backend3` (its
  latency signal and its held connections make it the least attractive);
- **round-robin** splits evenly, so roughly a quarter of the requests pay the
  50 ms;
- the LB's **consistent-hash** spills if `backend3` owns the hot key (bounded
  loads cap its share), while Nginx's `hash $remote_addr consistent` does not.

`/stats` reads bypass the injected delay (it is a control endpoint). The active
health checker probes `backend3`'s own URL, so its probe also waits 50 ms, but
that is far below the checker's 2 s probe timeout — `backend3` stays healthy and
is not ejected for the whole run.

## Reproducing the published numbers

`make bench-repro` takes a stranger from a fresh clone to the full result set in
one command:

```sh
make bench-repro      # ~2–2.5 h on Docker Desktop
```

Prerequisites:

- Docker Desktop with **at least 8 vCPUs** assigned — the fixed 8-vCPU split is
  never scaled from the host, so fewer cannot honour the cpusets;
- `openssl` (for `scripts/generate-cert.sh`);
- `make`.

It runs these steps in order, stopping at the first failure:

1. **Preflight** — refuses to start unless Docker, Compose v2, `openssl` and
   `make` are present, Docker reports at least 8 vCPUs, no container is running,
   and the working tree is clean (untracked files included). Each refusal prints
   what is wrong and how to fix it; running containers are listed by ID and
   image, and the reproducer **never stops containers itself**.
2. Generate the certificates.
3. Build the bench images.
4. Run the smoke slice (`smoke`, the preflight gate — not part of `all`).
5. Run the full matrix (`all`).

The published run is produced by this command from a fresh clone. The harness
itself (`bench/run.sh`) is a development tool that only *records* a dirty tree or
running containers; refusal lives here, at the publication boundary.

## CPU pinning and run-time checks

The rig pins each role to a fixed slice of an 8-vCPU host so that a throughput
difference cannot come from one competitor being starved or over-provisioned.
The split, reused verbatim as the methodology statement: **LB and Nginx: cores 0–1; backends 1–4: cores 2–5, one each; vegeta: cores 6–7.** It is set with `cpuset` in `docker-compose.yml` and is never scaled from the available CPU count — scaled splits give incomparable numbers.

The harness proves at run time that the pinning took effect, aborting the slice
with the check name and the value it saw otherwise:

- after every LB (re)create, it reads `gomaxprocs` from the LB's startup line in
  the container logs and requires **2** (cores 0–1);
- after every Nginx (re)create, it waits until Nginx serves a 200 through its
  listener (workers fork slightly after the container starts), then counts
  worker processes and requires **2** (cores 0–1, matching the
  `worker_processes 2` in every competitor config).

Tear the stack down when finished:

```sh
docker compose -f bench/docker-compose.yml down
```

## Provenance record

Every invocation writes `bench/results/provenance.json` when it starts and
updates it when it finishes (an aborted slice still leaves a record), so any
result set traces back to exact code and hardware. The harness never refuses a
dirty tree or running containers — it records them; the reproducer and the
results generator are what refuse a dirty record.

| Field | Meaning |
|---|---|
| `git_sha` | the commit the run started from |
| `git_dirty` | whether the tree had uncommitted or untracked changes — a separate boolean, never a `-dirty` suffix |
| `slices` | the slices actually run (`all` expands to `core`, `protocol`, `failure`) |
| `start_time`, `finish_time` | UTC ISO-8601 timestamps |
| `host_os` | host OS, kernel and architecture (`uname -srm`) |
| `host_cpu` | host CPU model (`sysctl` on macOS, `/proc/cpuinfo` on Linux) |
| `docker_version`, `compose_version` | Docker engine and Compose plugin versions |
| `docker_cpus`, `docker_memory_bytes` | vCPUs and memory as Docker reports them |
| `cpusets` | the per-service cpuset split read from the resolved compose config |
| `lb_go_version`, `lb_gomaxprocs` | the LB's Go version and effective GOMAXPROCS, from its startup line |
| `nginx_version`, `nginx_workers` | Nginx version and running worker count |
| `vegeta_version` | the pinned vegeta source tag the image is built from (the `go install`-built binary embeds no version) |

Fields only knowable once containers exist (`lb_*`, `nginx_*`) are best-effort:
they are `null` when the container is not yet up (the record written at start)
and populated when the run finishes. `git_dirty` ignores `bench/results/` — the
harness's own output — so a repeated invocation is not reported dirty by a
previous run's record.

## Results generator

`bench/generate-results.sh` turns the raw `results/` output into the published
tables, so no published number is typed by hand. It takes an optional results
directory and document path (defaults `bench/results` and `RESULTS.md`), writes
only between `<!-- BEGIN GENERATED: <section> -->` / `<!-- END GENERATED -->`
markers, preserves everything outside them byte-for-byte, and refuses a
provenance record with `git_dirty: true`. A second run against the same results
produces no diff. Sections: `methodology`, `core`, `protocol` (S5.T10.1) and
`failure`, `degraded`, `hot-key` (S5.T10.2). It also checks that every
latency/throughput figure the hand-written narrative quotes outside the markers
(`ms`, `µs` or `req/s`, optionally prefixed `≈` or `~`) matches a generated
cell, failing and naming any figure that does not (S5.T10.3); percentages,
counts, sizes, ADR numbers and percentile names are never matched.
`bench/generate-results_test.sh` runs it against the committed fixture under
`bench/testdata/results-generator/` and is the regression gate (the generator is
shell-only, TDD-exempt).

## Manual smoke checks

Plain HTTP/1.1 (the default topology):

```sh
docker compose -f bench/docker-compose.yml up -d --build
curl -s http://127.0.0.1:8080/200b | wc -c    # through the LB
curl -s http://127.0.0.1:8090/200b | wc -c    # through Nginx
```

TLS + HTTP/2, with the backends also serving TLS so the LB speaks HTTP/2 to
them:

```sh
scripts/generate-cert.sh
BACKEND_TLS_CERT_FILE=/certs/server.crt BACKEND_TLS_KEY_FILE=/certs/server.key \
  LB_CONFIG=./configs/h2/roundrobin.yaml NGINX_CONF=h2/roundrobin.conf \
  docker compose -f bench/docker-compose.yml up -d --force-recreate
curl -sk --http2 https://127.0.0.1:8443/200b | wc -c   # through Nginx
```

Vegeta idles under `up`; drive a run in-compose with an explicit entrypoint
(the rig never uses a host-installed vegeta):

```sh
echo "GET http://lb:8080/10kb" | docker compose -f bench/docker-compose.yml \
  run --rm --entrypoint vegeta vegeta attack -rate=500 -duration=10s > results.bin
```

## How the h2 slice trusts its certs

The h2 configs point the LB at `https://backend*:8080`, so the proxy transport
sets `tls_skip_verify: true` for the shared self-signed cert. The active health
checker, however, uses its own default-cloned transport (ADR-0011 decision 11)
that the config knob cannot reach, so the compose `lb` service also sets
`SSL_CERT_FILE=/certs/server.crt`; the cert's SAN list already covers
`backend1`–`backend4`, so the probes verify successfully instead of ejecting
every backend after three failures. Nginx's h2 configs likewise proxy
`https://backends` with `proxy_ssl_verify off` — the backends serve TLS in this
slice, so a plaintext upstream would fail. Nginx speaks HTTP/1.1 upstream here
(it has no upstream-HTTP/2 directive for this topology), so the backend leg is
TLS+h1 for Nginx and TLS+h2 for the LB; the client-facing comparison stays
matched.
