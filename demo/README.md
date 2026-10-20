# Local live demo

One load balancer (LB) in front of four backends, with eight traffic clients, Prometheus, Grafana and a control page. You change the selection algorithm as in production: edit the config file and send a reload. The process keeps serving, and no client request fails (ADR-0015 decisions 18–21).

The demo is local only. Every published port is bound to `127.0.0.1` ([ADR-0023](../docs/adr/0023-local-live-demo.md)).

## Run it

```sh
make demo-up        # docker compose -f demo/docker-compose.yml up -d --build
demo/acceptance.sh  # optional: checks the rig and the hot reload
make demo-down
```

The first build takes a few minutes. Then 16 containers run, and traffic starts at 400 req/s. Open **http://127.0.0.1:8095**.

| Host address | What |
|---|---|
| `http://127.0.0.1:8095` | Control page |
| `http://127.0.0.1:3000` | Grafana: `l7LoadBalancer — demo` (uid `l7lb-demo`) and the shared `l7LoadBalancer` dashboard |
| `http://127.0.0.1:8080` | LB client port |

The generators, the backend admin listeners, and the LB metrics (`:9090`, also the reload admin routes) and health (`:8081`) ports stay on the internal network.

## Change the algorithm

1. In [`configs/lb.yaml`](configs/lb.yaml), set `algorithm:` to `round_robin`, `least_conn`, `consistent_hash` or `p2c_ewma`.
2. Run `docker compose -f demo/docker-compose.yml kill -s HUP lb`.

The LB logs `"msg":"config reloaded"` with `previous_algorithm` and `algorithm`. The page header shows the new algorithm and `Last reload: SUCCESS`, and the share chart gets a dashed marker. Requests in flight finish on their backend. The next request uses the new algorithm.

The backend list reloads the same way. The LB rejects an unknown algorithm or a change to any other field, and keeps the running config. Compose mounts the whole `demo/configs/` directory, not the file, so the LB also sees an editor save by rename (ADR-0023 decision 15).

## Control page

The page holds no state. Every 2 s it reads the generators, the backends, the LB and the Docker Engine.

- **Header:** the algorithm from `lb_algorithm_info`, and the last reload (`SUCCESS` or `REJECTED`, with time and error).
- **Reload check:** sends one fixed config with `algorithm: "p3c_ewma"` to the LB's `POST /admin/reload`. The LB must answer `REJECTED` and change nothing. No user input reaches the LB (ADR-0025 decision 13). The page has no algorithm buttons, because the algorithm changes only by reload (ADR-0025 decision 17).
- **Load:** the total rate, and each client's offered, sent, dropped and error totals.
- **Backends:** delay, jitter and fail rate with **Apply** and **Clear**. **Stop** kills the container. It has no restart policy, so it stays down until **Start**.
- **Dashboard:** the demo dashboard, embedded. Open it alone at `http://127.0.0.1:3000/d/l7lb-demo/?var-window=15s&refresh=5s`. It shows request totals (successful, failed 5xx, no-backend 503), traffic share with reload markers, health, circuit state, latency, requests in progress, and container CPU and memory.

## Traffic

Eight clients send open-loop Poisson traffic, 400 req/s in total, split by Zipf rank (`s = 1.0`). Each client is its own container, so consistent hashing sees eight keys. Responses are 70% `/200b`, 25% `/10kb` and 5% `/1mb`. Request bodies are 70% none, 20% 1 KiB and 10% 64 KiB. A client allows 64 requests in flight and counts an arrival over that as `dropped`, not as an LB failure.

## Acceptance check

`demo/acceptance.sh` uses the generators, backends, LB admin endpoint and Prometheus directly. It never edits `configs/lb.yaml`.

1. On `round_robin`, with `backend3` at 200 ms, `backend3`'s p50 must be ≥ 150 ms and the others ≤ 20 ms after 30 s.
2. A reload to `p2c_ewma` through `POST /admin/reload` (the same operation as SIGHUP) must succeed with no LB restart and zero client errors. After 30 s, `backend3`'s share must be < 15%.

A failure names the assertion and the values, and exits non-zero. An exit trap restores the backends, the generators and the LB config. `INJECT_BACKENDS="backend2 backend3" demo/acceptance.sh` is a negative test and must fail.

## Docker socket

The `control` service mounts `/var/run/docker.sock` to stop and start backends. The socket gives root access to the host. This is acceptable only because every port is bound to `127.0.0.1` (ADR-0023 decision 2). The service acts only on the four demo backend containers (ADR-0025). No other compose file mounts the socket.

## Other stacks

Do not run this stack together with the repo-root stack or `bench/docker-compose.yml`. They also use `:8080`, and the root stack uses `:3000`. Demo charts are not benchmark results. See [`RESULTS.md`](../RESULTS.md).
