# 05: Backend logging switch (S5.T5.5.1)

**What to build:** The dummy backend's per-request log line can be switched off, and the bench stack switches it off. At tens of thousands of requests per second, per-request JSON on stdout through Docker's log driver could make the *backends* the throughput ceiling, and then both competitors would report the same number for a reason unrelated to either. The demo stack keeps logging on.

**Blocked by:** 01

**Status:** ready-for-agent

Spec: `../spec.md` (Dummy-backend instrumentation)

- [ ] A `LOG_REQUESTS` environment variable, parsed with the same strictness as `SLEEP_MS` and `FAIL_RATE`: unset or empty → on; `true` or `false` accepted; any other value → startup error naming the variable.
- [ ] When off, no per-request log line is written on **any** path, `/health` included.
- [ ] The startup log line records the switch's value.
- [ ] The bench compose sets it off on all four backends. The repo-root demo stack is unchanged (default on).
- [ ] TDD, Red-first, through the existing handler-constructor seam driven by `httptest` (prior art: `TestHandlerEndpoints`, `TestTLSFilesFromEnv`): table-driven parsing (unset / empty / true / false / invalid), and no output written to a captured logger when off, across the payload, default and `/health` paths.
- [ ] `make test`, `make test-race`, `go vet ./...`, `make fmt` clean. `docker compose config` clean.
