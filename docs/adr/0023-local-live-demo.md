# ADR-0023: Local live demo, no public deployment

**Status:** Accepted

## Context

The project needs a demo a viewer can watch: realistic traffic through the load balancer, a live page, and scenarios driven from that page. One option was a public deployment across cloud regions, so that geographic latency would separate the backends. That conflicts with the project scope:

- ADR-0005 excludes hardening against hostile internet traffic, cross-region failover, and validation under real production traffic. A public endpoint invites exactly that traffic.
- ADR-0019 makes the container image the packaging artifact. It does not commit the project to running a hosted instance.
- The thing regions would show, a latency-aware selector moving traffic off a slow backend, needs only controlled per-backend latency. The dummy backend can inject that locally. The published `degraded` slice in `RESULTS.md` failed to show it, because the injected delay reached all four backends.

Two facts about the system shape the demo:

- **The consistent-hash key is the client IP** (`requestHashKey` in `internal/balancer/hashkey.go`). One generator container is one key, so every consistent-hash run would be a hot key.
- **P2C-EWMA learns only from proxied responses** (ADR-0010). `Backend.RecordLatency` has one production caller, `internal/proxy`. The estimate does not decay without samples, and the active health checker does not feed it.

## Decision

Decision numbers are stable because code comments cite them.

1. **No public deployment.** The demo is a local stack that the owner runs, screen-shares or records. ADR-0005 does not change.
2. **Local only.** Every published port in the demo stack binds to `127.0.0.1`. The stack is not designed or documented for any other address.
3. **A separate compose file under `demo/`.** The repo-root stack and `bench/` do not change because the demo exists.
4. *Superseded by decision 14.*
5. **EWMA state is cold at first and can go stale.** At start every backend reads `EWMALatency() == 0` (ADR-0010). A backend that gets no traffic keeps its old estimate. The demo script allows a convergence period after each change before it makes any claim about the distribution.
6. **Eight explicit client services give skewed hash keys.** The generator runs as eight services from one YAML anchor, each with `RANK=1..8` and its own container IP. That gives eight real hash keys with no LB change. Each client takes its Zipf share of the total rate. `deploy.replicas` is not used, because replicas share one environment and cannot have different ranks.
7. **Backend latency and failure are set at runtime through an admin listener on the dummy backend.** The listener is on `:9091` and starts only when `ADMIN_ENABLED=true`, which only the demo compose sets. It accepts `POST` only, rejects unknown fields, and is never routed through the LB. It sets `sleep_ms`, `jitter_ms` (uniform ±, the effective sleep clamped at ≥ 0) and `fail_rate`. The `SLEEP_MS`/`FAIL_RATE` environment path does not change. Runtime changes avoid the env-and-recreate method that leaked into every backend in the `degraded` slice.
8. **The control service mounts the Docker socket, in the demo compose only.** It uses the socket to stop and start backend containers. The socket is root-equivalent on the host. This is accepted only because the stack is local (decision 2) and the service acts only on an allowlist of the demo's own containers (ADR-0025). No other compose file mounts the socket.
9. **Charts are Grafana panels. There is no new charting code.** The shared dashboard JSON has a `window` interval variable (default `5m`, so the root stack looks the same), an `lb` variable and a per-backend request-share panel. Only the demo compose sets `GF_SECURITY_ALLOW_EMBEDDING=true` with anonymous Viewer access. Only the demo Prometheus scrapes at 1 s.
10. **A scripted check validates the rig before any UI depends on it.** `demo/acceptance.sh` runs two phases with jitter 0. Phase 1, with round robin: backend3 gets 200 ms. After 30 s, backend3's p50 must be ≥ 150 ms, and backends 1, 2 and 4 must stay ≤ 20 ms. This proves the delay is isolated. Phase 2 is in decision 16. It checks request share, not p50, because under P2C-EWMA backend3 gets too few samples for a useful p50.
11. **A demo-only dashboard.** The demo provisions `demo/grafana/dashboards/l7lb-demo.json` (uid `l7lb-demo`). The shared dashboard and its provisioning files are mounted, never copied, so the shared `l7loadbalancer` dashboard is also there and cannot drift. The demo dashboard has number tiles for what clients see, a stacked traffic-share chart, and health and circuit state. The control page embeds it whole in one kiosk-mode iframe. The "no backend available" tile counts `5xx` with an empty `backend` label, which is how the proxy records a 503 with no selectable backend.
12. *Withdrawn with decision 4.*
13. *Withdrawn with decision 4.*
14. **One LB, `lb`, whose algorithm changes by reload.** The demo shows what production does: one process whose selection algorithm changes on a config reload while clients are served (ADR-0015). All eight clients send to `lb` for the whole run.
15. **The LB's config directory is bind-mounted, not the single file.** `demo/configs/` is mounted read-only at `/etc/l7lb-demo/`, and the LB runs with `-config /etc/l7lb-demo/lb.yaml`. Many editors save by writing a new file and renaming it over the old one. A single-file bind mount keeps the old inode, so the LB would reload stale content. A directory mount sees the rename. To change the algorithm, edit `demo/configs/lb.yaml` and send SIGHUP.
16. **The acceptance check proves the hot reload.** Phase 2 changes the algorithm to `p2c_ewma` through `POST /admin/reload`, the same operation SIGHUP uses (ADR-0015). It then requires a successful reload, `lb_algorithm_info` on `p2c_ewma`, backend3's request share below 15%, and zero client errors across the reload. Its exit trap posts the file's config again, so the file is authoritative afterwards.

## Consequences

- The demo shows the property that regions were meant to show, under the owner's control, at no cost, with no internet exposure and no change to ADR-0005.
- An algorithm change is a real in-place reload with the same backends and the same traffic.
- The latency-isolation check protects the demo from the failure that broke the `degraded` slice.
- The demo's numbers are not benchmark numbers. `RESULTS.md` is the only published performance evidence.
- The control service holds the Docker socket. This is acceptable only under decision 2. The stack must never bind to a non-loopback address.
- EWMA has no decay. A backend at the bottom of the P2C-EWMA ranking may get no more traffic, so its estimate can stay stale after its latency is restored. The demo script must not promise a recovery the algorithm does not do.
- The dummy backend has an admin listener that is off by default.
- Rejected alternatives:
  - A public multi-region deployment (conflicts with ADR-0005, costs money, and real latency is noisier than injected latency).
  - Restarting the LB to change the algorithm (drops traffic and resets every gauge).
  - Zipf keys inside one generator or a header hash key (the key is the client IP).
  - New selector-internal gauges such as EWMA scores (request share and p50 already show the shift).
  - Env-var latency with container recreate (the leak that broke `degraded`).
