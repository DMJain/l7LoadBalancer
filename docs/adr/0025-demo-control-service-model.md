# ADR-0025: Demo control service: stateless read-through, honest fan-out

**Status:** Accepted

## Context

The local demo (ADR-0023) needs one page from which the owner drives the demo without a terminal. The page sets the total load rate, sets each backend's latency, jitter and failure rate, stops and starts backends, triggers a rejected reload, and shows the Grafana dashboard. The service uses `net/http` only, with no framework, and a partial fan-out must be reported, never hidden.

The main open question is the state model. Does the service remember the desired settings, or read them back from the peers that hold them? Two facts decide it:

- The generators already hold the real runtime settings. Each generator keeps its `(total_rate, target)` behind its own `atomic.Pointer` (ADR-0024). The eight are independent and can disagree after a partial fan-out. A second copy in the control service could drift from them.
- The control service can restart on its own. Any state in its memory would be lost while the generators kept theirs.

## Decision

`demo/control-service` is a stdlib-only Go program with its own small image, in the repository's single module. It serves one embedded page and a small JSON API. Decision numbers are stable because code comments cite them.

1. **The service is stateless.** It stores no desired settings. `GET /api/state` reads every peer live and builds a snapshot. The total rate (and the generators' target) is reported only when every generator agrees. Otherwise it is empty, so a partial fan-out shows as "mixed" and is not hidden.
2. **Peers come from the environment, with the demo topology as defaults.** `GENERATORS`, `BACKENDS`, `LBS` and `LB_ADMINS` are comma-separated base URLs. `GRAFANA_URL` is the Grafana address the browser can reach. An LB's identity is the hostname in its URL.
3. **Each action is validated before any outbound call.** `POST /api/rate` (`{"total_rate": <n>}`) and `POST /api/lb` (`{"lb": <name>}`) fan out to all eight generators at the same time. `POST /api/backend` (`{"backend": <name>, ...profile}`) goes to that backend's admin listener. Unknown fields are rejected. The LB and backend must be in the configured set. Profile values must be in range: `sleep_ms ≥ 0`, `jitter_ms ≥ 0`, `fail_rate` in `[0,1]`. A rejected request makes no outbound call.
4. **The fan-out contacts every peer and names every failure.** The response is `200` with `{"ok": bool, "results": [...], "failed": [<names>]}`. A peer failure is data that the page shows, not a transport error. Validation failures return `400` with a plain message.
5. **The page is one embedded HTML file with plain JavaScript `fetch` calls.** There is no framework, build step or charting code. The page polls `/api/state` and shows each backend's profile and container state, the total rate, the generators' offered, sent and dropped counters, the LB's algorithm and its last reload. A failed action shows as failed. The page embeds the `l7lb-demo` dashboard whole in one kiosk-mode iframe (ADR-0023).
6. *Superseded by decisions 7–11.*
7. **The Docker Engine client is the standard library over the unix socket, with no Docker SDK.** `DOCKER_HOST` (default `unix:///var/run/docker.sock`) selects the endpoint. A `unix://` host uses an `http.Transport` that dials the socket, with base URL `http://docker`. An `http` or `https` host points the same client at a TCP endpoint, which tests use with `httptest`.
8. **Only the configured backends can be addressed, and the container name is derived.** A request names a backend (`backend1` to `backend4`). The service rejects any name not in `BACKENDS` before it calls the Engine. It then uses the container `CONTAINER_PREFIX + name` (default prefix `l7loadbalancer-demo-`). The demo compose pins each `container_name` to that string. The Engine never gets a name from the caller.
9. **Stop is the Engine's kill, and Start is the Engine's start.** `POST /api/backend/kill` calls `POST /containers/{name}/kill` (SIGKILL, like the chaos tests). `POST /api/backend/revive` calls `POST /containers/{name}/start`. A failed Engine call returns `200` with `{"ok":false,"error":…}`. The page labels the buttons Stop and Start. A per-backend Clear sets the profile to `0/0/0`.
10. **Container state is in `/api/state`.** Each backend's `GET /containers/{name}/json` `State.Status` is shown beside its profile. A failed read is reported as an error.
11. **The control container runs as root only to open the socket.** The socket is root-equivalent (ADR-0023), and a non-root user cannot open it. The demo compose sets `user: "0:0"` on this service only. This is acceptable only because the stack binds to loopback (ADR-0023 decision 2).
12. **The LBs' metrics listeners are a peer set.** `LB_ADMINS` (default `http://lb:9090`) pairs one to one with `LBS`, and startup fails if the counts differ. `/api/state` reads each LB's `GET /admin/reload/last` (ADR-0015) and reports it on that LB. A failed read is reported, not fatal. The listeners stay on the internal network.
13. **The invalid-reload action sends one fixed body.** `POST /api/reload-invalid` takes no input. It posts a constant YAML config to the LB's `POST /admin/reload` and returns the LB's answer. The config is valid except for `algorithm: "p3c_ewma"`, so validation rejects that field and the running config does not change. If the generators disagree on the target, the action returns `409` and calls no LB. No caller input can reach the LB's reload path, so the page cannot change a backend list.
14. *Withdrawn by decision 17.*
15. *Withdrawn by decision 17, except the Stop/Start and Clear labels now in decision 9.*
16. **The page shows the algorithm the LB reports.** `/api/state` reads each LB's `/metrics` from `LB_ADMINS` and reports the label of the `lb_algorithm_info` series whose value is 1 (ADR-0013). A failed read is reported, not fatal.
17. **The page has no algorithm control.** The algorithm changes only by reload (ADR-0023 decision 14). There is no `demo_active_lb` metric. `POST /api/lb` stays as a generic endpoint, but the page does not call it.
18. **`GET /metrics` serves container CPU and memory.** The service reads Docker's read-only stats for an allowlist: the four backend containers (decision 8) and each LB's container, `CONTAINER_PREFIX + <LB name>` (the demo pins `l7loadbalancer-demo-lb`). Names come from configuration, never from a request. Each scrape calls `GET /containers/{name}/stats?stream=false&one-shot=true` for every allowlisted container at the same time. It exposes, in hand-written Prometheus text format:
    - `demo_container_cpu_seconds_total{container}`: cumulative CPU time, so `rate()` gives cores in use.
    - `demo_container_memory_bytes{container}`: memory usage minus inactive file cache, the figure `docker stats` shows.
    - `demo_container_stats_up{container}`: 1 or 0, so a failed read is visible and not a silent gap.

    Nothing is cached between scrapes (decision 1).

## Consequences

- There is one source of truth: the generators, backends, LB and Engine. A restart or a partial fan-out cannot make the page show a state the demo is not in.
- The API is tested through HTTP against `httptest` stand-ins, with no access to unexported state.
- No new dependency and no frontend toolchain.
- `/api/state` makes many outbound requests per poll. A down peer adds up to its 5 s timeout to each refresh, and the page shows which peers are unreachable.
- The derived rate is undefined while the generators disagree. The page shows "mixed" and does not guess.
- The service is demo-only. No LB, bench or root-stack code changes.
- Rejected alternatives:
  - Server-held desired state (drifts on restart or partial failure).
  - One `POST /api/config` for everything (a partial failure is harder to attribute).
  - `502` on partial fan-out (clients would drop the per-peer detail).
  - A separate name/URL/job mapping (the hostname already serves).
  - The Docker SDK (a heavy dependency for a few calls).
  - `docker stop` instead of kill (kill matches the chaos tests).
  - Compose-generated container names (depend on project name and index).
  - A non-root user with `group_add` (the group id is host-specific and the socket is root-equivalent anyway).
  - Letting the page send its own invalid YAML (turns the service into a reload proxy).
  - cAdvisor (needs a privileged container with broad host mounts on Docker Desktop).
  - Go process metrics in the LB (covers the LB only and changes `internal/metrics` for a demo need).
