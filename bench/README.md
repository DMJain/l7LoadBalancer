# Benchmarks

The benchmark rig: the LB, Nginx (pinned 1.27.x), four dummy backends, and a
vegeta load generator on a single flat bridge network, so container-to-container
networking is identical for every service and the LB-vs-Nginx comparison is
consistent across hosts. vegeta is the only load generator — wrk has no HTTP/2
support ([ADR-0020](../docs/adr/0020-benchmark-tool-vegeta-over-wrk.md)).

`./bench/run.sh all` runs the full 62-run matrix and writes reproducible
`.txt` summaries and `.hdr` histograms under `bench/results/`.

## Layout

- `run.sh` — the execution harness and the single source of truth for the
  matrix parameters (rates, durations, warmup, thresholds). Slices: `core`
  (48 runs, HTTP/2), `protocol` (12 runs, HTTP/1.1), `failure` (2 runs), `all`
  (62 runs), and `smoke` (10 short attacks, a preflight that is deliberately
  **not** part of `all`).
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
- `results/{core,protocol,failure}/` — the harness's output; `.txt` + `.hdr`
  are committed, raw `.gob`/`.csv` scratch lands in the gitignored `results/.tmp/`.
- `results/provenance.json` — the machine-readable record of the invocation that
  produced the results beside it (see *Provenance record*).

## Running

The whole matrix from the repo root:

```sh
./bench/run.sh all      # ~2h; use core|protocol|failure to run one slice
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
`results/core/roundrobin-10kb-nginx-throughput.txt` and
`results/failure/roundrobin-10kb-sighup.txt`. Protocol-slice filenames carry an
`http11` token (spec §30), e.g.
`results/protocol/roundrobin-10kb-http11-lb-throughput.txt`; the latency `.txt`
holds one report per rate, with a `-latency-<pct>.hdr` histogram beside it.

Peak throughput is discovered automatically: seed at 1000 req/s, double until
p99 exceeds 100 ms or the error rate exceeds 1%, then bisect to within 500 req/s.
Latency is then profiled at 30/50/70/90% of that peak. The first 5 s of every
measured step is discarded as warmup. These are constants at the top of
`run.sh`, not flags.

Failure mode runs round-robin at 10 KB over TLS+HTTP/2 at 50% of discovered
peak for 60 s. `backend-kill` stops `backend3` at T+30 s; `sighup` sends an
unchanged-config SIGHUP to the LB at T+30 s (zero drops is the target). Both
write a `-timeseries.txt` (per-second cumulative stats) beside the summary.
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
