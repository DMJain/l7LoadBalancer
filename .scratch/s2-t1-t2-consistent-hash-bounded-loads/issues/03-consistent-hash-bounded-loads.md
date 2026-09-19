# 03: ConsistentHashBoundedLoads Selector and Config Wiring

**What to build:** The operator-facing `consistent_hash` algorithm, end-to-end: session-sticky routing with a per-backend load ceiling, selectable in YAML exactly like `round_robin` and `least_conn` are today. Ships with same-repo evidence — not just a citation — that it actually rebalances a hot key naive consistent hashing wouldn't.

**Blocked by:** 01, 02

**Status:** ready-for-agent

- [x] Exported `ConsistentHashBoundedLoads` implements `balancer.Selector`; wired into `balancer.NewFromConfig`'s switch and `config.implementedAlgorithms` for the `consistent_hash` identifier — an operator can select it via YAML and `make run` routes traffic accordingly
- [x] Same ring and candidate iterator as ticket 02's selector; same `RemoteAddr`-derived hash key
- [x] Per-candidate skip logic checks two conditions: `IsHealthy()` and current load against capacity
- [x] Load = `ActiveConns()`, averaged only over currently-healthy backends
- [x] Capacity = `max(1, ceil(avg_active * 1.25))` (ε = 0.25) — the floor of 1 exists so an idle system's first request isn't rejected against a zero-sized cap
- [x] Walk capped at one full ring traversal; if exhausted, falls back to the least-loaded candidate seen during that traversal (expected to never trigger given the capacity floor — a defensive path, not a normal-operation branch)
- [x] Returns `ErrNoHealthyBackends` only when the healthy set is empty — never a distinct "over capacity" error
- [x] Virtual-node count, epsilon, and hash-key source remain unexported Go constants — no `config.Config` schema change
- [x] Test: pre-seeded `ActiveConns` values that push a backend over capacity cause it to be skipped in favor of an under-capacity candidate
- [x] Test: an idle system (`avg_active = 0`) does not reject a backend's first request
- [x] Test: a backend going unhealthy mid-run is skipped and resumes being chosen once healthy again
- [x] Test: empty healthy set → `ErrNoHealthyBackends`
- [x] Hot-key comparative test, fixed deterministic seed: a Zipfian-skewed (skew ≈ 1.0) 10,000-request workload over 100 octet-diverse synthetic client IPs across 4 backends, constructing both ticket 02's selector and this one over the identical request stream. Naive's busiest-backend share asserted inside [3,800, 4,200] of 10,000; bounded-loads' busiest-backend share asserted inside [3,100, 3,130] of 10,000 (the window accounts for the capacity check's `<=`-admission rule legitimately landing one request over the computed cap)
- [x] Offline reproducer, excluded from normal `go test` runs via a build tag, sweeping many seeds of the same workload shape; its summary distribution (minimum, median, p90, maximum) is what the ADR cites as the general claim, with the checked-in test's fixed seed as the specific, CI-safe instance of it
- [x] ADR recording epsilon, the load metric, the capacity formula, the probing/fallback behavior, and the hot-key evidence — citing the reproducer's summary numbers and confirming the fallback never fired across every swept seed
