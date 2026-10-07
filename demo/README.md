# Local live demo (S5.T16–T17, S5.T20)

A one-command, **local-only** live demonstration of the load balancer: **one**
load balancer in front of four backends, Zipf-skewed Poisson traffic from eight
clients, and a demo dashboard showing the result live. You change the selection
algorithm the way production does it: edit the config file and send a reload.
The same process keeps serving, and no client request fails (ADR-0015
decisions 18–21). There is no public deployment — see
[ADR-0023](../docs/adr/0023-local-live-demo.md) for that decision.

Every published port is bound to `127.0.0.1`. The stack is never configured or
documented for any other bind address.

## Run it

```sh
make demo-up        # docker compose -f demo/docker-compose.yml up -d --build
demo/acceptance.sh  # optional: proves the rig and the hot reload ("All checks passed.")
make demo-down      # docker compose -f demo/docker-compose.yml down
```

`make demo-up` builds the LB image (from the repo-root `Dockerfile`), the dummy
backend, the traffic generator and the control service, then starts sixteen
containers. The first build takes a few minutes; later `up`s are fast. Traffic
starts on its own at 400 req/s.

Then open **http://127.0.0.1:8095**.

To rehearse or record a walkthrough, follow [`DEMO-SCRIPT.md`](DEMO-SCRIPT.md);
the video's shot list and voiceover are in
[`docs/demo/video-script.md`](../docs/demo/video-script.md).

## Change the algorithm (hot reload)

1. Edit [`configs/lb.yaml`](configs/lb.yaml) with any editor and set
   `algorithm:` to `round_robin`, `least_conn`, `consistent_hash` or
   `p2c_ewma`.
2. Send the reload signal:

   ```sh
   docker compose -f demo/docker-compose.yml kill -s HUP lb
   ```

The LB logs one line, for example
`"msg":"config reloaded",…,"previous_algorithm":"round_robin","algorithm":"p2c_ewma"`.
The page header shows the new algorithm and `Last reload: SUCCESS`, and the
dashboard's traffic-share chart gets a dashed marker. Requests already in flight
finish on the backend they have; the next request uses the new algorithm.

Watch the log with `docker compose -f demo/docker-compose.yml logs -f lb`.

A config with an unknown algorithm is rejected and the running config stays.
The page's **Try a reload with an invalid config** button shows that without
touching the file. The backend list reloads the same way (add or remove a
backend in `configs/lb.yaml`, then SIGHUP). Any other field change is rejected
whole.

The whole `demo/configs/` directory is mounted read-only at `/etc/l7lb-demo/`,
not the single file, so editors that save by writing a new file and renaming
it are seen (ADR-0023 decision 15).

## Walkthrough video

**Video pending.** The recorded walkthrough (S5.T17.2) follows `DEMO-SCRIPT.md`
from a fresh `make demo-up` with `demo/acceptance.sh` passing first. The link
is added here once it is recorded and its storage location is fixed by the
owner (spec open decision 8).

## What is published

| Host address | What |
|---|---|
| `http://127.0.0.1:8095` | Control page: header, reload check, load rate, backend controls, embedded dashboard |
| `http://127.0.0.1:3000` | Grafana: `l7LoadBalancer — demo` (uid `l7lb-demo`) and the shared `l7LoadBalancer` dashboard |
| `http://127.0.0.1:8080` | LB client port, for `curl`ing the LB by hand |

Generators, backend admin listeners, and the LB's metrics (`:9090`, which also
serves the reload admin routes) and health (`:8081`) ports stay on the internal
demo network.

## Control page

The control page (ADR-0025 and its amendments) is plain and functional:

- **Header.** The algorithm the LB itself reports (`lb_algorithm_info`), and the
  LB's last reload: `SUCCESS` or `REJECTED`, with its time and error.
- **Change the algorithm.** The two steps above. The page has no algorithm
  buttons on purpose: the algorithm changes by reload (ADR-0025 decision 17).
- **Reload check.** **Try a reload with an invalid config** posts one fixed
  config, whose only invalid field is `algorithm: "p3c_ewma"`, to the LB's
  `POST /admin/reload`. The LB must answer `REJECTED` and change nothing. No
  caller input reaches the LB (ADR-0025 decision 13).
- **Load.** The total request rate, split across the eight clients by Zipf rank,
  and the clients' offered / sent / dropped / errors totals.
- **Backends.** Per backend: delay, jitter and fail rate with **Apply**,
  **Clear** (back to `0/0/0`), **Stop** (the Docker Engine's abrupt kill; no
  restart policy, so it stays down) and **Start**, plus its container state.
- **Dashboard.** The demo dashboard, embedded whole.

The page holds no state of its own: every value is read live from the
generators, the backends' admin listeners, the LB and the Docker Engine.

## Dashboard

The demo-only dashboard **`l7LoadBalancer — demo`** (ADR-0023 decision 11) can
also be opened on its own:

```
http://127.0.0.1:3000/d/l7lb-demo/?var-window=15s&refresh=5s
```

- **Top row:** requests per second, successful, failed (5xx from a backend) and
  "no backend available" (503), each as a total for the time range and a rate.
- **Middle:** the stacked traffic share per backend, with a dashed marker at
  each hot reload that changed the algorithm.
- **Bottom:** health and circuit breaker per backend, p99 latency per backend,
  request rate, latency (p50 and p99), and requests in progress.

The shared `l7LoadBalancer` dashboard is still provisioned beside it, unchanged.

## Traffic

Eight clients send open-loop Poisson traffic at a total of 400 req/s, split by
Zipf rank (`s = 1.0`), so rank 1 sends the most and rank 8 the least. Each
client is a separate container, so consistent hashing sees eight distinct keys.
The mix is 70% 200 B / 25% 10 KiB / 5% 1 MiB responses, with 1 KiB and 64 KiB
request bodies. Each client caps its in-flight requests at 64; an arrival over
the cap is counted as `dropped` by the client, not as an LB failure.

## Acceptance check

Run it against a live stack before trusting the rig:

```sh
demo/acceptance.sh
```

It drives the generators, the backends' admin listeners and the LB's reload
admin endpoint directly, and reads the demo Prometheus. It does not depend on
the control page, and it never edits `configs/lb.yaml`. It proves:

1. **Phase 1:** with the LB on `round_robin` and `backend3` at 200 ms
   (jitter 0), after 30 s `backend3`'s p50 is ≥ 150 ms while `backend1/2/4` stay
   ≤ 20 ms. The injected latency is isolated.
2. **Phase 2:** a hot reload to `p2c_ewma` through `POST /admin/reload`, the
   operation SIGHUP runs. It requires a `success` result, `lb_algorithm_info` on
   `p2c_ewma`, the LB container not restarted, **zero client errors across the
   reload**, and `backend3`'s share < 15% after 30 s.

Any failure names the assertion and the measured values, and exits non-zero.
An exit trap restores every backend profile, the generators' rate and target,
and the LB's config from `configs/lb.yaml`. `INJECT_BACKENDS` (default
`backend3`) is the negative-run hook for phase 1:
`INJECT_BACKENDS="backend2 backend3" demo/acceptance.sh` must fail.

## Docker socket

To stop and start backends, the `control` service mounts `/var/run/docker.sock`
and calls the Docker Engine API over it with the standard library's HTTP client,
no Docker SDK. The socket is **root-equivalent on the host**, and the container
runs as root solely to open it. That is acceptable only because every port in
this stack is bound to `127.0.0.1` (ADR-0023 decision 2). The service acts only
on an allowlist of this demo's own four backend containers (ADR-0025 amendment
decisions 8 and 11). No other compose file in the repository mounts the socket.

## Independence

This stack is separate from the repo-root stack (`docker-compose.yml`) and the
benchmark rig (`bench/docker-compose.yml`); neither is edited or affected by it
(ADR-0023 decision 3). Do not run it at the same time as either: they also use
`:8080`, and the root stack uses `:3000`. Benchmark numbers remain the only
published performance evidence; demo charts are not benchmark results.
