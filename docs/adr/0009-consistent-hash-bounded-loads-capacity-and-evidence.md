# ADR-0009: Consistent-hash bounded loads: epsilon, load metric, capacity formula, and probing/fallback

**Status:** Accepted. Amended by ADR-0011 (load is averaged over `Registry.Selectable()`, which also excludes open circuits) and ADR-0015 (the walk also skips backends that a reload removed).

## Context

A hash ring has no idea of load. A popular key sends all its traffic to the one backend that owns its ring position. `ConsistentHashBoundedLoads` (the `consistent_hash` algorithm) adds a load limit on top of the ring from ADR-0008. This ADR records the limit, the load metric, the capacity formula, the fallback, and test evidence from this repository.

## Decision

1. **ε is 0.25**, the unexported constant `capacityEpsilon`. This is the value in Mirrokni, Thorup and Zadimoghaddam (2016), "Consistent Hashing with Bounded Loads". It is not a config field.
2. **Load is `Backend.ActiveConns()`**, the live in-flight count, averaged over the selectable set. It is not a lifetime counter. The selector only reads `ActiveConns`. The proxy owns the bookkeeping, as for `LeastConnections`.
3. **Capacity is `max(1, ceil(avg_active * (1 + ε)))`.** A candidate is admitted when `ActiveConns() <= capacity`. The floor of 1 matters when the system is idle: `avg_active = 0` would otherwise reject every first request. Because of `<=`, a backend can hold one request more than the computed cap.
4. **One pass checks both conditions.** The walk uses the ring's candidate iterator and skips a candidate that is unhealthy, removed, or over capacity. The iterator yields each backend once and stops, so no counter is necessary.
5. **The fallback is defensive.** If the walk admits no candidate, `Select` returns the least-loaded healthy candidate it saw. With a non-empty selectable set this cannot happen: the mean is always `<= ceil(mean * (1 + ε))`, so the least-loaded backend is always admissible. The branch turns a broken invariant into a bounded choice instead of a nil return.
6. **`ErrNoHealthyBackends` has one cause: an empty selectable set.** There is no "over capacity" error, because decisions 3 and 5 make that state impossible. All selectors share one error contract, and the proxy maps the sentinel to 503.
7. **The hash key is `r.RemoteAddr` without the port**, from the shared `requestHashKey` (see `CONTEXT.md`, "Hash key"). The selector does not read `X-Forwarded-For`. Behind another proxy, the key is that proxy's IP.
8. **`consistent_hash` maps to this selector** in `config.implementedAlgorithms` and `balancer.NewFromConfig` (ADR-0004). `naiveConsistentHash` is never wired to config.

## Evidence

**Fixed-seed test.** `TestConsistentHashHotKeyRebalances` sends one deterministic Zipf workload (skew ≈ 1.0, 10,000 requests, 100 client IPs, 4 backends) through both selectors. In the bounded run, load only goes up: requests never finish, which is the worst case for a hot key. With seed 2253 the cap is `ceil(10000 / 4 * 1.25) = 3125`:

| Selector | Busiest backend | Share |
|---|---|---|
| `naiveConsistentHash` | 4,005 | 40.0% |
| `ConsistentHashBoundedLoads` | 3,126 | 31.3% |

The bounded busiest backend is one over the cap, as decision 3 predicts. The test asserts the windows `[3800, 4200]` (naive) and `[3100, 3130]` (bounded). The generator in `hotkey_test.go` defines what a seed produces. A change to its random draws changes the result.

**Seed sweep.** `TestHotKeySeedDistribution` runs only with the `offline` build tag:

```
go test -tags offline -run TestHotKeySeedDistribution -v ./internal/balancer/
```

Busiest-backend share over seeds 1–60:

| Selector | min | p10 | median | p90 | max | mean |
|---|---|---|---|---|---|---|
| naive | 29.9% | 34.0% | 40.0% | 47.8% | 51.3% | 40.4% |
| bounded | 30.0% | 31.2% | 31.3% | 31.3% | 31.3% | — |

Naive hashing puts about 30–51% of a skewed workload on one backend. Bounded loads holds the busiest backend at 3,120–3,126 of 10,000 when the cap binds. On seeds 14 and 52 the first-choice load is already under the cap, so bounded loads matches naive. The cap is a ceiling, not a target. Across all 600,000 selections the fallback never ran.

## Consequences

- A hot key cannot overload one backend. Each backend stays within `(1 + ε)` of the mean.
- No new dependency, config field or per-backend state.
- `Select` scans the selectable snapshot once per call. The scan is O(backends) with atomic reads.
- The limit reacts to in-flight load, not to latency. `p2c_ewma` handles latency (ADR-0010).
- A lifetime counter was not used. It never goes down, so the cap stops matching real load.
- A configurable ε was not added. No operator needs it.
- A ring rebuild for a hot backend was not used. It moves every key and loses affinity. Bounded loads moves only the overflow.
