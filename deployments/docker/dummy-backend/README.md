# Dummy backend

A small HTTP service, standard library only, that local stacks, the benchmark and the demo use as a backend. It can add latency and failures.

## Routes

| Route | Response |
|---|---|
| `GET /` and any other `GET` path | `200`, `{"backend":"<name>"}` |
| `GET /health` | `200`, `ok` |
| `GET /stats` | `200`, `{"backend":...,"requests":N}` (arrival count) |
| `GET` or `POST /200b`, `/10kb`, `/1mb` | `200`, a fixed body of 200, 10240 or 1048576 bytes. A `POST` body is read and discarded. |
| Any other method or path | `405` |

An injected failure returns `500` with the normal body. `/health` and `/stats` ignore injected latency and failures. Otherwise an injected failure rate would make the LB health check flap.

## Configuration

| Variable or flag | Default | Meaning |
|---|---|---|
| `SLEEP_MS` | `0` | Added latency for each request, in ms. |
| `FAIL_RATE` | `0` | Fraction of requests (`0.0`–`1.0`) that return 500. |
| `LOG_REQUESTS` | `true` | One log line for each request. |
| `ADMIN_ENABLED` | `false` | Start the admin listener on `:9091`. |
| `TLS_CERT_FILE`, `TLS_KEY_FILE` | unset | Set both to serve TLS, with HTTP/2 through ALPN. |
| `-name` | `backend` | The name in each response body. |
| `-addr` | `:8080` | The listen address. |

The backend exits non-zero at startup on a bad value: a negative or non-integer `SLEEP_MS`, a `FAIL_RATE` outside `[0, 1]`, a bool other than `true` or `false`, or only one TLS variable.

## Admin listener

With `ADMIN_ENABLED=true`, a second server listens on `:9091`. The LB does not send traffic to this port, so an admin call cannot go through the LB. Only the demo stack turns it on, so nothing changes the benchmark backends during a run.

```sh
curl -X POST http://127.0.0.1:9091/ -d '{"sleep_ms":200,"jitter_ms":20,"fail_rate":0.1}'
```

- It accepts `POST` only. Other methods get `405` with `Allow: POST`.
- All fields are optional. An omitted field keeps its value.
- Unknown fields, a negative `sleep_ms` or `jitter_ms`, or a `fail_rate` outside `[0, 1]` get `400`.
- The response is the full profile now in effect.
- The sleep is `sleep_ms` plus a uniform offset in `[-jitter_ms, +jitter_ms]`, with a minimum of 0.

`SLEEP_MS` and `FAIL_RATE`, with jitter `0`, set the start profile.

## Run with docker compose

From `deployments/docker`:

```sh
docker compose up -d --build
```

| Service | Host port | `SLEEP_MS` | `FAIL_RATE` |
|---|---|---|---|
| `backend-a` | `9001` | `50` | `0.05` |
| `backend-b` | `9002` | `150` | `0.15` |
| `backend-c` | `9003` | `300` | `0.30` |

These ports match `configs/example.yaml`, so `make run` works with no change. Some responses fail on purpose. To change a default, set the variable for that service, for example `BACKEND_A_SLEEP_MS=0 BACKEND_C_FAIL_RATE=0 docker compose up -d`. To stop, run `docker compose down`.
