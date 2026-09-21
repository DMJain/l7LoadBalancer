# 03: S3.T12 — Health endpoint handlers, third listener, ADR-0014

**What to build:** The demoable vertical slice for the health endpoint. A third
`http.Server` binds to the configured `health_endpoint.listen` address (default
`:8081`) and serves three orchestrator-native probe paths:

- **`/livez`** — returns 200 unconditionally with body `{"status": "alive"}`.
  Docker `HEALTHCHECK` targets this path. Go gives us no general deadlock
  signal, so the probe timing out *is* the failure signal; a watchdog is
  speculative complexity with nothing to tune it against.
- **`/startupz`** — a one-shot gate. Returns 503 until *both*
  `config_loaded == true` *and* `HealthChecker.ProbeRoundComplete() == true`
  (from ticket 02); at that moment transitions to 200 and stays there for the
  process lifetime. Body carries the two check names on both 200 and 503.
- **`/readyz`** — returns 200 only when all of `{config_loaded,
  initial_probe_complete, len(Registry.Selectable()) >= 1}` hold. The
  selectable-set check runs *live* at query time, so a fleet-wide outage
  transitions `/readyz` to 503 and lets a fronting LB or K8s Service route
  around this instance. Fly.io's HTTP check targets this path.

Response envelope is structured JSON with per-check breakdown, pinned exactly:

```json
{
  "status": "ready",
  "checks": {
    "config_loaded": true,
    "initial_probe_complete": true,
    "selectable_backends": 3
  }
}
```

On 503, same envelope with `"status": "not_ready"` and the failing check(s)
visible (e.g. `"selectable_backends": 0`). `/startupz` uses the same envelope
minus `selectable_backends`. `/livez` returns `{"status": "alive"}` unadorned.

The third `http.Server` mirrors `metricsSrv` exactly: constructed in `main`,
started in its own goroutine after the client and metrics servers, listens on
its own port, shares `sigCtx`, and joins the graceful-shutdown sequence
alongside the other two. Sprint 4's future `Run(ctx, cfg)` seam absorbs all
three uniformly.

Every probe hit invokes `Collector.RecordProbe(endpoint, statusCode)` from
ticket 02, so `lb_health_probe_total{endpoint, status}` reflects real probe
activity. Probes never enter the proxy path and therefore never touch
`lb_requests_total` or the latency histogram — a natural consequence of the
third listener.

**ADR-0014 is written as part of this ticket** and captures all ten decisions:
separate listener; three probe paths; `/livez` unconditional 200; `/startupz`
one-shot gate; `/readyz` gates on startup conditions plus live selectable-set
check; empty-selectable → 503 enables upstream failover; structured JSON body;
`lb_health_probe_total{endpoint, status}` counter on the shared registry;
`health_endpoint` config block; `HealthChecker.ProbeRoundComplete() bool` API.
The ADR also states explicitly which orchestrator maps to which path
(Docker HEALTHCHECK → `/livez`, Fly.io HTTP check → `/readyz`, Kubernetes maps
all three to distinct probe fields).

**Blocked by:** 02.

**Status:** ready-for-agent

- [ ] Tests written first (Red phase): table-driven `httptest`-based tests for
      each of the three handlers, driving the underlying `HealthChecker` and
      `Registry` into known states and asserting exact status codes and JSON
      bodies for every combination the response envelope spec calls out.
- [ ] `/livez` returns 200 and `{"status":"alive"}` under every state,
      including "no backends selectable" and "checker just started."
- [ ] `/startupz` returns 503 with `{"config_loaded": true, "initial_probe_complete": false}`
      before the first probe round completes, 200 after it completes, and
      stays 200 permanently under further state changes (including full
      backend eviction).
- [ ] `/readyz` returns 200 iff all three conditions hold; returns 503 with
      the failing check(s) visible in the JSON body for each of: pre-probe-
      round, empty selectable set, and both simultaneously.
- [ ] `/readyz` transitions 200 → 503 → 200 when a running instance loses
      every backend and then regains one (drives via `Backend.MarkUnhealthy` /
      `MarkHealthy` in the test).
- [ ] Every probe hit against a `httptest`-hosted mux increments the
      appropriate `lb_health_probe_total{endpoint, status}` cell, asserted
      via `promtestutil.ToFloat64`.
- [ ] `main.go` constructs a third `http.Server` on `cfg.HealthEndpoint.Listen`,
      shares `sigCtx`, logs `"health endpoint started"` at Info level (mirroring
      the metrics-server startup log line), and shuts down cleanly in the same
      `Shutdown` sequence as the client and metrics servers.
- [ ] `docs/adr/0014-health-endpoint-contract-and-probe-semantics.md` (or the
      accepted-numbering-of-the-day) is committed as part of this ticket, in
      the Accepted state, listing the ten decisions.
- [ ] Any doc-comments in ticket 02 that forward-referenced ADR-0014 are now
      pointing at a real file.
- [ ] `make test`, `make test-race`, `go vet ./...`, `make fmt` pass.
- [ ] Manual verification recorded in the session log: `make run`; then
      `curl -sS http://127.0.0.1:8081/livez`, `.../readyz`, `.../startupz` and
      paste the responses. Simulate empty selectable set (stop the three
      dummy backends) and assert `/readyz` transitions to 503 with
      `selectable_backends: 0` visible in the body.
- [ ] `PROGRESS.md` entry for this ticket links back to spec decisions
      D2–D12 for provenance.
