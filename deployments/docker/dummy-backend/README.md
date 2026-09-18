# Dummy backend

A tiny, standard-library-only HTTP service used to exercise the load
balancer end-to-end without standing up real upstreams (Sprint 1, task
S1.T9). It answers requests with a JSON body identifying itself and can
inject artificial latency and failures.

## Response

```
GET /           → 200 OK   {"backend":"backend-a"}
```

Every path answers, and the body always identifies the backend. When
`FAIL_RATE` triggers, the response is `500 Internal Server Error` but still
carries the identifying body. Non-`GET` requests get `405 Method Not
Allowed`.

## Environment variables

| Variable    | Type    | Default | Meaning                                                              |
|-------------|---------|---------|----------------------------------------------------------------------|
| `SLEEP_MS`  | integer | `0`     | Artificial per-request latency in milliseconds (`0` disables it).    |
| `FAIL_RATE` | float   | `0`     | Fraction of requests, `0.0`–`1.0`, that return HTTP 500 (`0` disables). |

Both are validated at startup: a non-integer `SLEEP_MS`, an out-of-range
`FAIL_RATE`, or a negative `SLEEP_MS` logs an error and exits non-zero
rather than silently falling back to a default.

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

Override the defaults per service for a different chaos profile:

```sh
SLEEP_MS=0 FAIL_RATE=0 docker compose up -d
```

Tear down:

```sh
docker compose down
```
