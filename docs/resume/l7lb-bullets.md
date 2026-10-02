# L7 Load Balancer — Resume Bullets

**Generated:** 2026-09-30
**Repo state:** `3f275fb2c4b809fdb868680d740ea47bce5eab8f`, Sprint 5, last done S5.T5.5.2 (backend arrival counter); next S5.T5.6. The published benchmark run (S5.T6) has not happened yet.
**Evidence sourced from:** No benchmark result files. `bench/results/{core,protocol,failure}/` hold only `.gitkeep`, and the only vegeta output is an untracked 100-request smoke run (`bench/results/.tmp/smoke.json`), which is not a benchmark. The numbers below come from `docs/adr/0009-consistent-hash-bounded-loads-capacity-and-evidence.md` (hot-key test), `docs/architecture.md` §Sprint 4 and `docs/sessions/2026-09-27-opencode-4.md` (1-hour soak), `test/chaos/reload_test.go` and `test/chaos/sighup_e2e_test.go` (1,000 in-flight reload), `internal/metrics/metrics.go`, `Dockerfile`, `go.mod`, and a local `go test -cover ./...` plus `wc -l` run at the commit above.

---

## Version A — Impact-forward (4 bullets)

- Built an HTTP/2 L7 load balancer in Go with 4 selectors, incl. consistent
  hashing with bounded loads and power-of-two-choices on EWMA latency
- Held the busiest of 4 backends to 31% of a Zipf-skewed load vs 40% under
  naive consistent hashing, via bounded loads (Mirrokni et al., ε = 0.25)
- Shipped zero-downtime SIGHUP reload (atomic snapshot swap + per-backend
  drain); 1,000 in-flight requests survive a real-signal reload, 0 dropped
- Soaked 59.66M requests over 1 hour of cancellations, backend kills and 12
  reloads with no leak: goroutines 35→30, post-GC heap 930 KB→804 KB

---

## Version B — Depth-forward (6 bullets)

- Designed a Selector interface over httputil.ReverseProxy with 4 algorithms;
  P2C-EWMA avoids slow-but-healthy backends that count-based policies miss
- Composed active probes, passive outlier ejection and a 3-state circuit
  breaker; eligibility is healthy AND not-open, so no signal overwrites another
- Built SIGHUP reload as an atomic swap of an immutable snapshot diffed by
  (name, URL); removed backends drain in a bounded window, 0 of 1,000 dropped
- Made in-flight accounting exactly-once via a sync.Once body wrapper; ruled
  out retries, which double-count load and feed one failure to breakers twice
- Built a vegeta benchmark harness (constant-rate, no coordinated omission) for
  a 50-run matrix vs Nginx: 4 algorithms, 3 payload sizes, HTTP/1.1 and h2
- Shipped a static, digest-pinned distroless image with a self-probing
  HEALTHCHECK, 6 Prometheus metrics with chosen buckets, Grafana and pprof

---

## Version C — LinkedIn / portfolio long-form

l7LoadBalancer is a Layer 7 HTTP/1.1 and HTTP/2 load balancer written in Go on the standard library's reverse proxy, with five direct dependencies. It adds what an nginx config can't show: four selection algorithms, health checks and a circuit breaker that don't fight each other, and a live config reload that drops nothing. Every non-trivial choice is written down in one of 20 architecture decision records, from the hash pipeline to why the balancer never retries a request.

- Kept 59.66M requests over a 1-hour soak leak-free through client cancellations, backend kills and 12 live reloads (goroutines 35→30, heap 930 KB→804 KB)
- Reloaded config with a real SIGHUP while 1,000 requests were held in flight: all 1,000 returned 200, and the removed backend got no new traffic
- Held the busiest backend to 31.3% of a skewed workload against 40.0% for naive consistent hashing, using bounded loads with ε = 0.25 and live in-flight counts as the load signal

**Tech stack:** Go 1.25, net/http, httputil.ReverseProxy, x/net/http2 (h2c), TLS + ALPN, log/slog, Prometheus, Grafana, Docker (distroless), vegeta, Nginx 1.27 (comparison)

**Links:** https://github.com/DMJain/l7LoadBalancer · no live demo (see warnings)

---

## Notes for Darshan

- **Numbers you can defend cold**:
  - Hot-key test (`TestConsistentHashHotKeyRebalances`, seed 2253): 10,000 Zipf-skewed requests over 100 client IPs and 4 backends. Busiest backend is 4,005 (40.0%) with naive CH vs 3,126 (31.3%) with bounded loads. The cap is `ceil(10000/4 × 1.25) = 3125`, and bounded lands one over it because of the `<=` admission rule. Across 60 seeds naive ranges 29.9–51.3% and bounded tops out at 31.3%. The exhaustion fallback never fired in 600,000 selections.
  - Reload: 1,000 in-flight, 0 dropped. There are two tests. `TestChaosReloadDrainExitCriterion1000` runs in process. `TestChaosSighupReloadZeroDrop1000` runs the built binary and sends it a real `kill -HUP`.
  - Soak (`make soak`, 3,616.5 s): 59.66M requests, 7,175 cancellations, 2,789 backend failures, 12 reloads. Goroutines went 35→30 and post-GC heap 930 KB→804 KB. The committed tolerances are +10 goroutines and +8 MB. It runs in process over loopback, so it is not a throughput benchmark. Don't quote it as req/s.
  - Code: 4,837 lines of production Go (`internal/` + `cmd/`) and 13,660 lines of tests. Coverage is 100% for circuit, config, logger and metrics, 94.7–97.7% for backend, balancer, health and proxy, and 84.6% for app.
  - Design constants: P2C records a fixed 2 s failure penalty into EWMA (ADR-0010); the ring uses 150 vnodes per backend and an FNV-1a-64 → fmix64 hash (ADR-0008).
- **Claims that need Sprint 5 completion to be fully honest**:
  - Version B bullet 5 (benchmark harness) describes a built harness with no published results. Once S5.T6 and S5.T10.4 land, rewrite it with real peak req/s and p99 vs Nginx, and say plainly where Nginx wins.
  - Any "within X% of Nginx" line needs S5.T6. None is included.
  - "One-command reproducible" (`make bench-repro`, S5.T12) and "pinned CPU" (S5.T5.7.1) are left out on purpose. Both are still TODO.
  - The Docker image size is left out. The Docker daemon wasn't running, so it couldn't be measured.
- **Suggested interview questions that map to each bullet**:
  - A1: "Why P2C-EWMA over least-connections? When would least-conn beat it?"
  - A2: "What does ε trade off? Why in-flight count rather than request rate as the load signal? What happens to cache affinity when the cap binds?"
  - A3 / B3: "Why an in-process swap rather than SO_REUSEPORT handoff? What happens to a request that's mid-body on a removed backend when the drain window expires?"
  - A4: "How do you know it didn't leak? Why did the heap assertion get dropped under -race?"
  - B1: "Why is Selector defined at the producer, against Go convention?" (ADR-0002)
  - B2: "Why doesn't the circuit breaker mark a backend unhealthy? What feedback loop does that prevent?" (ADR-0011)
  - B4: "Why no retries at all, even for idempotent GETs? How do you tell a client disconnect apart from a backend failure?" (ADR-0017, ADR-0018)
  - B5: "Why vegeta over wrk? What is coordinated omission? How is 'peak' defined? Is the Nginx comparison fair for consistent-hash and P2C?"
  - B6: "How does distroless run a health check with no shell? Why those histogram buckets? What's the label cardinality?"
- **Bullets I would NOT include on the resume even though the code supports them**:
  - "95–100% test coverage / 13.6k lines of tests": this is table stakes and reads junior. Mention it only if someone asks.
  - "Written with strict TDD": process, not outcome.
  - Proxy micro-benchmark (~35 µs/op, 102 allocs/op, from `docs/sessions/2026-09-26-opencode.md`): it runs through an httptest backend in process, so it's easy to misread as proxy overhead.
  - "YAML config with `${VAR}` interpolation and redaction", "slow-loris read timeout", "tuned http.Transport pool": all correct, but any competent proxy has them. Keep them for follow-up answers.
  - "20 ADRs" as its own bullet: it's better as a line in the Version C paragraph than as a resume claim.

---

## Warnings

- ⚠ **Benchmark data is missing for every algorithm.** `bench/results/` has no committed runs. There is no throughput, latency percentile, or Nginx comparison for round-robin, least-connections, consistent-hash or P2C-EWMA, on any payload size or protocol. That's why no bullet carries a req/s or p99 number.
- ⚠ **Claims depending on unfinished Sprint 5 tasks:** B5 (harness, results pending S5.T6 / S5.T10.x), plus reproducibility and CPU pinning (S5.T12, S5.T5.7.1), which are left out.
- ⚠ **No live deployment exists.** ADR-0019 makes the container image the deployment target, and the repo has no fly.toml, k8s manifest, or public URL. The repo's S5.T13 is the proposed README diagram ticket, not a deployment. Leave out any "live demo" claim.
- ⚠ **Not verified:** whether `github.com/DMJain/l7LoadBalancer` is public (taken from `git remote`). Check before putting the link on a resume.
- ⚠ **Docker image size** couldn't be measured because the Docker daemon was down, so it's left out.
