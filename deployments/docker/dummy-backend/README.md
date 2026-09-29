# Dummy backend

A tiny, standard-library-only HTTP service used to exercise the load
balancer end-to-end without standing up real upstreams (Sprint 1, task
S1.T9). It answers requests with a JSON body identifying itself and can
inject artificial latency and failures.

## Response

```
GET /           → 200 OK   {"backend":"backend-a"}
GET /health     → 200 OK   ok
GET /200b       → 200 OK   200 bytes
GET /10kb       → 200 OK   10240 bytes
GET /1mb        → 200 OK   1048576 bytes
```

`/200b`, `/10kb`, and `/1mb` serve pre-generated fixed-size bodies so the
benchmark harness can vary payload profile without a separate image. `/health`
is the LB health-checker target: it is always `200`, cheap, and deliberately
ignores `SLEEP_MS`/`FAIL_RATE` so an injected failure rate cannot make the LB
flap. Every other path answers with a body identifying the backend. When
`FAIL_RATE` triggers, the response is `500 Internal Server Error` but still
carries the body. Non-`GET` requests get `405 Method Not Allowed`.

## Environment variables

| Variable        | Type    | Default | Meaning                                                              |
|-----------------|---------|---------|----------------------------------------------------------------------|
| `SLEEP_MS`      | integer | `0`     | Artificial per-request latency in milliseconds (`0` disables it).    |
| `FAIL_RATE`     | float   | `0`     | Fraction of requests, `0.0`–`1.0`, that return HTTP 500 (`0` disables). |
| `TLS_CERT_FILE` | path    | unset   | PEM certificate; set with `TLS_KEY_FILE` to serve TLS (HTTP/2 via ALPN). |
| `TLS_KEY_FILE`  | path    | unset   | PEM private key; set with `TLS_CERT_FILE`.                           |

All are validated at startup: a non-integer `SLEEP_MS`, an out-of-range
`FAIL_RATE`, a negative `SLEEP_MS`, or a `TLS_CERT_FILE`/`TLS_KEY_FILE` pair
with only one side set logs an error and exits non-zero rather than silently
falling back to a default. When both TLS variables are set the server uses
`ListenAndServeTLS` (Go auto-negotiates HTTP/2 via ALPN); when neither is set
it serves plain HTTP.

## Flags

| Flag    | Default    | Meaning                                       |
|---------|------------|-----------------------------------------------|
| `-name` | `backend`  | Identifier returned in every response body.   |
| `-addr` | `:8080`    | Address to listen on.                         |

## Running via docker compose

From this directory (the compose file sets `-name` and distinct chaos
defaults per service):

```sh
docker compose up -d --build
```

That starts three services on host ports `9001`, `9002`, `9003`:

| Service     | Host port | `SLEEP_MS` | `FAIL_RATE` |
|-------------|-----------|------------|-------------|
| `backend-a` | `9001`    | `50`       | `0.05`      |
| `backend-b` | `9002`    | `150`      | `0.15`      |
| `backend-c` | `9003`    | `300`      | `0.30`      |

Those host ports match `configs/example.yaml` unmodified, so from the repo
root `make run` routes to them immediately.

Smoke test (each backend answers, some responses fail by design):

```sh
for p in 9001 9002 9003; do curl -s "http://127.0.0.1:$p/"; echo; done
```

Override the defaults per service, without editing the compose file, using
the per-service host variables (their built-in defaults are the table
above):

```sh
BACKEND_A_SLEEP_MS=0 BACKEND_C_FAIL_RATE=0 docker compose up -d
```

Tear down:

```sh
docker compose down
```
