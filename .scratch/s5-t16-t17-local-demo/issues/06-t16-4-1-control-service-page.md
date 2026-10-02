# 06: S5.T16.4.1 — Control service and page: switch, rate, profiles, embedded panels

**What to build:** one page on `127.0.0.1` from which the owner drives the demo without a terminal: switch the active LB, set the total load rate, set each backend's latency/jitter/failure, see the current state, and watch embedded dashboard panels react within seconds.

Spec: `../spec.md` — *Control service and page (S5.T16.4)*; ADR-0023 decisions 4, 8, 9.

**Blocked by:** 05 (acceptance check — the rig is validated before the UI).

**Status:** ready-for-agent

- [ ] A new Go service in the same module with its own small image, `net/http` only; one static HTML page with plain JavaScript `fetch` calls, embedded in the binary; no framework, no build step, no charting code.
- [ ] JSON API actions: switch the active LB (sends the new target to all eight generators); set the total rate (sends the same total to all eight; each derives its own share); set a backend's profile (forwards to that backend's admin listener).
- [ ] Fan-out is total and honest: the response names every generator that failed; a partial fan-out is reported on the page, never hidden.
- [ ] Inputs validated before any call: the LB must be one of the four, the backend one of the four, profile values in range; unknown fields rejected.
- [ ] The page shows current state: active LB, each backend's profile, the total rate, and the generators' offered / sent / dropped counters; a failed action is shown as failed.
- [ ] Charts are iframes of the demo Grafana's existing dashboard panels with `var-window=15s&refresh=5s` and the active LB selected.
- [ ] Added to the demo compose: its port published on `127.0.0.1` only; it reaches generators and admin listeners over the internal network. No Docker socket in this ticket.
- [ ] Tests, Red-first, through the JSON API against `httptest` stand-ins for the eight generators and four admin listeners: each action reaches the right peers; fan-out reaches all eight; one failing stand-in is reported; invalid inputs make no outbound call; the page is served.
- [ ] The S5.T16.5 check still passes with the control service in the stack.
- [ ] `make test`, `make test-race`, `go vet ./...`, `make fmt` clean.
