# Benchmarks

The benchmark rig: the LB, Nginx (pinned 1.27.x), four dummy backends, and a
vegeta load generator on a single flat bridge network, so container-to-container
networking is identical for every service and the LB-vs-Nginx comparison is
consistent across hosts. vegeta is the only load generator — wrk has no HTTP/2
support ([ADR-0020](../docs/adr/0020-benchmark-tool-vegeta-over-wrk.md)).

`./bench/run.sh all` runs the full 50-run matrix and writes reproducible
`.txt` summaries and `.hdr` histograms under `bench/results/`.

## Layout

- `run.sh` — the execution harness and the single source of truth for the
  matrix parameters (rates, durations, warmup, thresholds). Slices: `core`
  (36 runs, HTTP/2), `protocol` (12 runs, HTTP/1.1), `failure` (2 runs), `all`.
- `docker-compose.yml` — the topology: `lb`, `nginx`, `backend1`–`backend4`, `vegeta`.
- `nginx/http11.conf`, `nginx/h2.conf` — Nginx plain HTTP/1.1 and TLS+HTTP/2
  (`http2 on;`), constrained-matched to the LB (round-robin or `least_conn`,
  four backends, keepalive 100).
- `configs/http11/`, `configs/h2/` — eight self-contained LB configs, one per
  algorithm × client protocol (`roundrobin`, `leastconn`, `consistent-hash`,
  `p2c-ewma`). No templating; each is readable in isolation.
- `vegeta/Dockerfile` — pinned vegeta built for the host architecture (the
  popular prebuilt image is amd64-only and would run under emulation on arm64).
- `results/{core,protocol,failure}/` — the harness's output; `.txt` + `.hdr`
  are committed, raw `.gob`/`.csv` scratch lands in the gitignored `results/.tmp/`.

## Running

The whole matrix from the repo root:

```sh
./bench/run.sh all      # ~2h; use core|protocol|failure to run one slice
```

Each slice prints a summary table (algorithm, size, competitor, p50, p99,
throughput) and writes per-run files named by their parameters, e.g.
`results/core/roundrobin-10kb-nginx-throughput.txt` and
`results/failure/roundrobin-10kb-sighup.txt`.

Peak throughput is discovered automatically: seed at 1000 req/s, double until
p99 exceeds 100 ms or the error rate exceeds 1%, then bisect to within 500 req/s.
The first 5 s of every measured step is discarded as warmup. These are constants
at the top of `run.sh`, not flags.

Failure mode runs round-robin at 10 KB over TLS+HTTP/2 at 50% of discovered
peak for 60 s. `backend-kill` stops `backend3` at T+30 s; `sighup` sends an
unchanged-config SIGHUP to the LB at T+30 s (zero drops is the target). Both
write a `-timeseries.txt` (per-second cumulative stats) beside the summary.

Nginx and the LB are compared under a constrained match — same algorithm, same
topology, same keepalive pool — so a gap is attributable to implementation, not
tuning ([ADR-0020](../docs/adr/0020-benchmark-tool-vegeta-over-wrk.md)).

Tear the stack down when finished:

```sh
docker compose -f bench/docker-compose.yml down
```

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
  LB_CONFIG=./configs/h2/roundrobin.yaml NGINX_CONF=h2.conf \
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
every backend after three failures.
