# 07: LB startup runtime fields (S5.T5.6)

**What to build:** The load balancer's startup line reports its effective GOMAXPROCS and its Go version. The benchmark methodology can then show from observed values that CPU pinning took effect (Go derives GOMAXPROCS from CPU affinity) and which toolchain built the binary, which nothing else in the distroless image can report. This is the bundle's only change to load-balancer code.

**Blocked by:** 01

**Status:** ready-for-agent

Spec: `../spec.md` (Load-balancer startup fields)

- [ ] The **existing** `"l7LoadBalancer starting"` log call gains `gomaxprocs` (effective value, integer) and `go_version` (runtime version string). No new log statement.
- [ ] Both names are added to the canonical field vocabulary in the logger package's docs, one line of meaning each.
- [ ] No other log line, metric or behaviour changes. No Prometheus Go collector (rejected: it changes the metrics exposition and adds unrequested series).
- [ ] TDD, Red-first, through the existing built-binary seam (prior art: `TestBinaryLogsInjectedVersionAndCommit`). The test builds the binary, runs it with `GOMAXPROCS=2` in its environment and a temp config pointing at an `httptest` backend, reads the startup JSON line, then SIGTERMs the process. It asserts that `gomaxprocs` is 2 and that `go_version` is present and matches the toolchain's.
- [ ] `make test`, `make test-race`, `go vet ./...`, `make fmt` clean.
