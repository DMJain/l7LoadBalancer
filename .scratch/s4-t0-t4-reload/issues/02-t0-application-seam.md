# 02: S4.T0 — Application seam (`internal/app`: build + run)

**What to build:** A behaviour-preserving prefactor that moves the whole wiring
graph out of `main` into a new, importable `internal/app` package (approved in
the grilling), so production and tests build the system the same way. After
this ticket the binary behaves identically, `main` is only flags / file I/O /
signals, and the chaos tests no longer duplicate the wiring — closing the
Sprint 3 retro's `assemble` debt. Spec: stories 48–50, decisions D3–D4.

**Blocked by:** 01.

**Status:** ready-for-agent

- [ ] `internal/app` exposes **build** (validated config + logger → application
      value, or error) and **run** (serve until the context is cancelled, then
      the existing graceful shutdown of the client, metrics, and
      health-endpoint servers in the current order and timeout).
- [ ] The application value exposes the client handler, the metrics
      collector, and the backend registry — exactly what the chaos tests read
      today. **No reload operation exists in this ticket** (T3 adds it).
- [ ] Build performs today's wiring unchanged: collector; registry with every
      backend's series seeded; circuit breaker installed as registry gate and
      registered as observer; selector from config; proxy with metrics and the
      latency, outlier, and circuit observers; active checker; health endpoint
      handler.
- [ ] `main` becomes flags → `probe` subcommand short-circuit → load →
      validate → build → run, sharing the SIGINT/SIGTERM context. Startup log
      lines (`starting`, `l7LoadBalancer starting`, `health checker started`,
      `metrics endpoint started`, `health endpoint started`) are unchanged.
- [ ] The chaos-test assembly delegates to build; every chaos test passes
      with unchanged assertions.
- [ ] A build-level test proves the handler routes to configured backends and
      every backend's seeded gauge series exists.
- [ ] `AGENTS.md`'s package dependency graph and directory map show
      `internal/app` above proxy/health/circuit/metrics/balancer/backend/config,
      imported only by `main` and tests; graph stays acyclic.
- [ ] No ADR (package decision recorded in the PROGRESS entry and AGENTS.md).
- [ ] `make test`, `make test-race`, `go vet ./...`, `make fmt` clean; manual
      `make run` smoke recorded in the session log.
