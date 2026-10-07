# ADR-0023: Local live demo, no public deployment

- **Status**: Accepted
- **Date**: 2026-10-02
- **Deciders**: Darshan Jain (project owner) + Claude Code (S5.D1, decisions made in the S5.T16–T17 grilling session; scope in `.scratch/s5-t16-t17-local-demo/spec.md`)

## Context

The project needs a demonstration a viewer can watch: realistic traffic flowing
through the load balancer, a page showing its behaviour live, and scenarios
driven from that page while the selectors are explained. The first framing of
this work was a live public deployment — the LB and backends spread across
cloud regions so geographic latency would separate the backends. That framing
collides with the project's own scope:

- [ADR-0005](0005-scope-of-production-grade.md) lists hardening against
  adversarial internet traffic, cross-region failover, and validation under
  multi-region or real production traffic as things "production-grade" does
  **not** mean here. A public endpoint invites exactly the traffic ADR-0005
  disclaims, and a multi-region layout implies a failover story the system does
  not have.
- [ADR-0019](0019-deployment-target.md) settles the container image as the
  packaging artifact. It does not commit the project to operating a hosted
  instance, and nothing in Sprints 1–5 depends on one.
- The thing regions were meant to demonstrate — a latency-aware selector moving
  traffic off a slow backend — needs only *controlled per-backend latency*, which
  the dummy backend can inject locally. The published `degraded` slice
  (`RESULTS.md`) failed to show it because the injected delay reached all four
  backends; the demo has to show it cleanly.

Three properties of the built system constrain how a live demo can work:

1. **The algorithm is not reloadable.** Only the backend list hot-reloads; any
   non-backend difference, `algorithm` included, rejects the reload whole
   ([ADR-0015](0015-reload-architecture.md) decision 4;
   `internal/config/diff.go`). A "rewrite the config and SIGHUP" algorithm
   switch would only ever log a rejected reload.
2. **The consistent-hash key is the client IP** (`requestHashKey`,
   `internal/balancer/hashkey.go`; CONTEXT.md "Hash key"). A single generator
   container is a single key — every consistent-hash run is a hot key
   (`RESULTS.md`, hot-key section).
3. **P2C-EWMA state is learned only from proxied responses**
   ([ADR-0010](0010-p2c-ewma-backend-latency-state-cold-start-and-failure-penalty.md);
   `Backend.RecordLatency` has one production caller, `internal/proxy`). There
   is no decay without samples, and the active health checker does not feed it.

## Decision

1. **There is no public deployment.** The original live-deployment deliverable
   is superseded by a local live-demo stack the owner runs on their own machine
   and screen-shares or records. ADR-0005 is unchanged: nothing here weakens a
   non-goal or adds a production claim.

2. **Local only, never internet-reachable.** Every published port in the demo
   stack is bound to `127.0.0.1`. The stack is not designed, configured, or
   documented for any other bind address.

3. **A separate compose file under `demo/`**, independent of the repo-root demo
   stack and of `bench/`. Neither of those changes behaviour because the demo
   exists; bench runtime behaviour in particular is unchanged.

4. **One LB container per algorithm, sharing the same four backends.** The demo
   runs four LB instances — `round_robin`, `least_conn`,
   `consistent_hash_bounded`, `p2c_ewma` — each with its own config file under
   `demo/`. "Switching algorithm" means pointing the traffic generator at a
   different LB; no config is rewritten and no reload is attempted for it. SIGHUP
   stays in the demo for what it actually does: changing the backend list (a
   drain under load). Prometheus distinguishes the LBs by scrape `job`, and the
   dashboard gains an LB variable filtering on `job`, so no LB code changes.

5. **EWMA state on switch is cold on first activation and stale thereafter.** An
   LB that has never carried traffic starts with every backend reading
   `EWMALatency() == 0` — the ADR-0010 decision 2 bootstrap, narrated as such.
   An LB that carried traffic earlier keeps the estimates from when it last did,
   because nothing updates them while it is idle. Either way the demo script
   allows a convergence period after every switch before any claim is made about
   the distribution.

6. **Skewed hash keys come from eight explicit client services.** The traffic
   generator runs as eight services expanded from one YAML anchor, each with a
   distinct `RANK=1..8` and therefore a distinct container IP — eight real hash
   keys with no LB change. Each derives its share of the total rate from its
   Zipf rank. `deploy.replicas` is not used: replicas share one environment and
   cannot be given per-instance ranks.

7. **Backend latency and failure are set at runtime through an admin listener on
   the dummy backend.** A separate listener (`:9091`), started only when
   `ADMIN_ENABLED=true`, which only the `demo/` compose sets. It accepts `POST`
   only, rejects unknown fields, and is never routed through the LB. It sets
   `sleep_ms`, `jitter_ms` (uniform ±, the effective sleep clamped at ≥ 0), and
   `fail_rate`. The existing `SLEEP_MS`/`FAIL_RATE` env path is untouched, and
   with `ADMIN_ENABLED` unset the backend's runtime behaviour is unchanged. Runtime
   mutation also avoids the env-and-recreate injection that leaked into every
   backend in the published `degraded` slice.

8. **A control service with the Docker socket, `demo/` compose only.** The
   control service — serving the single control page — mounts
   `/var/run/docker.sock` to kill and revive backend containers. The socket is
   root-equivalent on the host. It is accepted because the stack is local-only
   (decision 2), and the control service acts only on an allowlist of the demo's
   own backend containers. No other compose file in the repository mounts the
   socket.

9. **Charts are embedded panels of the existing Grafana dashboard; there is no
   new charting code.** The single dashboard JSON gains a `$window` interval
   variable (default `5m`, so the root stack renders as before), the LB variable
   (decision 4), and a per-backend request-share panel. The control page embeds
   panels with `var-window=15s&refresh=5s`. Only the `demo/` compose sets
   `GF_SECURITY_ALLOW_EMBEDDING=true` (alongside anonymous Viewer access), and
   only the demo Prometheus scrapes at 1 s.

10. **The rig is validated before the UI is built on it.** A scripted acceptance
    check runs in two phases with jitter 0: with the round-robin LB active,
    backend3 is set to 200 ms, and after 30 s backend3's p50 must be ≥ 150 ms
    while backends 1, 2 and 4 stay ≤ 20 ms (the injected delay is isolated); then
    with the p2c-ewma LB active and after convergence, backend3's request share
    must be < 15% (the selector moves off it). Share, not p50, is the phase-2
    assertion because under p2c backend3 receives too few samples for a
    meaningful p50.

## Consequences

- Positive: the demonstration shows the property the regions were meant to show,
  under the owner's control, at $0, with no internet exposure and no change to
  any ADR-0005 non-goal.
- Positive: algorithm comparisons are "same backends, same traffic, only the
  selector changed", and no demo step depends on an unsupported reload.
- Positive: the latency-isolation check guards the demo against the failure mode
  that sank the published `degraded` slice.
- Negative: four LB processes and eight client services make the demo stack
  heavier than the root stack, and the demo's numbers are not benchmark numbers —
  `RESULTS.md` remains the only published performance evidence.
- Negative: the control service holds the Docker socket. That is acceptable only
  under decision 2; the stack must never be adapted for a non-loopback bind.
- Negative: EWMA has no decay. A backend that falls to the bottom of the
  p2c-ewma ranking may receive no further traffic from that LB, so its estimate
  can stay stale after its latency is restored. The demo script must not promise
  recovery that the algorithm does not do (see S5.T17).
- Neutral: the dummy backend gains an admin listener that is off by default.

## Alternatives considered

- **Public deployment across cloud regions** — rejected: conflicts with ADR-0005
  (internet exposure, multi-region implications), costs money and upkeep, and
  real geographic latency is noisier and less controllable than injected latency
  for showing one specific selector property.
- **Switch algorithm by config rewrite + SIGHUP** — rejected: ADR-0015 decision 4
  rejects any reload that changes `algorithm`.
- **Switch algorithm by restarting a single LB** — rejected: each switch drops
  traffic for a few seconds and resets every gauge, which a viewer reads as an
  outage rather than a selector change.
- **Make `algorithm` hot-reloadable** — rejected for this work: it changes
  ADR-0015 and the reload code for a demo convenience.
- **Zipf keys inside one generator / a header-based hash key** — rejected: the
  hash key is the client IP, so in-process keys have no effect, and a header key
  is a frozen-contract change that would need its own ADR.
- **New selector-internals gauges (EWMA scores, ring ownership)** — deferred:
  request share next to per-backend p50 already shows the shift; an
  `lb_p2c_ewma_seconds` gauge is recorded as proposed S5.T18.
- **A separate demo dashboard JSON** — rejected: it would drift from the
  dashboard the root stack ships.
- **Change latency by env var and container recreate** — rejected: that is the
  mechanism whose leak sank the published `degraded` slice, and it restarts the
  backend under load.

## Amendment (2026-10-07, S5.T20.4) — a demo-only dashboard file

Decision 9 kept one dashboard JSON for the root stack and the demo. The
recorded demo needs a different screen: number tiles for what the clients see,
a stacked traffic-share chart that continues across LB switches and marks each
switch, and health and circuit state for the active LB
(`.scratch/s5-t20-demo-dashboard/spec.md`, D4). Adding those panels to the
shared file would change the root stack's dashboard for a need the root stack
does not have.

11. **The demo provisions a second, demo-only dashboard,
    `demo/grafana/dashboards/l7lb-demo.json` (uid `l7lb-demo`).** The shared
    dashboard JSON and its provisioning files are unchanged and still mounted,
    never copied, so the original `l7loadbalancer` dashboard is still there in
    the demo. The demo Grafana mounts the shared datasource directory, a
    demo-only dashboard provider that lists both directories, and the two
    dashboard directories. The control page embeds the demo dashboard whole in
    one kiosk-mode iframe, replacing its three single-panel iframes.
    Decision 9's other rule, no new charting code, holds: every chart is a
    Grafana panel.

12. **The demo dashboard reads every LB at once, and the active one is known
    from `demo_active_lb`** (ADR-0025 amendment decision 14). Rates and shares
    are summed over all `lb-*` jobs, because only the active LB carries
    traffic, so the share chart continues across a switch. Circuit state is
    joined with `demo_active_lb` so that it shows the active LB only. Health
    takes the minimum across LBs, since all four probe the same backends. The
    "no backend available" tile counts `5xx` with an empty `backend` label,
    which is how the proxy records a 503 when no backend is selectable.

## Amendment (2026-10-07, S5.T20.7) — the algorithm became reloadable

Context item 1 and decision 4 rest on ADR-0015 decision 4, which rejected any
reload that changes `algorithm`. ADR-0015's amendment of 2026-10-07 (decisions
18–21) makes the algorithm reloadable in place, so that premise no longer
holds. The alternative "make `algorithm` hot-reloadable" is no longer rejected:
it is done.

13. **Decision 4 (one LB per algorithm) is kept until the demo is reshaped.**
    The four-LB stack still runs, and it can now also show a true hot reload:
    edit an LB's config file in place, send it SIGHUP, and its algorithm
    changes without a restart. Moving the demo to one hot-reloaded LB, and
    removing the control page's algorithm buttons, is a follow-up ticket that
    needs the owner's approval.

## Amendment (2026-10-07, S5.T20.8) — one hot-reloaded LB

14. **Decision 4 is superseded: the demo runs one LB, `lb`, and its algorithm
    is changed by reload.** The owner wants the demo to show what production
    does: one process whose selection algorithm changes on a config reload
    while clients keep being served (ADR-0015 decisions 18–21). The four
    per-algorithm LBs, their configs, and the generators' target switching
    are removed. All eight clients send to `lb` for the whole run.

15. **The LB's config directory is bind-mounted, not the single file.**
    `demo/configs/` is mounted read-only at `/etc/l7lb-demo/`, and the LB
    runs with `-config /etc/l7lb-demo/lb.yaml`. Many editors save by writing
    a new file and renaming it over the old one. A single-file bind mount
    keeps pointing at the old inode, so the LB would reload stale content. A
    directory mount sees the rename. Editing `demo/configs/lb.yaml` with any
    editor and sending SIGHUP is therefore the whole procedure.

16. **The acceptance check proves the hot reload.** Phase 2 changes the
    algorithm to `p2c_ewma` through `POST /admin/reload`, the same operation
    SIGHUP uses (ADR-0015 decision 14). It then requires a successful reload,
    `lb_algorithm_info` on `p2c_ewma`, backend3's share below the ceiling, and
    zero client errors across the reload. Its exit trap re-posts the file's
    config, so the file is authoritative again afterwards (ADR-0015
    decision 15).

Decision 12's active-LB join and decision 13's interim note are withdrawn with
decision 4. Decision 6's distinct client IPs still matter for consistent
hashing.
