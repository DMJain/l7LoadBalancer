# Benchmarks

The benchmark rig: the LB, Nginx (pinned 1.27.x), four dummy backends, and a
vegeta load generator on a single flat bridge network, so container-to-container
networking is identical for every service and the LB-vs-Nginx comparison is
consistent across hosts.

The execution harness (`run.sh`, the `.txt`/`.hdr` results, the full matrix) and
ADR-0020 land in issue 06. This directory currently holds the infrastructure
issue 05 builds.

## Layout

- `docker-compose.yml` — the topology: `lb`, `nginx`, `backend1`–`backend4`, `vegeta`.
- `nginx/http11.conf` — Nginx plain HTTP/1.1, constrained-matched to the LB
  (round-robin, four backends, keepalive 100).
- `nginx/h2.conf` — Nginx TLS + HTTP/2 (`http2 on;`), same match.
- `configs/http11/roundrobin.yaml` — default LB config for the plain-HTTP
  topology; the remaining seven algorithm × protocol configs land in issue 06.
- `vegeta/Dockerfile` — pinned vegeta built for the host architecture (the
  popular prebuilt image is amd64-only and would run under emulation on arm64).

## Running

Plain HTTP/1.1 (the default):

```sh
docker compose -f bench/docker-compose.yml up -d --build
curl -s http://127.0.0.1:8080/200b | wc -c    # through the LB
curl -s http://127.0.0.1:8090/200b | wc -c    # through Nginx
```

TLS + HTTP/2 (needs the shared SAN cert; the h2 LB config is issue 06). This
also makes the backends serve TLS so the LB speaks HTTP/2 to them:

```sh
scripts/generate-cert.sh
BACKEND_TLS_CERT_FILE=/certs/server.crt BACKEND_TLS_KEY_FILE=/certs/server.key \
  LB_CONFIG=./configs/h2/roundrobin.yaml NGINX_CONF=h2.conf \
  docker compose -f bench/docker-compose.yml up -d
curl -sk --http2 https://127.0.0.1:8443/200b | wc -c   # through Nginx
```

Vegeta idles under `up`; drive a run in-compose with an explicit entrypoint
(the rig never uses a host-installed vegeta):

```sh
echo "GET http://lb:8080/10kb" | docker compose -f bench/docker-compose.yml \
  run --rm --entrypoint vegeta vegeta attack -rate=500 -duration=10s > results.bin
```

Tear down:

```sh
docker compose -f bench/docker-compose.yml down
```
