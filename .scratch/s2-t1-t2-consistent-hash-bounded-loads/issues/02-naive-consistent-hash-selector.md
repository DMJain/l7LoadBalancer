# 02: naiveConsistentHash Selector

**What to build:** A real, working, health-aware consistent-hash `Selector` — session-sticky routing by client IP, with no notion of backend load — built directly on the ring primitive. Proves session affinity is correct in isolation before capacity logic is layered on top, and gives ticket 03 a real comparator to demonstrate the bounded-loads decision against, rather than an assertion backed only by a citation.

**Blocked by:** 01

**Status:** ready-for-agent

- [x] Unexported `naiveConsistentHash` type implements `balancer.Selector`, with a compile-time `var _ Selector = (*naiveConsistentHash)(nil)` assertion
- [x] Hash key: the request's `RemoteAddr`, with the port stripped
- [x] Walks the ring's candidate iterator, skipping any backend for which `IsHealthy()` is false, selecting the first healthy candidate found
- [x] Returns `ErrNoHealthyBackends` if the walk exhausts every backend without finding a healthy one
- [x] Never appears in `balancer.NewFromConfig`'s switch or in `config.implementedAlgorithms` — not reachable via any config value
- [x] Doc comment on the type states why it's deliberately unwired, references ticket 01's ADR, and explains why it isn't named `ConsistentHash` — that name is reserved, in a reader's expectation, for whatever `consistent_hash` in config actually maps to (ticket 03's `ConsistentHashBoundedLoads`)
- [x] Test: the same client IP repeatedly selects the same backend
- [x] Test: a backend going unhealthy mid-run is skipped, and resumes being chosen once healthy again
- [x] Test: empty healthy set → `ErrNoHealthyBackends`
