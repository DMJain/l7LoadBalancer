# ADR-0025: Demo control service — stateless read-through, honest fan-out

- **Status**: Accepted
- **Date**: 2026-10-02
- **Deciders**: Darshan Jain (project owner) + OpenCode (deepseek-v4.1-flash) (S5.T16.4.1)

## Context

The local live demo (ADR-0023) needs a single page from which the owner drives
the demonstration without a terminal: switch the active LB, set the total load
rate, set each backend's latency/jitter/failure, and watch the existing Grafana
panels react. ADR-0023 decision 8 commits the service to the demo stack and to
acting only on the demo's own peers; decision 9 commits the charts to embedded
panels of the single dashboard; the bundle spec
(`.scratch/s5-t16-t17-local-demo/spec.md`, *Control service and page (S5.T16.4)*)
fixes the actions, the `net/http`-only / no-framework constraint, and that a
partial fan-out must be reported, never hidden.

What the spec leaves to the implementation is the service's **state model**: does
it remember the desired configuration, or read it back from the peers that
actually hold it? Two facts constrain the answer:

- The generators already hold the authoritative runtime settings. Each
  generator's `(total_rate, target)` is its own `atomic.Pointer` (ADR-0024
  decision 4); the eight are independent and can disagree if a fan-out is
  partial. A control service that also stored a desired value would be a second
  source of truth that could silently drift from what the generators are doing.
- The control service can be restarted independently of the stack. Any state it
  held in memory would be lost while the generators kept their settings, so the
  page would show a value the demo is not running.

## Decision

A new stdlib-only Go program, `demo/control-service`, shipped as its own small
image in the repository's single module (the dummy-backend and traffic-generator
precedent). It serves one embedded static page plus a small JSON API.

1. **The service is stateless.** It stores no desired configuration. `GET
   /api/state` reads every peer live — the eight generators' status and the four
   admin listeners' profiles — and assembles a snapshot. The active LB and the
   total rate are *derived*: reported only when every generator's `target`
   (respectively `total_rate`) agrees, and `null`/empty otherwise, so a partial
   fan-out is visible as "mixed", not papered over with the last value the owner
   asked for.

2. **Peers and identity come from the environment, with the demo's topology as
   defaults.** `GENERATORS`, `BACKENDS` and `LBS` are comma-separated base URLs;
   `GRAFANA_URL` is the browser-reachable Grafana base. An LB's identity is the
   hostname of its URL — the compose service name, which is also its Prometheus
   `job` and therefore its dashboard `$lb` value (ADR-0023 decision 4). No
   separate name/URL/job mapping is introduced; the one string carries all three.

3. **Three action endpoints, each validated before any outbound call.** `POST
   /api/lb` (`{"lb": <name>}`) and `POST /api/rate` (`{"total_rate": <n>}`) fan
   out to all eight generators concurrently; `POST /api/backend` (`{"backend":
   <name>, ...profile}`) forwards to that one backend's admin listener. Unknown
   fields are rejected, the LB and backend must be in the configured set, and
   profile values must be in range (the admin listener's own contract:
   `sleep_ms ≥ 0`, `jitter_ms ≥ 0`, `fail_rate ∈ [0,1]`). A rejected request
   makes no outbound call.

4. **Fan-out is total and its response names every failure.** Every generator is
   contacted even when one is down; the response is `200` with `{"ok": bool,
   "results": [...], "failed": [<names>]}`. A dependency failure is a `200` with
   `ok:false` and the failed peers named — it is data the page renders, not a
   transport error to swallow. Validation failures are `400` with a plain
   message and no outbound call.

5. **The page is one embedded static HTML file with plain JavaScript `fetch`
   calls.** No framework, no build step, no charting code. It polls
   `/api/state`, renders the active LB, each backend's profile, the total rate
   and the generators' offered/sent/dropped counters, and shows a failed action
   as failed. Charts are iframes of the existing dashboard's panels against
   `GRAFANA_URL`, each with `var-window=15s&refresh=5s` and `var-lb=<active>`
   (ADR-0023 decision 9).

6. **No Docker socket in this ticket.** Kill/revive over the Docker Engine API
   is S5.T16.4.2; this service neither mounts the socket nor imports anything
   that talks to it.

## Consequences

- Positive: one source of truth (the generators and backends themselves), so a
  restart or a partial fan-out cannot make the page claim a state the demo is
  not in.
- Positive: the API is exercised entirely through HTTP against `httptest`
  stand-ins — the same seam the generator uses — with no reach into unexported
  state.
- Positive: no new dependency, no frontend toolchain, and the demo's existing
  images and ports are untouched.
- Negative: `/api/state` makes twelve outbound requests per poll, so a peer that
  is down adds its timeout to every state refresh; the client timeout bounds
  this and the page shows the unreachable peers.
- Negative: "active LB" is undefined while the generators disagree (mid-fan-out
  or after a partial failure). That is deliberate — the page shows mixed rather
  than guessing.
- Neutral: the control service is demo-only; no LB, bench, or root-stack code
  changes.

## Alternatives considered

- **Server-held desired state (write-through cache).** Rejected: a second source
  of truth that drifts from the generators on restart or partial failure, and
  it would let the page show a setting the demo is not running.
- **A single `POST /api/config` carrying LB, rate and profiles at once.**
  Rejected: the spec names the actions separately, and one endpoint would make a
  partial fan-out harder to attribute to an action.
- **`502` on a partial fan-out.** Rejected: the page must render which peers
  failed, so the failure is returned as a `200` body field; a non-2xx would make
  every client treat it as a generic error and discard the detail.
- **An explicit name/URL/job mapping in config.** Rejected: the compose service
  name is all three (host, `job`, `$lb`), so deriving identity from the URL
  removes a field that could disagree with itself.
- **Embedding the whole dashboard in one iframe.** Rejected by the ticket's
  wording ("panels") and because panel-scoped iframes let the page place the
  latency and share charts beside the controls; the dashboard URL remains the
  fallback.
