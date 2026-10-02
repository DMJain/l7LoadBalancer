# Local live demo (S5.T16–T17)

A one-command, **local-only** live demonstration of the load balancer: four LB
instances (one per algorithm) sharing four backends, realistic Zipf-skewed
Poisson traffic from eight clients, and the project's single Grafana dashboard
showing the result live. There is no public deployment — see
[ADR-0023](../docs/adr/0023-local-live-demo.md) for that decision.

Every published port is bound to `127.0.0.1`. The stack is never configured or
documented for any other bind address.

## Run it

```sh
make demo-up      # docker compose -f demo/docker-compose.yml up -d --build
make demo-down    # docker compose -f demo/docker-compose.yml down
```

`make demo-up` builds the LB image (from the repo root `Dockerfile`), the dummy
backend, and the traffic generator, then starts eighteen containers. The first
build takes a few minutes; later `up`s are fast.

## What is published

| Host address | What |
|---|---|
| `http://127.0.0.1:3000` | Grafana (dashboard `l7LoadBalancer`) |
| `http://127.0.0.1:8080` | LB client port — `round_robin` |
| `http://127.0.0.1:8082` | LB client port — `least_conn` |
| `http://127.0.0.1:8083` | LB client port — `consistent_hash` |
| `http://127.0.0.1:8084` | LB client port — `p2c_ewma` |

The four LB client ports are published only for rehearsal (`curl`ing an LB by
hand). Generators, backend admin listeners, and LB metrics and health ports stay
on the internal demo network.

## Dashboard

Open the dashboard with the short demo window:

```
http://127.0.0.1:3000/d/l7loadbalancer/?var-window=15s&var-lb=lb-roundrobin
```

The `$lb` variable lists the four LB jobs (`lb-roundrobin`, `lb-leastconn`,
`lb-consistent-hash`, `lb-p2c-ewma`); pick one to see that selector's behaviour.
A 1 s Prometheus scrape interval (demo-only) feeds the 15 s window, so charts
react within seconds. The root stack keeps its 15 s scrape interval and 5 min
default window.

## Traffic

Eight clients send open-loop Poisson traffic at a total of 400 req/s, split by
Zipf rank (`s = 1.0`), so rank 1 sends the most and rank 8 the least. Each client
is a separate container, so consistent-hash sees eight distinct keys. The mix is
70% 200 B / 25% 10 KiB / 5% 1 MiB responses with 1 KiB and 64 KiB request bodies.

Runtime control (switch LB, set rate, change a backend's latency/jitter/failure,
kill and revive backends) arrives with S5.T16.4; until then the defaults above
are what you see.

## Acceptance check

Before trusting the rig (or building a UI on it), run the latency-isolation
check against a live stack:

```sh
docker compose -f demo/docker-compose.yml up -d --build
demo/acceptance.sh
```

It drives the generators' control endpoints and the backends' admin listeners
directly and reads the demo Prometheus's HTTP API, so it does not depend on the
control service. It proves two things (ADR-0023 decision 10):

1. **Phase 1** — with `round_robin` active and `backend3` at 200 ms (jitter 0),
   after 30 s `backend3`'s p50 is ≥ 150 ms while `backend1/2/4` stay ≤ 20 ms: the
   injected latency is isolated.
2. **Phase 2** — with `p2c_ewma` active and `backend3` still slow, after 30 s
   convergence `backend3`'s request share is < 15%: the selector moves off it.

Any failure names the assertion, the LB and the measured values, and exits
non-zero. An exit trap restores every backend profile and the generators' rate
and target, so the check is safe to re-run. `SLOW_BACKENDS` (default `backend3`)
is a verification hook for the negative run — set it to two backends to confirm
phase 1 fails: `SLOW_BACKENDS="backend2 backend3" demo/acceptance.sh`.

## Independence

This stack is separate from the repo-root stack (`docker-compose.yml`) and the
benchmark rig (`bench/docker-compose.yml`); neither is edited or affected by it
(ADR-0023 decision 3). Do not run it at the same time as the root stack — both
publish Grafana on `:3000` and an LB on `:8080`. Bench numbers remain the only
published performance evidence; demo charts are not benchmark results.

## Verified (S5.T16.3.2)

- `docker compose -f demo/docker-compose.yml config` is clean.
- After `up`, all four LBs come up healthy and are scraped successfully; every
  backend is selectable on every LB; the dashboard at `var-window=15s` shows the
  request rate and per-backend share moving.
- `docker compose ps` shows every published port on `127.0.0.1`.
