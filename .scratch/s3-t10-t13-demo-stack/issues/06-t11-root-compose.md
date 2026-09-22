# 06: S3.T11 — Root `docker-compose.yml`, `prometheus-stack.yml`, ADR-0013 decision 18

**What to build:** A single `docker-compose.yml` at the repository root that
composes the LB image from ticket 05 with the three existing dummy backends,
Prometheus, and Grafana — the whole system up under one command. The two
existing composes under `deployments/docker/` stay untouched and canonical
for the flows they were built for (chaos scripts and host-process
observability).

Services and images:

- **`backend-a`, `backend-b`, `backend-c`** — each `build:
  deployments/docker/dummy-backend`. Names match `configs/example.yaml` and
  `configs/docker.yaml`. Base image stays `scratch` (unchanged; adding a
  `probe` subcommand to `dummy-backend` was rejected as scope creep).
- **`l7lb`** — `build: .` using the T10 Dockerfile from ticket 05.
- **`prometheus`** — `prom/prometheus:v2.53.0`. Service name pinned literally
  to `prometheus` so the shared Grafana datasource provisioning (which
  hardcodes `http://prometheus:9090`) resolves in either stack.
- **`grafana`** — `grafana/grafana:11.1.0`.

Networking: single default bridge network (Compose auto-creates
`l7loadbalancer_default`); every service DNS-resolves every other by service
name. No frontend/backend split — that is a production concern excluded by
ADR-0005.

Published host ports (tight demo surface):

- `8080:8080` — LB client traffic.
- `8081:8081` — LB health endpoint. Published deliberately so a reviewer can
  `curl http://localhost:8081/readyz` during the demo and see the structured
  JSON that ticket 03 was designed for.
- `3000:3000` — Grafana.

Prometheus, LB metrics, and the backends stay internal to the compose network.

Dependency chain (mixed health-gating strategy — backends stay `scratch`, LB
self-probes via its ticket 05 `HEALTHCHECK`):

- `backend-a/b/c`: no `depends_on` — start immediately.
- `l7lb`: `depends_on: {backend-a: {condition: service_started}, backend-b:
  …, backend-c: …}`; its own `healthcheck` block runs
  `l7lb probe http://127.0.0.1:8081/livez`.
- `prometheus`: `depends_on: {l7lb: {condition: service_healthy}}` — never
  scrapes a not-yet-listening metrics port.
- `grafana`: `depends_on: {prometheus: {condition: service_started}}` —
  Grafana retries datasource connections gracefully.

`restart: unless-stopped` on every service — makes the `docker stop backend-a` /
`docker start backend-a` loop that demonstrates circuit-breaker recovery
work without containers vanishing.

Config bind-mount: `./configs/docker.yaml:/etc/l7lb/config.yaml:ro`. Since
the image bakes the same file, this is a no-op override at first; its value
is that a reviewer can edit config on disk and `docker compose restart l7lb`
without rebuilding.

Two supporting artifacts:

- **`deployments/docker/observability/prometheus/prometheus-stack.yml`** — a
  new file sitting alongside the existing `prometheus.yml`. Only delta:
  `targets: ["l7lb:9090"]` instead of `targets: ["host.docker.internal:9090"]`.
  Both files carry a header cross-reference comment naming each other and
  ADR-0013 decision 18.
- **Grafana provisioning bind-mount** — root compose bind-mounts
  `./deployments/docker/observability/grafana/provisioning:/etc/grafana/provisioning:ro`
  and `./deployments/docker/observability/grafana/dashboards:/var/lib/grafana/dashboards:ro`.
  Single source of truth for dashboard JSON and datasource config.
  Grafana's environment matches the observability compose:
  `GF_AUTH_ANONYMOUS_ENABLED=true`, `GF_AUTH_ANONYMOUS_ORG_ROLE=Viewer`.

**ADR-0013 amendment** — a new decision 18 appended to ADR-0013 capturing:
the repo-root compose is additive (not a replacement for the two existing
composes); per-stack Prometheus config; single-source Grafana provisioning;
the exact published-port list (`8080`, `8081`, `3000`); the mixed
health-gating dependency chain above.

Chaos scripts (`deployments/docker/chaos/eviction.sh` and `circuit.sh`) are
untouched — they keep targeting the host-process LB flow they were built
for. Any future unification is its own ticket.

**Blocked by:** 05.

**Status:** done

- [x] `docker compose -f docker-compose.yml config` at the repo root parses
      cleanly, listing five services.
- [x] `docker compose up -d --build` from a clean state converges within
      ~60 seconds: `docker compose ps` shows `l7lb` as `Up (healthy)` and
      the other four as `Up`.
- [x] `curl -sS http://localhost:8080/` returns 200 through the proxy
      (routes to a live backend).
- [x] `curl -sS http://localhost:8081/readyz` returns 200 with
      `"selectable_backends": 3` in the JSON body.
- [x] `curl -sS http://localhost:8081/livez` returns 200 with
      `{"status":"alive"}`.
- [x] `curl -sS http://localhost:8081/startupz` returns 200 with
      `"initial_probe_complete": true` in the body once the LB has been
      up for one probe interval.
- [x] Grafana at `http://localhost:3000` renders the provisioned
      `l7LoadBalancer` dashboard with anonymous Viewer access; the three
      gauge panels (Circuit state, Backend healthy, Active connections) are
      populated from the first scrape thanks to `main.go`'s startup seeding
      (ADR-0013 decision 9); the two traffic-derived panels fill after
      `curl` traffic.
- [x] Prometheus is *not* accessible from the host directly (unpublished
      port); it is reachable from Grafana as `http://prometheus:9090` via
      the internal network.
- [x] `docker stop backend-a`; wait; `curl http://localhost:8081/readyz`
      still returns 200 (two backends still selectable);
      `curl http://localhost:8080/` still succeeds; Grafana's `Backend
      healthy` panel eventually shows `backend-a` unhealthy.
- [x] `docker stop backend-a backend-b backend-c`; wait one health-check
      interval; `curl -sS -o /dev/null -w "%{http_code}" http://localhost:8081/readyz`
      returns `503`; body shows `"selectable_backends": 0`.
- [x] `docker start backend-a`; `/readyz` transitions back to 200 within
      the health-checker's recovery threshold.
- [x] `deployments/docker/observability/prometheus/prometheus-stack.yml`
      exists; its `targets` names `l7lb:9090`; the existing `prometheus.yml`
      is unmodified except for the added header cross-reference comment.
- [x] `deployments/docker/docker-compose.yml` and
      `deployments/docker/observability/docker-compose.yml` are both
      untouched. `deployments/docker/chaos/eviction.sh` and `circuit.sh`
      are untouched.
- [x] `docs/adr/0013-observability-metrics-logging-and-integration.md`
      gains decision 18 as documented above; existing decisions 1–17 are
      unchanged.
- [x] Manual smoke recorded in the session log covering the full up →
      dashboard → chaos → recovery → down flow.
- [x] `PROGRESS.md` entry for this ticket links back to spec decisions
      D26–D38 for provenance.
