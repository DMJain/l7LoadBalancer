# 07: S5.T16.4.2 — Kill and revive backends through the Docker socket

**What to build:** kill and revive buttons for each backend on the control page, so the owner can show the active health checker eject a dead backend, clean 502s for requests in flight, and reinstatement once it passes its probes again.

Spec: `../spec.md` — *Control service and page (S5.T16.4)*, Docker access; ADR-0023 decision 8.

**Blocked by:** 06 (control service and page).

**Status:** ready-for-agent

- [ ] The demo compose mounts the Docker socket into the control service only; no other compose file in the repository mounts it.
- [ ] The control service calls the Docker Engine API over the unix socket with the standard library's HTTP client; no Docker SDK.
- [ ] "Kill" is `docker kill` semantics (abrupt, matching the Sprint 3/4 chaos tests); "revive" starts the same container again.
- [ ] Only the demo's four backend containers are allowed targets; any other name is rejected before any Engine call is made.
- [ ] The page shows each backend's container state (running / exited) next to its profile; a failed Engine call is shown as failed.
- [ ] The demo README states that the socket is root-equivalent and is acceptable only because every port is bound to `127.0.0.1` (ADR-0023 decisions 2 and 8).
- [ ] Tests, Red-first, through the JSON API against an `httptest` stand-in serving the few Engine endpoints called: kill and revive reach the right container; a disallowed name makes no Engine call; an Engine error is propagated.
- [ ] Verified live: kill a backend under load → it is ejected on every LB within the health threshold and its share drops to zero on the dashboard; revive → it is reinstated.
- [ ] `make test`, `make test-race`, `go vet ./...`, `make fmt` clean.
