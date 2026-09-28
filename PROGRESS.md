# PROGRESS

Live state of the project. Every agent updates this file per the protocol in `AGENTS.md`.

## Current status

**Sprint 1 complete.** S1.T0 through S1.T10 (including S1.T0.5) are all [DONE].

**Sprint 2 complete.** All four algorithms are implemented and config-selectable: S2.T1.1 (ring), S2.T1.2 (`naiveConsistentHash` comparator), S2.T2 (`ConsistentHashBoundedLoads`), S2.T3 (`PowerOfTwoChoicesEWMA`), closed out by S2.T8 (retro / architecture close-out: deviations audit, ADR sweep, architecture-doc updates).

**Sprint 3 complete.** All of Sprint 3 (S3.T0.1–T13, including T6.5 and the four demo-stack tickets T10–T13) is [DONE]. Scoped in four passes: `.scratch/s3-t1-t3-health-passive-circuit/` (one spec + six tickets), `.scratch/s3-t4-t7-observability/` (one spec + ten tickets), `.scratch/s3-t6-5-t8-t9-chaos/` (one spec + three tickets: the reinstatement-gate fix plus the T8/T9 chaos tests), and `.scratch/s3-t10-t13-demo-stack/` (one spec + seven tickets: this MILESTONES amendment, the T12 prefactors + handlers, the T10 probe subcommand + Dockerfile, the T11 root compose, and the T13 retro). Done: S3.T0.1 (`MarkHealthy`/`MarkUnhealthy`), S3.T0.2 (round-trip observer fan-out), S3.T0.3 (probe/cooldown config schema), S3.T1 (active health checks), S3.T2 (passive outlier detection), S3.T3 (circuit breaker + `Registry.Selectable()` rename), S3.T4 (metrics package + `metrics.listen` endpoint), S3.T5.1 (logger test coverage), S3.T5.2 (`event`/`reason` transition vocabulary), S3.T5.3 (health transition log lines), S3.T5.4 (circuit transition log lines), S3.T6.1 (whole-request counter + histogram wired into the proxy), S3.T6.2 (active-connections gauge + its startup seeding), S3.T6.3 (backend-healthy gauge + its startup seeding), S3.T6.4 (circuit-state gauge + its startup seeding), S3.T7 (Grafana dashboard + Prometheus/Grafana observability demo stack), S3.T6.5 (active-checker reinstatement gate `==` → `>=`), S3.T8 (backend eviction/recovery chaos test + `docker stop` smoke), S3.T9 (circuit-breaker trip/half-open chaos test + 5xx-injection smoke), S3.T10–T13.D0 (MILESTONES.md amendment), S3.T12.0 (health-endpoint prefactor APIs: `ProbeRoundComplete`/`RecordProbe`/`health_endpoint.listen`), S3.T12 (health endpoint handlers + third listener + ADR-0014), S3.T10.0 (probe subcommand + version/commit injection), S3.T10 (multi-stage distroless Dockerfile + `.dockerignore` allowlist + baked `configs/docker.yaml` + OCI labels + ADR-0005 amendment), S3.T11 (repo-root `docker-compose.yml` + `prometheus-stack.yml` + ADR-0013 decision 18), S3.T13 (Sprint 3 retro + additive `docs/architecture.md` update, spec D39–D44).

**Sprint 4 in progress.** The zero-downtime reload bundle is scoped in `.scratch/s4-t0-t4-reload/` as one spec plus eight strictly serial tickets (each `Blocked by:` the one before): S4.D0 (tracking amendment), S4.T0 (application seam), S4.T1 (config diffing + ADR-0015), S4.T2 (registry snapshot swap), S4.T3.0 (reload hook-up APIs), S4.T3 (SIGHUP orchestration), S4.T4.0 (drain-cancel join + ADR-0016), and S4.T4 (drain lifecycle). S4.D0, S4.T0, S4.T1, S4.T2, S4.T3.0, S4.T3, S4.T4.0, and S4.T4 are [DONE]. The bundle's Sprint 4 exit criterion — SIGHUP with 1000 in-flight requests drops zero — is met, demonstrated by `TestChaosReloadDrainExitCriterion1000` (see the S4.T4 entry).

The connection-lifecycle bundle is scoped in `.scratch/s4-t5-t10-connection-lifecycle/` as one spec plus eight tickets: S4.D1 (tracking amendment), S4.T5 (cancellation correctness), S4.T6 (mid-body death), S4.T7 (slow-loris timeouts), S4.T8 (transport tuning), S4.T9 (pprof audit), S4.T10 (soak test), S4.T11 (retry-policy ADR). T7 and T8 are parallel (both blocked by D1 alone); the rest are serial. All eight are [DONE] — S4.T11 closes the bundle.

The close-out bundle is scoped in `.scratch/s4-t12-t15-closeout/` as one spec plus ten tickets: S4.T12 (amendment-first tracking), S4.T13 (gated counting harness), S4.T14 (SIGHUP e2e zero-drop), S4.T15 (`make e2e` target), S4.T16 (env interpolation), S4.T17 (redaction), S4.T18 (example config), S4.T19 (deployment ADR), S4.T20 (retro), S4.T21 (architecture doc). T12→T13→T14 are serial; T15 and T16 both follow T14; T17–T19 follow T16; T20–T21 follow T19. S4.T12–S4.T15 are [DONE]; S4.T16–S4.T21 are pending.

## Proposed tickets (awaiting owner approval)

Not approved, not claimed, not implemented. Surfaced by the S4 reload grilling (`.scratch/s4-t0-t4-reload/spec.md`, *Out of Scope*; decisions D1–D2) and recorded per AGENTS.md Step 2.5. (Prior S3 entries were approved and promoted into the `.scratch/s3-t6-5-t8-t9-chaos/` bundle: S3.T6.5, S3.T8 landed from that bundle and live in the Sprint 3 list above, and S3.T9 was approved and promoted into the same bundle and is claimed above.)

- ~~A reload-outcome counter metric, `lb_config_reloads_total{result}`.~~ **Deferred to Sprint 5 (2026-09-27): benchmarking scope** — a richer reload-observability story may want more than one counter; recorded per the close-out grilling (spec R1, Q7).
- ~~Fix the pre-existing misclassification where a client cancellation is reported to observers as a backend failure (same cancellation-cause mechanism; belongs to Sprint 4's connection-lifecycle deliverable). On removed backends T2's suppression already hides it; on live backends it is unchanged.~~ **Closed by S4.D1 (2026-09-26): absorbed into S4.T5**, its owner in the connection-lifecycle bundle.
- ~~Hot-reload of the algorithm, health timing, circuit cooldown, listen addresses, or the drain window — changing them is rejected, not ignored.~~ **Closed by S4.T12 (2026-09-27): already implemented** — `NonBackendChanges` (`internal/config/diff.go`) names `reload`, `server`, `transport` and every original non-backend field (`listen`, `algorithm`, `health`, `circuit`, `metrics`, `health_endpoint`), so a reload that changes any of them is rejected whole with the fields named (ADR-0015 decision 4; ADR-0016 decision 1; S4.T7; S4.T8).
- Pre-warming added backends (probing before the swap, so a blue/green reload has no empty-selectable window).

## Sprint 4 — Hard Subsystems

Scoped in `.scratch/s4-t0-t4-reload/` as one bundle spec plus eight strictly serial tickets (each `Blocked by:` the one before), with the `.0` split-outs following the S3.T12.0 precedent so each fits one context. The bundle's `spec.md` is the authoritative scope boundary; every story and implementation decision is tagged with exactly one ticket there.

- [DONE] S4.D0 — Tracking amendment: MILESTONES + PROGRESS
  - Spec: `.scratch/s4-t0-t4-reload/issues/01-d0-tracking-amendment.md`

- [DONE] S4.T0 — Application seam (`internal/app`: build + run)
  - Spec: `.scratch/s4-t0-t4-reload/issues/02-t0-application-seam.md`
  - Depends: S4.D0

- [DONE] S4.T1 — Config diffing by backend identity + ADR-0015
  - Spec: `.scratch/s4-t0-t4-reload/issues/03-t1-config-diffing.md`
  - Depends: S4.T0

- [DONE] S4.T2 — Registry snapshot swap, removed-backend suppression, ring rebuild
  - Spec: `.scratch/s4-t0-t4-reload/issues/04-t2-registry-snapshot-swap.md`
  - Depends: S4.T1

- [DONE] S4.T3.0 — Reload hook-up APIs: checker add/remove, outlier forget, series deletion, initial-probe admission
  - Spec: `.scratch/s4-t0-t4-reload/issues/05-t3-0-reload-hook-up-apis.md`
  - Depends: S4.T2

- [DONE] S4.T3 — SIGHUP reload orchestration
  - Spec: `.scratch/s4-t0-t4-reload/issues/06-t3-sighup-reload-orchestration.md`
  - Depends: S4.T3.0

- [DONE] S4.T4.0 — Drain window config, retired context, and drain-cancel join + ADR-0016
  - Spec: `.scratch/s4-t0-t4-reload/issues/07-t4-0-drain-cancel-join.md`
  - Depends: S4.T3

- [DONE] S4.T4 — Drain lifecycle and the zero-drop exit criterion
  - Spec: `.scratch/s4-t0-t4-reload/issues/08-t4-drain-lifecycle-exit-criterion.md`
  - Depends: S4.T4.0

## Sprint 4 — Connection Lifecycle

Scoped in `.scratch/s4-t5-t10-connection-lifecycle/` as one bundle spec plus eight tickets. The bundle's `spec.md` is the authoritative scope boundary; every story and implementation decision is tagged with exactly one ticket there. T7 and T8 are parallel (both blocked by D1 alone); the rest are serial per the spec's ticket map.

- [DONE] S4.D1 — Tracking amendment: PROGRESS + proposed-ticket closure
  - Spec: `.scratch/s4-t5-t10-connection-lifecycle/issues/01-d1-tracking-amendment.md`
  - Acceptance: PROGRESS.md gains the S4.T5–T11 entries with acceptance criteria; the client-cancellation proposed ticket is closed, named to S4.T5; MILESTONES.md confirmed unchanged.

- [DONE] S4.T5 — Context propagation and cancellation correctness
  - Spec: `.scratch/s4-t5-t10-connection-lifecycle/issues/02-t5-client-cancellation-correctness.md`
  - Depends: S4.D1
  - Completed: 2026-09-26T18:20:00Z. `reqState` gains `clientCtx`; `errorHandler` classifies failed round trips drain-first into drain / client-gone / transport-failure; client-gone reaches no observer, records no EWMA latency, releases its slot, records 499/4xx and logs `client_canceled` at INFO; response-header timeout still reaches observers as a failure. Scope amended by owner approval mid-task (two-axis review finding): ADR-0017 adds `Backend.RearmTrial()` so a client-gone request that held a half-open circuit trial re-arms it instead of wedging the circuit permanently away from a live backend.
  - Acceptance: the error handler classifies every failed round trip into exactly one of three buckets — drain cancellation, client-gone, genuine transport failure — using the client's own request context as the discriminator; client-gone requests reach no observer, record no EWMA latency, release their active-connection slot, and are recorded as 499 / `status_class="4xx"`; the log vocabulary gains the `client_canceled` reason; a chaos test proves a client cancellation reaches no observer and records no EWMA latency, and that a response-header timeout still reaches the observers as a failure.

- [DONE] S4.T6 — Backend death mid-response
  - Spec: `.scratch/s4-t5-t10-connection-lifecycle/issues/03-t6-backend-death-mid-response.md`
  - Depends: S4.T5
  - Completed: 2026-09-26T18:34:00Z. `releaseBody` gains a `Read` that counts copied bytes and logs a non-EOF read error at WARN with reason `backend_died_mid_response`, backend, path, and `bytes_copied`; the headers-time success stands and no observer is fed a second event. A drain or client-gone cancellation on the same body path is recognised via the same discriminators and is not misreported as a backend death. Chaos tests cover a mid-body death (WARN line, gauges unchanged, slot released) and a pre-headers death (clean 502, failure observed, no leak); the mid-body ejection gap is documented as a known limitation in the session log.
  - Acceptance: the response-body wrapper observes read errors — any non-EOF error after headers is logged at WARN with the new `backend_died_mid_response` reason carrying the backend name, the path, and the bytes already copied; the success recorded when headers arrives stands; no observer is fed; the mid-body ejection gap is documented as a known limitation.

- [DONE] S4.T7 — Slow-loris client timeouts
  - Spec: `.scratch/s4-t5-t10-connection-lifecycle/issues/04-t7-slow-loris-client-timeouts.md`
  - Depends: S4.D1
  - Claimed 2026-09-26T18:42:21Z by OpenCode (deepseek-v4.1-flash); completed 2026-09-27T09:57:34Z.
  - Completed: `Config` gains a top-level `server:` section with `read_timeout` (pointer duration, default 60s via `DefaultReadTimeout`, explicitly non-positive rejected naming the field). `Build` wires it to the client-facing server's `ReadTimeout`; `WriteTimeout` is deliberately omitted and the rationale documented in `ServerConfig` and `configs/example.yaml`. `NonBackendChanges` names `server`, so a reload that changes it is rejected whole. A behavioral test proves a stalled slow-body client is cut off at the bound; `make test`, `make test-race`, `go vet`, and `make fmt` are green.
  - Acceptance: config gains a `server:` section with `read_timeout` (default 60s when omitted, explicitly non-positive rejected naming the field); a slow-loris client is disconnected at the read timeout; `WriteTimeout` is deliberately omitted with the rationale documented; a reload that changes `server` is rejected naming it.
  - Acceptance: config gains a `server:` section with `read_timeout` (default 60s when omitted, explicitly non-positive rejected naming the field); a slow-loris client is disconnected at the read timeout; `WriteTimeout` is deliberately omitted with the rationale documented; a reload that changes `server` is rejected naming it.

- [DONE] S4.T8 — Transport tuning
  - Spec: `.scratch/s4-t5-t10-connection-lifecycle/issues/05-t8-transport-tuning.md`
  - Depends: S4.D1
  - Claimed 2026-09-27T11:24:07Z by OpenCode (deepseek-v4.1-flash); completed 2026-09-27T11:29:47Z.
  - Completed: `Config` gains a top-level `transport:` section with four pointer fields and exported defaults — `dial_timeout` (5s), `response_header_timeout` (30s), `max_idle_conns_per_host` (100), `idle_conn_timeout` (90s); duration/positive-int validation follows the nil-means-omitted convention and names each field. `proxy.SetTransport` installs an app-built `*http.Transport` (dial timeout, response-header timeout, per-host idle pool, total `MaxIdleConns = per-host × backend count` so the knob is not capped at 100) in place of `http.DefaultTransport`. `NonBackendChanges` names `transport`, so a reload that changes it is rejected whole. Behavior tests prove a non-accepting address fails at the dial bound and a gated backend fails at the response-header bound; `make test`, `make test-race`, `go vet`, and `make fmt` are green.
  - Acceptance: config gains a `transport:` section with `dial_timeout` (default 5s), `response_header_timeout` (default 30s), `max_idle_conns_per_host` (default 100), and `idle_conn_timeout` (default 90s); the proxy runs on a configured `http.Transport` replacing `http.DefaultTransport`, with total `MaxIdleConns` sized from the per-host value and the backend count; a reload that changes `transport` is rejected naming it.

- [DONE] S4.T9 — pprof audit
  - Spec: `.scratch/s4-t5-t10-connection-lifecycle/issues/06-t9-pprof-audit.md`
  - Depends: S4.T5–T8
  - Claimed 2026-09-27T11:42:11Z by OpenCode (deepseek-v4.1-flash); completed 2026-09-27T11:44:51Z.
  - Completed: `buildMetricsHandler` mounts `net/http/pprof`'s index, cmdline, profile, symbol, and trace endpoints on the metrics listener's mux beside `/metrics` — no new listener, no new config (ADR-0005 puts security hardening out of scope). A listener test drives each endpoint through the same handler and asserts its actual body, not just a 200, because the bare Prometheus handler answers every path. The goroutine-leak audit (`TestChaosGoroutineLeakAudit`) warms the system, captures a settled baseline, drives cancellations against a gated backend and backend deaths, then asserts the count settles back within a delta of 10; run five times under `-race`. `make test`, `make test-race`, `go vet`, and `make fmt` are green.
  - Acceptance: `net/http/pprof` is mounted on the metrics listener; a goroutine-leak audit test through the app seam asserts the goroutine count returns to baseline within a small delta after a failure-laden load burst.

- [DONE] S4.T10 — Soak test
  - Spec: `.scratch/s4-t5-t10-connection-lifecycle/issues/07-t10-soak-test.md`
  - Depends: S4.T9
  - Claimed 2026-09-27T12:14:58Z by OpenCode (deepseek-v4.1-flash); completed 2026-09-27T12:38:56Z.
  - Completed: `test/chaos/soak_test.go` — `TestChaosSoakConnectionLifecycle`, flag-gated by `-soak`, budget `-soak-duration` (default 1h), six phases (warmup, steady, cancellation, death, reload, quiet) through the app seam, with committed tolerances +10 goroutines (`leakAuditDelta` reused) and +8 MB post-GC heap. `test/chaos/soak_race_test.go` / `soak_norace_test.go` set `raceDetectorEnabled` from the `race` build tag so the race variant keeps the goroutine assertion and drops the heap one. `make soak` (non-race, both tolerances) and `make soak-race` (goroutine only), both with `SOAK_DURATION` and a 2h test timeout. `assembleWith` splits the shared `assemble` helper so the soak runs on a discarding logger rather than retaining an hour of records. Reloads drive `App.Reload` directly — the operation main's SIGHUP loop calls; the signal registration stays untested per the bundle spec's Out of Scope. `make test`, `make test-race`, `go vet`, `make fmt` green; short iterations and both make targets verified, and the full one-hour `make soak` passed (35→30 goroutines, post-GC heap 930 KB→804 KB, 59.66M requests, 7,175 cancellations, 2,789 failures, 12 reloads) — the Sprint 4 third exit criterion.
  - Acceptance: a flag-gated one-hour soak test through the app seam cycling steady load, client cancellations, backend deaths, and SIGHUP reloads, with a duration flag to shorten for iteration and a new `make soak` target; tolerances committed as numbers — goroutines ≤ warmup baseline + 10, post-GC `HeapAlloc` ≤ baseline + 8 MB; the race variant keeps the goroutine assertion and drops the heap assertion.

- [DONE] S4.T11 — Retry-policy ADR (docs-only)
  - Spec: `.scratch/s4-t5-t10-connection-lifecycle/issues/08-t11-retry-policy-adr.md`
  - Depends: S4.T10
  - Claimed 2026-09-27T14:31:23Z by OpenCode (deepseek-v4.1-flash); completed 2026-09-27T14:32:26Z.
  - Completed: `docs/adr/0018-no-retry-ever.md` records "no retry, ever" as a consequence of the observer and active-connection design, citing the T5/T6 code that forces it — `reqState.activate()`/`release()` (exactly-once slot), the `observe()` fan-out to circuit/outlier/EWMA, and the terminal three-tier `errorHandler`. It states the two harms (a retry double-counts `ActiveConns` and feeds two failures into the outlier window for one client failure), why it cannot be added later without redesign (bodies are unreplayable streams, idempotency is unknowable to a method-agnostic LB), and what replaces it (classified fast failure, retry owned by the client). The `docs/adr/INDEX.md` row is added. Docs-only, no code; the Sprint 4 retry-policy MILESTONES deliverable.
  - Acceptance: the ADR records "no retry, ever" as a consequence of the observer and active-connection design, citing the T5/T6 code that forces it.

## Sprint 4 — Close-out

Scoped in `.scratch/s4-t12-t15-closeout/` as one bundle spec plus ten tickets. The bundle's `spec.md` is the authoritative scope boundary; every story and implementation decision is tagged with exactly one ticket there. T12→T13→T14 are serial; T15 and T16 both follow T14; T17–T19 follow T16; T20–T21 follow T19.

- [DONE] S4.T12 — Amendment-first: tracking for the close-out (opencode, started 2026-09-27T21:50:00+05:30, completed 2026-09-27T21:55:00+05:30)
  - Spec: `.scratch/s4-t12-t15-closeout/issues/01-t12-amendment-first-tracking.md`
  - Depends: S4.T11
  - Acceptance: PROGRESS.md gains the "Sprint 4 — Close-out" section with the ten ticket rows (S4.T12–S4.T21) and their acceptance criteria; MILESTONES.md Sprint 4 deliverables gain the interpolation line; the stale hot-reload proposed ticket is closed with the reason recorded; the reload-outcome counter metric is deferred to Sprint 5 with the reason recorded; the amendment lands as one commit touching only PROGRESS.md and MILESTONES.md.
  - Test approach: none — docs-only (AGENTS.md TDD exception).

- [DONE] S4.T13 — Harness: gated counting backends (opencode, started 2026-09-27T16:44:01Z, completed 2026-09-27T16:46:45Z)
  - Spec: `.scratch/s4-t12-t15-closeout/issues/02-t13-harness-gated-counting-backends.md`
  - Depends: S4.T12
  - Acceptance: the harness backend type gains an atomic request counter incremented at entry, before gating, readable by tests; existing chaos tests are untouched and stay green (`make test`, `make test-race`); a test proves the counter increments once per request and is readable without touching proxy or registry internals.

- [DONE] S4.T14 — SIGHUP end-to-end zero-drop test (opencode, started 2026-09-27T23:00:00Z, completed 2026-09-27T23:10:00Z)
  - Spec: `.scratch/s4-t12-t15-closeout/issues/03-t14-sighup-e2e-zero-drop.md`
  - Depends: S4.T13
  - Completed: `test/chaos/sighup_e2e_test.go` — `TestChaosSighupReloadZeroDrop1000`, flag-gated by `-e2e` (precedent: the soak's `-soak`). Builds the binary under test with `go build -C` from the module root into `t.TempDir()` (skips cleanly via `exec.LookPath("go")` when no toolchain), spawns it with `-config` on a temp config, captures stdout/stderr and replays through `t.Logf` on failure, polls the client listener until it answers, raises `RLIMIT_NOFILE` soft-to-hard best-effort, grabs a free `127.0.0.1:0` port for `listen` (metrics/health-endpoint on `:0`). Fires 1000 concurrent GETs through one `http.Client`, confirms via the per-backend counters (baseline + 1000) that all are held, rewrites the config atomically (temp + `os.Rename`: backend-c added, backend-b removed, `drain_window` unchanged), sends a real `syscall.Kill(pid, syscall.SIGHUP)`. While the 1000 are held, fires 20 probe requests asynchronously and asserts the added backend's counter grows past its admission-probe baseline and the removed backend's counter stays frozen. Releases the gates, asserts all 1000 return 200, the removed backend's counter equals its pre-signal value, and the process is alive; teardown `SIGTERM` + wait with a 10s timeout, asserts exit 0. The in-flight wait uses a 30s deadline constant, never sleeps. `make e2e` runs the single test with `-timeout 10m`; the target's help text documents `ulimit -n 10240` for restrained environments. The make target is included here per spec story 7 (the e2e behind its own target so `make test` stays hermetic); S4.T15 closes as already-implemented. `make test`, `make test-race`, `go vet`, and `gofmt` are green; `make e2e` passes (~2s).
  - Acceptance: build the binary under test with `go build -C` pointed at the module root, output in `t.TempDir()`; skip cleanly via `exec.LookPath("go")` when no toolchain is present; spawn the binary with `-config` pointing at a temp config; capture stdout/stderr and replay through `t.Logf` on failure; poll the client listener until it answers (any HTTP response means up); grab a free `127.0.0.1:0` port for the config's `listen` (metrics and health-endpoint listens use `:0`); raise `RLIMIT_NOFILE` soft-to-hard best-effort at start; fire 1000 concurrent GETs through one `http.Client`; confirm via the per-backend counters (sum == 1000) that all are held before the signal; rewrite the config atomically (temp file + `os.Rename`): one backend added, one removed, `drain_window` unchanged (not reloadable, ADR-0016); send `syscall.Kill(pid, syscall.SIGHUP)`; while the 1000 are held, fire probe requests and assert they land on the added backend (counter grows past its admission-probe baseline) and never on the removed one (counter frozen); release the gates; assert all 1000 return 200, the removed backend's counter equals its pre-signal value, and the process is still alive; teardown: `SIGTERM`, wait with a timeout, assert exit status 0; the in-flight wait uses a 30s deadline constant sized for process spawn and socket setup, never sleeps; the test's doc comment states the in-process/e2e division of labor (registry views stay in-process, the e2e asserts externally visible outcomes only); the PROGRESS entry marks the exit criterion as evidenced at the OS boundary, citing both tests; `make test`, `make test-race`, vet, fmt clean.

- [DONE] S4.T15 — `make e2e` target (opencode, started 2026-09-28T10:06:56+05:30, completed 2026-09-28T10:08:30+05:30)
  - Spec: `.scratch/s4-t12-t15-closeout/issues/04-t15-make-e2e-target.md`
  - Depends: S4.T14
  - Completed: the `e2e` target was landed with S4.T14 (commit `a8a6e0c`) under this ticket's spec story 7 — the e2e must sit behind its own target so the default suite stays hermetic. Close-out verified it against issue 04's acceptance rather than re-adding it: `make e2e` runs the single `TestChaosSighupReloadZeroDrop1000` with `-e2e -timeout 10m -v`; the help text documents `ulimit -n 10240` for restrained environments; `make test` and `make test-race` are untouched; `make e2e` passes locally (~2.2s). No code change, so the TDD exception applies (AGENTS.md: infra-only tasks follow Steps 0–2 and 6). `make test`, `go vet ./...`, and `gofmt -l .` are clean.
  - Acceptance: `make e2e` runs the single e2e test with a generous `-timeout`; the target's help text documents a `ulimit -n` invocation for restrained environments; `make test` and `make test-race` are unchanged; `make e2e` passes locally.

- [DONE] S4.T16 — Env interpolation in the loader (opencode, started 2026-09-28T04:41:21Z, completed 2026-09-28T04:46:46Z)
  - Spec: `.scratch/s4-t12-t15-closeout/issues/05-t16-env-interpolation-loader.md`
  - Depends: S4.T14
  - Completed: `Load` gains one expansion step after YAML decode: `expandBackendURLs` resolves `${VAR}` (name `[A-Za-z_][A-Za-z0-9_]*`, no default syntax) in each backend URL from the process environment, in place, so the resolved URL is what `Validate`'s existing `validateBackendURL` checks; non-backend fields pass through untouched. Unset or empty variables and malformed references (unterminated `${`, empty `${}`, invalid name characters) fail the load naming the backend (and the variable for unset/empty); errors never contain the expanded value. `Load`'s doc comment is updated from "pure deserialization". Tests: table-driven `Load` tests with `t.Setenv` (no `t.Parallel`) covering host/port/credential positions, multi-variable and literal-adjacent URLs, all failure classes, non-backend passthrough, an error names-the-right-backend case, and a mixed valid-then-malformed secret-leak guard; a resolved-URL-validated test; and a reload composition test proving a changed expansion diffs to one removed plus one added backend (ADR-0015 identity). No new exported symbols, `Config` shape unchanged, `TestDockerConfig` untouched. Two-axis code review flagged a double `config:` prefix on load errors (fixed) and asked that the passthrough case sit in the table (folded in). Redaction of `validateBackendURL` messages remains S4.T17. `make test`, `make test-race`, `go vet`, and `make fmt` green.
  - Acceptance: `Load` gains the expansion step between decode and return; its doc comment states the expansion rule (it was "pure deserialization"); syntax `${VAR}` where `VAR` matches `[A-Za-z_][A-Za-z0-9_]*`; no default syntax; non-backend fields pass through untouched; unset or empty variable → load error naming the backend and the variable; malformed references (unterminated `${`, empty `${}`, invalid name characters) → load error naming the backend; errors never contain the expanded value; the resolved URL is validated by the existing `validateBackendURL`; reload semantics: interpolation re-runs on every `Load`, and since backend identity is `(name, URL)` (ADR-0015) a changed expansion is one removed plus one added backend; table-driven `Load` tests with `t.Setenv` (no `t.Parallel()`): expansion in host/port/credential positions, multiple variables in one URL, variables adjacent to literal text, unset/empty/malformed failures, passthrough of a `${...}` in a non-backend field; composition test: two `Load`s differing only in environment, diffed — the affected identity is one removed plus one added backend; `configs/docker.yaml` still loads and validates interpolation-free (`TestDockerConfig` untouched); no new exported symbols; `Config` shape unchanged; `make test`, `make test-race`, vet, fmt clean.

- [DONE] S4.T17 — Redaction: validation errors never contain the expanded URL (opencode, started 2026-09-28T05:03:58Z, completed 2026-09-28T05:08:20Z)
  - Spec: `.scratch/s4-t12-t15-closeout/issues/06-t17-redaction-validation-errors.md`
  - Depends: S4.T16
  - Completed: `validateBackendURL`'s five error branches now name only the backend and the `url` field — the scheme/host/query/fragment branches drop their `%q` URL argument, and the parse branch no longer wraps `url.Parse`'s URL-bearing error (the cause can embed a fragment of the resolved value, so no safe wrapping target exists; the branch reports only "has an invalid url"). `TestValidateBackendURLErrorsRedactRawURL` locks all five branches (backend named, field token present, raw URL absent); `TestLoadEnvInterpolationValidationErrorIsRedacted` puts a secret in the port position and proves the Load→Validate pipeline's error contains neither the expanded value nor the resolved URL. The registry parse error is confirmed unreachable for config-sourced URLs — `app.Build` documents `cfg must already have passed Validate`, main runs Load→Validate first, and `validateBackendURL` already runs `url.Parse` — so `internal/backend/registry.go` is unchanged; the structural argument is recorded in the session log. Two-axis review folded in: tightened the parse-branch assertion token and routed the interpolated test through `loadAndValidate`. `make test`, `make test-race`, `go vet`, and `gofmt` green.
  - Acceptance: `validateBackendURL` error messages name only the backend and the field; the raw URL (resolved or template) never appears; a test proves an invalid interpolated URL produces a load error that does not contain the expanded value; the registry parse error is confirmed unreachable post-validation (`validateBackendURL` already runs `url.Parse`) and the structural argument is recorded in the session log; `make test`, `make test-race`, vet, fmt clean.

- [IN_PROGRESS] S4.T18 — Example config documents interpolation (opencode, started 2026-09-28T05:32:04Z)
  - Spec: `.scratch/s4-t12-t15-closeout/issues/07-t18-example-config-documents-interpolation.md`
  - Depends: S4.T16
  - Acceptance: `configs/example.yaml` documents the `${VAR}` syntax, the unset/empty/malformed failure behavior, and the backend-URL-only scope; the documented examples match the implementation's actual behavior; docs-only.

- [PENDING] S4.T19 — ADR: deployment target decision
  - Spec: `.scratch/s4-t12-t15-closeout/issues/08-t19-deployment-target-adr.md`
  - Depends: S4.T16
  - Acceptance: context cites ADR-0005 and its 2026-09-22 amendment (the demo stack does not settle the target), ADR-0014 (health-endpoint contract and orchestrator-probe semantics), ADR-0015 (in-process reload; why no socket handoff exists), the distroless image and `probe` subcommand (S3.T10), and the compose demo stack (S3.T11); options section: bare binary, Docker, Kubernetes — each argued against the system's real properties (three listeners, SIGHUP in-process reload, self-probe HEALTHCHECK, no external state, no clustering); decision and consequences made by the owner in the ADR session; SO_REUSEPORT process handoff recorded as a Post-Sprint-5 extension, citing the MILESTONES list; index row added to `docs/adr/INDEX.md`; the PROGRESS entry records the ADR as closing the Sprint 4 deployment deliverable deferred from Sprint 1; docs-only.

- [PENDING] S4.T20 — Sprint 4 retro
  - Spec: `.scratch/s4-t12-t15-closeout/issues/09-t20-sprint-4-retro.md`
  - Depends: S4.T19
  - Acceptance: `docs/sprint-4-retro.md` mirroring `docs/sprint-3-retro.md`'s shape — a deliverables-shipped table with the commit that landed each ticket, a deviations section, exit-criteria evidence, and a what-we-differently section; deviations record at minimum the R0 numbering reconciliation, the in-process-first exit criterion now evidenced at the OS boundary, the closed and deferred proposed tickets, and any `validateBackendURL` wording change from S4.T17; exit-criteria evidence: 1000-in-flight zero-drop (both `TestChaosReloadDrainExitCriterion1000` and the e2e SIGHUP test from S4.T14 cited by name), backend death mid-response → clean 502 (S4.T6), one-hour soak with the committed numbers (S4.T10: 59.66M requests, 7,175 cancellations, 2,789 failures, 12 reloads, goroutines 35→30, post-GC heap 930 KB→804 KB — cite the PROGRESS entry as the source of truth); docs-only.

- [PENDING] S4.T21 — Architecture doc update
  - Spec: `.scratch/s4-t12-t15-closeout/issues/10-t21-architecture-doc-update.md`
  - Depends: S4.T19
  - Acceptance: Sprint 4 sections added to `docs/architecture.md` — the app seam, reload and drain, connection lifecycle, transport tuning, soak results, env interpolation, and the e2e SIGHUP test; decision-index rows added for ADR-0015 through ADR-0019; the header's "Sprints 1–3 are complete" reference is updated to reflect the Sprint 4 close-out; the soak results are recorded with their numbers, so the production-resilience claims have evidence; docs-only.

## Sprint 5

See `MILESTONES.md`. Tasks added as scoped.

