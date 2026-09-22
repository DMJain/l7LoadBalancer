# Sprint 3 retrospective — Resilience & Observability

Written by S3.T13 (issue 07 of `.scratch/s3-t10-t13-demo-stack/`), 2026-09-23,
mirroring the S2.T8 close-out shape. Sprint 3 ran across four scoping passes:
`.scratch/s3-t1-t3-health-passive-circuit/` (health/circuit), then
`.scratch/s3-t4-t7-observability/` (metrics/logging/dashboard), then
`.scratch/s3-t6-5-t8-t9-chaos/` (the reinstatement-gate fix and the chaos
tests), then `.scratch/s3-t10-t13-demo-stack/` (the container demo stack and
this retro). The design decisions this document audits are recorded in the
four bundle specs' Implementation Decisions sections; the per-session detail
is in `docs/sessions/`.

Companion artifacts: `docs/adr/0011`–`0014`, the
[ADR-0005 2026-09-22 amendment](adr/0005-scope-of-production-grade.md#amendment-2026-09-22--demo-stack-containerization-does-not-settle-deployment-target),
and the [ADR-0011 2026-09-22 amendment](adr/0011-health-passive-outlier-and-circuit-breaker-composition.md#amendment-2026-09-22-reinstatement-gate-uses--not-).

## 1. Deliverables shipped

Every Sprint 3 task with a one-line summary and the commit that landed it.
Sub-tickets are indented under the ticket they prefactor; "claim" commits
(`chore(progress): start …`) are omitted in favour of the commit that carries
the implementation and its close-out.

| Task | Summary | Commit(s) |
|---|---|---|
| S3.T0.1 | Split `Backend.SetHealthy(bool)` into intent-named `MarkHealthy()`/`MarkUnhealthy()` (ADR-0011 decision 2). | `514965d`, review `55b4da3` |
| S3.T0.2 | Generalize the proxy's hardcoded `RecordLatency` call into the `RoundTripObserver` fan-out + additive `RegisterObserver` (ADR-0011 decision 9). | `bf02777`, review `92c3b71` |
| S3.T0.3 | Freeze the global `health.probe_interval`/`probe_timeout`/`circuit.cooldown` config schema with defaults + non-positive rejection (ADR-0011 decision 10). | `2c8069e`, review `90dc31b` |
| S3.T1 | `health.Checker`: one probe goroutine per backend, dedicated client, N-consecutive-failure / M-consecutive-success state machine on `sigCtx` (ADR-0011 decisions 2, 10, 11, 13). | `586e653`, review `a1c6d49` |
| S3.T2 | `health.OutlierDetector`: count-based sliding window registered into the observer fan-out, passive eject exactly once per episode, recovery only via active probe (ADR-0011 decisions 3, 8, 9, 12). | `af91d01`, review `26317c3` |
| S3.T3 | Per-backend circuit breaker: CAS-guarded state on `Backend`, policy in `internal/circuit`, Registry-gated admission after `Select`, `Healthy()`→`Selectable()` rename (ADR-0011 decisions 1, 5–8; ADR-0012). | `8bba09a`, ADR `079c901`, review `e781d0a` |
| S3.T4 | `internal/metrics` becomes a real leaf-only Prometheus `Collector` on a private registry; `metrics.listen` config; `/metrics` on its own server (ADR-0013 decisions 1–8). | `e022763`, ADR `ad24c79`, review `82e1b4e` |
| S3.T5.1 | `internal/logger` gets its first test file: `New` level thresholds and JSON output shape. | `9c280b4`, review `2473620` |
| S3.T5.2 | Freeze the closed `event`/`reason` transition-log vocabularies in `internal/logger` (ADR-0013 decision 12). | `820e4c0`, tests `d8410c2`, review `c66c5ad` |
| S3.T5.3 | Edge-triggered health transition log lines for the active checker and passive detector (ADR-0013 decision 11). | `4a84a36`, tests `d6f9644`, review `2e39077` |
| S3.T5.4 | `backend.CircuitTransition` + `circuit.Breaker` logging, one line per genuine circuit transition (ADR-0013 decision 10 and its 2026-09-21 amendment). | `4349300`, ADR `a6945c3`, tests `23754e6`, review `d6557a9` |
| S3.T6.1 | Whole-request counter + duration histogram pushed from `ServeHTTP`'s deferred completion hook, all four exit paths (ADR-0013 decision 14). | `f1de450`, tests `e678941`, review `3c7663b` |
| S3.T6.2 | `lb_active_connections` written at the exact `IncActive`/`DecActive` sites, seeded to `0` at startup (ADR-0013 decisions 7, 9). | `9d20be4`, tests `7cba958`, review `d4d4d0e` |
| S3.T6.3 | `lb_backend_healthy` driven by the same edge-triggered signals as the health log lines, seeded to `1` (ADR-0013 decisions 6, 15). | `0e2ccd8`, review `df44598`, `3538e78` |
| S3.T6.4 | `lb_circuit_state` driven by the `CircuitTransition` signal, seeded to `closed` (ADR-0013 decisions 5, 9, 15). | `caf2cb0` |
| S3.T7 | Five-panel Grafana dashboard + separate Prometheus/Grafana observability compose stack (ADR-0013 decision 17). | `74fc5ec`, review `859bdda`, `79f1659` |
| S3.T6.5 | Active-checker reinstatement gate `==` → `>=` to fix a bug in already-shipped S3.T1 code (ADR-0011 2026-09-22 amendment). | `301dd95`, tests `f3e6b4f`, `424e27c` |
| S3.T8 | `test/chaos/` harness + backend eviction/recovery chaos arcs + `docker stop` `eviction.sh` smoke. | `5d8ea77`, review `83d289e` |
| S3.T9 | Circuit trip / cooldown / half-open-trial chaos arcs + 5xx-injection `circuit.sh` smoke. | `9ecc5fc`, review `f8bf959` |
| S3.T10–T13.D0 | Amend `MILESTONES.md` with the T10–T13 deliverables and exit criteria before any of their code landed (spec D0). | `6a85c36`, `fec54e9`, `4d2208b` |
| S3.T12.0 | Health-endpoint prefactor APIs: `Checker.ProbeRoundComplete()`, `Collector.RecordProbe()`, `health_endpoint.listen` (spec D8, D9, D11). | `4c59bc5`, review `18c8d8f` |
| S3.T12 | `/livez`/`/readyz`/`/startupz` handlers + third listener + ADR-0014 (spec D2–D12). | `2c048ba`, tests `1806894`, ADR `b10fb80`, review `2629052` |
| S3.T10.0 | `l7lb probe <url>` subcommand + `main.version`/`main.commit` ldflags injection (spec D16, D17). | `010fc85`, tests `7f43cf0`, review `e3be04c` |
| S3.T10 | Multi-stage distroless LB Dockerfile, `.dockerignore` allowlist, baked `configs/docker.yaml`, OCI labels, ADR-0005 amendment (spec D13–D25). | `4a0917b`, review `f539249` |
| S3.T11 | Repo-root `docker-compose.yml`, `prometheus-stack.yml`, ADR-0013 decision 18 (spec D26–D38). | `c984aa2`, review `17020a4` |
| S3.T13 | This retro + additive `docs/architecture.md` update (spec D39–D44). | claim `fd7bb85`; the completion commit carries this file |

## 2. Deviations from MILESTONES/spec

Five known deviations, one paragraph each.

### 2.1 S3.T6.5 was inserted mid-sprint to fix a bug in already-shipped S3.T1 code

The reinstatement gate in `internal/health/checker.go` compared the
consecutive-successes accumulator with `== probeSuccessesBeforeHealthy`,
while passive outlier detection could eject a backend whose accumulator had
already crossed M — so the gate never fired again and the backend's
`lb_backend_healthy` gauge stayed pinned at `0` (both the
`health_reinstated` log line and the gauge write sat behind the equality)
until process restart, even though `IsHealthy()` read `true`. Sprint 3's
three-pass plan had no ticket for this: it was found while scoping the
T8/T9 chaos bundle, shipped first and alone as S3.T6.5, and recorded in
`PROGRESS.md` under "Sprint 3 — Resilience & Observability". The fix is
commit `301dd95` (`fix(health): reinstate passively-ejected backends via >=
success gate (S3.T6.5)`), with the Red-first regression test in `f3e6b4f`
and the ADR-0011 2026-09-22 amendment as the in-place record.

### 2.2 T8/T9 chaos-test assembly duplication, converging on Sprint 4's `Run(ctx, cfg)` seam

The external `package chaos_test` under `test/chaos/` composes the full
Sprint 3 stack (registry + selector factory + proxy + `health.Checker` +
outlier detector + `circuit.Breaker` + `metrics.Collector` + observer
registration + `Registry.SetCircuitGate`) through a local
`assemble(t, cfg)` helper that duplicates roughly twenty lines of
`main.go`'s wiring, because `main` has no programmatic entry point to call
from a test. Both chaos tickets explicitly recorded this as known debt and
pointed at the convergence path: when Sprint 4 introduces a
`Run(ctx, cfg)` seam for the SIGHUP reload work, `assemble()` should
converge with or be replaced by it. Citations: the S3.T8 two-axis review
notes and the S3.T9 two-axis review notes in
`docs/sessions/2026-09-22-opencode.md`; the `PROGRESS.md` entries for
S3.T8 and S3.T9; and the bundle spec's "Chaos test harness" decision
(`.scratch/s3-t6-5-t8-t9-chaos/spec.md`, "No `main.go` refactor into
`Run(ctx, cfg)` in this bundle").

### 2.3 Half-Open scan-promotion log gap is permanent by design

A Half-Open promotion whose CAS is won by the `Registry.Selectable()` read
path (via `Backend.CircuitOpen`) is never logged and never writes
`lb_circuit_state{half_open}`: `Selectable()` has no logger, and only
`CircuitAllow` can report a promotion it performed. This is
[ADR-0013 decision 13](adr/0013-observability-metrics-logging-and-integration.md),
recorded explicitly as an accepted limitation rather than left implicit.
It is stated here so its "known and accepted" status is unambiguous — it is
not debt to be paid down accidentally, and closing it needs a cross-package
plumbing change with its own ADR. It is pinned at two test layers
(`internal/circuit/metrics_test.go` and `test/chaos/circuit_test.go`), both
of which must change together if a future ticket closes it.

### 2.4 T10–T13 were mid-sprint scope additions; the amendment-first precedent they set

The four demo-stack tickets themselves (the containerized LB, the repo-root
compose, the health endpoint, and this retro) were not in the Sprint 3 plan
when the sprint opened. They were added mid-sprint, and the ordering chose
to amend `MILESTONES.md`'s Sprint 3 deliverables and exit criteria before
any of their code landed. That ordering is the precedent this retrospective
records for future sprints, verbatim: *"mid-sprint scope additions require
a `MILESTONES.md` amendment commit before any code lands, per S3.T10–T13's
amendment-first ordering (spec D0)."* The amendment landed as `6a85c36`
(`docs(milestones): add S3.T10–T13 deliverables and exit criteria`) before
S3.T12.0, the first code ticket of the bundle.

### 2.5 T9's three spec-to-implementation conflicts were adapted in tests, not production code

Tracing the frozen architecture before implementing S3.T9 surfaced three
acceptance bullets that are unreachable as literally written — a signed-off
owner decision, not a silent workaround. (1) `circuit_half_opened` and
`lb_circuit_state{half_open}` never fire through the proxy, because
`Selectable()` wins the lazy promotion CAS before `Allow` runs (ADR-0013
decision 13); the tests pin the gap instead of asserting the log line.
(2) The "503 via `Registry.Allow` denial during cooldown" is unreachable
because `Selectable()` filters open circuits; the reachable denial is
`ErrNoHealthyBackends`, which the test asserts instead. (3) The 20 ms probe
fast path would health-eject the 500-serving backend before a trial could
be taken, so T9 neutralizes the active checker with a one-hour
`probe_interval` (a config knob, no constant bent) and uses a single
backend to make the trial deterministic. The owner chose "adapt to real
behavior" over closing the gaps in production code (which would have needed
an ADR outside the bundle's scope). Citation:
`docs/sessions/2026-09-22-opencode.md`, S3.T9 sections "Spec/architecture
conflicts surfaced before implementation (owner-approved)" and "Review
(two-axis, post-implementation)"; the same three conflicts are recorded in
the `PROGRESS.md` S3.T9 entry.

## 3. ADR sweep

Audit-only. Method: for each Sprint 3 ADR, `git log --grep` / `grep -r`
confirm the code exists, and the code was read against the ADR's decision
text. Any row that is not "code exists and matches" must carry an explicitly
justified deferral; no blank cells, no undeferred gaps. The sweep also runs
in the inverse direction: every non-trivial Sprint 3 decision from the four
bundle specs' Implementation Decisions sections must be backed by an ADR or a
documented `AGENTS.md` / inline-comment reference.

### 3.1 Every Sprint 3 ADR has shipped code, and the code matches

| ADR | Corresponding code | Code matches ADR text? |
|---|---|---|
| [ADR-0004](adr/0004-reject-unimplemented-algorithms-in-validate.md) — reject unimplemented algorithms in `Validate` | `internal/config/config.go`: `implementedAlgorithms` + `Validate` | Yes. Set is now all four identifiers, per decision 4's "one-line addition made atomically with the sprint"; vocabulary/acceptance distinction holds. |
| [ADR-0005](adr/0005-scope-of-production-grade.md) — scope of "production-grade" | None (claims-scoping document, no code by design) | N/A-by-design. Re-read against Sprint 3's shipped scope: still accurate; no claimed capability exceeded its exclusions. |
| [ADR-0005 amendment (2026-09-22)](adr/0005-scope-of-production-grade.md#amendment-2026-09-22--demo-stack-containerization-does-not-settle-deployment-target) — demo-stack containerization does not settle the deployment target (ticket 05) | `Dockerfile`, root `docker-compose.yml` | Yes. Both exist as demo artifacts; `MILESTONES.md` still defers the deployment-target ADR to Sprint 4. |
| [ADR-0006](adr/0006-backend-sethealthy-amends-adr-0002.md) — add `Backend.SetHealthy` | `internal/backend/backend.go`: `MarkHealthy`/`MarkUnhealthy` | Superseded, documented: ADR-0011 decision 2 split `SetHealthy` in place; ADR-0006's contract is carried by the two named methods. Not a gap. |
| [ADR-0007](adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md) — proxy lifecycle / exactly-once decrement | `internal/proxy/proxy.go`: `reqState.release()`, `releaseBody.Close()` | Yes. `sync.Once`-guarded release unchanged; 100-concurrent leak test still green. |
| [ADR-0008](adr/0008-consistent-hash-ring-pipeline-and-vnode-layout.md) — ring pipeline, vnode key order, vnode count | `internal/balancer/ring.go` | Yes. FNV-1a-64 → `fmix64`, `index:name` keys, 150 vnodes, `iter.Seq` walk. |
| [ADR-0009](adr/0009-consistent-hash-bounded-loads-capacity-and-evidence.md) — bounded-loads ε, load metric, capacity, evidence | `internal/balancer/consistent_hash.go` | Yes. ε = 0.25, capacity `max(1, ceil(avg × 1.25))`, `<=` admission, one-pass walk. |
| [ADR-0010](adr/0010-p2c-ewma-backend-latency-state-cold-start-and-failure-penalty.md) — P2C-EWMA latency state, cold start, failure penalty | `internal/backend/backend.go` (`RecordLatency`/`EWMALatency`), `internal/balancer/p2c_ewma.go`, `internal/proxy/proxy.go` | Yes. α = 0.1, direct-set cold start, fixed 2 s failure penalty, unconditional recording. |
| [ADR-0011](adr/0011-health-passive-outlier-and-circuit-breaker-composition.md) — health / passive / circuit composition | `internal/health/{checker,outlier}.go`, `internal/backend/{backend,registry}.go`, `internal/circuit/circuit.go`, `internal/proxy/proxy.go` | Yes. Two-gate eligibility, `Mark*` split, single-path recovery, always-on, `Selectable()`, lazy promotion, same-signal failure counters, unconditional fan-out, config/constant split, 2xx-only probing, 5xx ∪ transport signal, `sigCtx`. |
| [ADR-0011 amendment (2026-09-22)](adr/0011-health-passive-outlier-and-circuit-breaker-composition.md#amendment-2026-09-22-reinstatement-gate-uses--not-) — reinstatement gate `>=` (S3.T6.5) | `internal/health/checker.go` | Yes. `p.successes >= probeSuccessesBeforeHealthy` with the `!IsHealthy()` guard; regression test `f3e6b4f`. |
| [ADR-0012](adr/0012-circuit-gate-interface-and-backend-state-methods.md) — circuit gate interface, Registry admission, Backend state API | `internal/backend/backend.go`, `internal/backend/registry.go`, `internal/circuit/circuit.go`, `internal/proxy/proxy.go` | Yes. `CircuitGate` in `backend`; `SetCircuitGate`/`Allow`; half-open selectable; single `atomic.Pointer[circuitSnapshot]` CAS; policy passed per call; ignore-while-open; `circuitFailuresBeforeOpen = 3`. |
| [ADR-0013](adr/0013-observability-metrics-logging-and-integration.md) — observability: metrics, logging, integration | `internal/metrics/metrics.go`, `internal/logger/vocab.go`, `internal/health/*`, `internal/circuit/circuit.go`, `internal/proxy/proxy.go`, `cmd/l7LoadBalancer/main.go`, `deployments/docker/observability/**` | Yes. Leaf push-only collector on a private registry; whole-request hook; label-enum circuit gauge with unconditional zeroing; `metrics.listen`; seeded gauges; `CircuitTransition`; equality-gated health logging; `event`/`reason` vocabularies. |
| [ADR-0013 2026-09-21 amendment](adr/0013-observability-metrics-logging-and-integration.md#amendment--2026-09-21-circuittransition-gains-a-fifth-value) — `CircuitTransition` gains `Reopened` (S3.T5.4) | `internal/backend/backend.go`: `CircuitNoChange`/`Opened`/`Reopened`/`Closed`/`HalfOpened` | Yes. Five values, 1:1 with logged reasons; `circuit_half_opened` only from `CircuitAllow`'s own promotion. |
| [ADR-0013 decision 18 (2026-09-22)](adr/0013-observability-metrics-logging-and-integration.md#repo-root-demo-stack-docker-composeyml) — repo-root demo stack (ticket 06) | `docker-compose.yml`, `deployments/docker/observability/prometheus/prometheus-stack.yml`, `prometheus.yml` header comment | Yes. Additive; `prometheus` service-name lock; shared Grafana provisioning; published `8080`/`8081`/`3000`; mixed health-gating chain; `restart: unless-stopped`; config bind-mount. |
| [ADR-0014](adr/0014-health-endpoint-contract-and-probe-semantics.md) — health endpoint contract and probe semantics | `internal/health/endpoint.go`, `internal/health/checker.go` (`ProbeRoundComplete`), `internal/metrics/metrics.go` (`RecordProbe`), `internal/config/config.go` (`health_endpoint.listen`), `cmd/l7LoadBalancer/main.go` (third server) | Yes. Separate listener; three K8s-native paths; unconditional `/livez`; one-shot `/startupz`; live-selectable `/readyz`; empty-selectable → 503; pinned envelope; `lb_health_probe_total`; orchestrator mapping table. |

No row is blank, and no row carries an undeferred gap. The only
"does-not-match-the-original-name" row is ADR-0006, superseded in place by
ADR-0011 decision 2 — documented, not a gap.

### 3.2 Inverse coverage: every non-trivial Sprint 3 decision is backed

| Bundle spec | Decision area | Backing reference |
|---|---|---|
| `s3-t1-t3-health-passive-circuit` | Active probe mechanics (dedicated client, 2xx-only, constants, `sigCtx`, `Mark*` split), config-vs-constant split | ADR-0011 decisions 2, 10, 11, 13; inline comments in `internal/health/checker.go` |
| `s3-t1-t3-health-passive-circuit` | Passive window/threshold, single-path recovery, observer fan-out, 5xx ∪ transport signal | ADR-0011 decisions 3, 8, 9, 12 |
| `s3-t1-t3-health-passive-circuit` | Circuit state on `Backend`, CAS snapshot, lazy promotion, `Allow` after `Select`, `Selectable()` rename, cooldown config | ADR-0011 decisions 1, 5–8; ADR-0012 decisions 1–6 |
| `s3-t4-t7-observability` | Leaf collector / private registry / instrument set / circuit-setter zeroing / `metrics.listen` / `promhttp` server | ADR-0013 decisions 1–8 |
| `s3-t4-t7-observability` | `CircuitTransition`, health `==` logging gate, `event`/`reason` vocabularies, permanent scan-promotion gap | ADR-0013 decisions 10–13 |
| `s3-t4-t7-observability` | Whole-request hook, constructor threading, startup seeding, balancer untouched | ADR-0013 decisions 14–16 |
| `s3-t4-t7-observability` | Five-panel dashboard + separate observability compose | ADR-0013 decision 17 |
| `s3-t6-5-t8-t9-chaos` | T6.5 `>=` reinstatement fix | ADR-0011 2026-09-22 amendment |
| `s3-t6-5-t8-t9-chaos` | `test/chaos/` external package, `assemble` duplication, `flippableBackend`, arc timing, smoke scripts | Test-only decisions (no ADR required); documented in the bundle spec, `AGENTS.md` Step 2.5 precedent, and the S3.T8/T9 session-log review notes |
| `s3-t10-t13-demo-stack` | D0/D1 amendment-first ordering, sequential gating | Process, not design: recorded in `PROGRESS.md` (S3.T10–T13.D0) and this retro §2.4; no ADR |
| `s3-t10-t13-demo-stack` | D2–D12 health endpoint | ADR-0014 decisions 1–10 |
| `s3-t10-t13-demo-stack` | D13–D25 Dockerfile, probe subcommand, OCI labels, baked config | Inline `Dockerfile` comments + ADR-0005 2026-09-22 amendment; no T10 ADR by design (D25) |
| `s3-t10-t13-demo-stack` | D26–D38 root compose | ADR-0013 decision 18 |
| `s3-t10-t13-demo-stack` | D39–D44 the retro and architecture update | This document; no ADR by design (D44) |

Result: no undeferred gap. The one deliberate no-ADR category is test-only
and process decisions, each documented in the bundle spec, `PROGRESS.md`,
`AGENTS.md`'s Step 2.5 guidance, or the session logs, matching how Sprint 2's
tracking-shape deviations were handled.

## 4. Architectural gaps left open

Known and accepted, not silently deferred:

- **Half-Open scan-promotion log gap.** A promotion won by the
  `Registry.Selectable()` read path is never logged and leaves
  `lb_circuit_state` at `open` until the trial resolves. Documented by
  ADR-0013 decision 13; pinned at two test layers. Permanent this sprint.
- **Deployment-target ADR still deferred.** Bare binary vs. Docker vs.
  Kubernetes remains open per ADR-0005 (and its 2026-09-22 amendment, which
  explicitly states the demo stack does not settle it). Sprint 4/5 will
  decide it, informed by the reload and connection-lifecycle work.
- **Two manual-verification-pending shell smokes.** The end-to-end
  `docker compose up` execution of `deployments/docker/chaos/eviction.sh`
  and `deployments/docker/chaos/circuit.sh` was never run (no Docker daemon
  in the implementing sessions), recorded as `[MANUAL VERIFICATION PENDING]`
  per the S3.T7 precedent. Their in-process equivalents (`test/chaos/`) do
  run green under `-race`.

## 5. Sprint 4 handoff

Starting debt inventory for Sprint 4's kickoff, in the spec's order:

- **The `Run(ctx, cfg)` seam refactor** and the chaos-test-assembly
  convergence it unlocks. `test/chaos/assemble(t, cfg)` currently duplicates
  `main.go`'s wiring; the seam should converge with or replace it (§2.2).
- **The Half-Open scan-promotion log gap**, pinned at two test layers
  (`internal/circuit/metrics_test.go`, `test/chaos/circuit_test.go`) per
  ADR-0013 decision 13; closing it needs a cross-package plumbing change and
  its own ADR, and both assertions must change together.
- **The deployment-target ADR still deferred** per ADR-0005 and its
  2026-09-22 amendment.
- **The two manual-verification-pending shell smokes** (`eviction.sh`,
  `circuit.sh`) awaiting a Docker daemon.
- **Test-coverage gaps surfaced by the ADR sweep:** none beyond the known
  items above. The sweep found every Sprint 3 ADR backed by shipped code and
  every non-trivial Sprint 3 decision backed by an ADR or a documented
  reference.

Also carried forward by `MILESTONES.md`'s Sprint 4 scope, independent of
this retro: zero-downtime SIGHUP reload, connection-lifecycle hardening and
the `pprof` goroutine-leak audit, connection-pool tuning, the retry-policy
ADR, and the deployment-target ADR.
