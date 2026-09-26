# PROGRESS

Live state of the project. Every agent updates this file per the protocol in `AGENTS.md`.

## Current status

**Sprint 1 complete.** S1.T0 through S1.T10 (including S1.T0.5) are all [DONE].

**Sprint 2 complete.** All four algorithms are implemented and config-selectable: S2.T1.1 (ring), S2.T1.2 (`naiveConsistentHash` comparator), S2.T2 (`ConsistentHashBoundedLoads`), S2.T3 (`PowerOfTwoChoicesEWMA`), closed out by S2.T8 (retro / architecture close-out: deviations audit, ADR sweep, architecture-doc updates).

**Sprint 3 complete.** All of Sprint 3 (S3.T0.1–T13, including T6.5 and the four demo-stack tickets T10–T13) is [DONE]. Scoped in four passes: `.scratch/s3-t1-t3-health-passive-circuit/` (one spec + six tickets), `.scratch/s3-t4-t7-observability/` (one spec + ten tickets), `.scratch/s3-t6-5-t8-t9-chaos/` (one spec + three tickets: the reinstatement-gate fix plus the T8/T9 chaos tests), and `.scratch/s3-t10-t13-demo-stack/` (one spec + seven tickets: this MILESTONES amendment, the T12 prefactors + handlers, the T10 probe subcommand + Dockerfile, the T11 root compose, and the T13 retro). Done: S3.T0.1 (`MarkHealthy`/`MarkUnhealthy`), S3.T0.2 (round-trip observer fan-out), S3.T0.3 (probe/cooldown config schema), S3.T1 (active health checks), S3.T2 (passive outlier detection), S3.T3 (circuit breaker + `Registry.Selectable()` rename), S3.T4 (metrics package + `metrics.listen` endpoint), S3.T5.1 (logger test coverage), S3.T5.2 (`event`/`reason` transition vocabulary), S3.T5.3 (health transition log lines), S3.T5.4 (circuit transition log lines), S3.T6.1 (whole-request counter + histogram wired into the proxy), S3.T6.2 (active-connections gauge + its startup seeding), S3.T6.3 (backend-healthy gauge + its startup seeding), S3.T6.4 (circuit-state gauge + its startup seeding), S3.T7 (Grafana dashboard + Prometheus/Grafana observability demo stack), S3.T6.5 (active-checker reinstatement gate `==` → `>=`), S3.T8 (backend eviction/recovery chaos test + `docker stop` smoke), S3.T9 (circuit-breaker trip/half-open chaos test + 5xx-injection smoke), S3.T10–T13.D0 (MILESTONES.md amendment), S3.T12.0 (health-endpoint prefactor APIs: `ProbeRoundComplete`/`RecordProbe`/`health_endpoint.listen`), S3.T12 (health endpoint handlers + third listener + ADR-0014), S3.T10.0 (probe subcommand + version/commit injection), S3.T10 (multi-stage distroless Dockerfile + `.dockerignore` allowlist + baked `configs/docker.yaml` + OCI labels + ADR-0005 amendment), S3.T11 (repo-root `docker-compose.yml` + `prometheus-stack.yml` + ADR-0013 decision 18), S3.T13 (Sprint 3 retro + additive `docs/architecture.md` update, spec D39–D44).

**Sprint 4 in progress.** The zero-downtime reload bundle is scoped in `.scratch/s4-t0-t4-reload/` as one spec plus eight strictly serial tickets (each `Blocked by:` the one before): S4.D0 (tracking amendment), S4.T0 (application seam), S4.T1 (config diffing + ADR-0015), S4.T2 (registry snapshot swap), S4.T3.0 (reload hook-up APIs), S4.T3 (SIGHUP orchestration), S4.T4.0 (drain-cancel join + ADR-0016), and S4.T4 (drain lifecycle). S4.D0, S4.T0, S4.T1, S4.T2, S4.T3.0, S4.T3, S4.T4.0, and S4.T4 are [DONE]. The bundle's Sprint 4 exit criterion — SIGHUP with 1000 in-flight requests drops zero — is met, demonstrated by `TestChaosReloadDrainExitCriterion1000` (see the S4.T4 entry).

The connection-lifecycle bundle is scoped in `.scratch/s4-t5-t10-connection-lifecycle/` as one spec plus eight tickets: S4.D1 (tracking amendment), S4.T5 (cancellation correctness), S4.T6 (mid-body death), S4.T7 (slow-loris timeouts), S4.T8 (transport tuning), S4.T9 (pprof audit), S4.T10 (soak test), S4.T11 (retry-policy ADR). T7 and T8 are parallel (both blocked by D1 alone); the rest are serial. S4.D1 is [DONE] (2026-09-26T18:03:24Z); S4.T5–T11 are pending.

## Proposed tickets (awaiting owner approval)

Not approved, not claimed, not implemented. Surfaced by the S4 reload grilling (`.scratch/s4-t0-t4-reload/spec.md`, *Out of Scope*; decisions D1–D2) and recorded per AGENTS.md Step 2.5. (Prior S3 entries were approved and promoted into the `.scratch/s3-t6-5-t8-t9-chaos/` bundle: S3.T6.5, S3.T8 landed from that bundle and live in the Sprint 3 list above, and S3.T9 was approved and promoted into the same bundle and is claimed above.)

- A reload-outcome counter metric, `lb_config_reloads_total{result}`.
- ~~Fix the pre-existing misclassification where a client cancellation is reported to observers as a backend failure (same cancellation-cause mechanism; belongs to Sprint 4's connection-lifecycle deliverable). On removed backends T2's suppression already hides it; on live backends it is unchanged.~~ **Closed by S4.D1 (2026-09-26): absorbed into S4.T5**, its owner in the connection-lifecycle bundle.
- Hot-reload of the algorithm, health timing, circuit cooldown, listen addresses, or the drain window — changing them is rejected, not ignored.
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
  - Completed: 2026-09-26T18:34:00Z. `releaseBody` gains a `Read` that counts copied bytes and logs a non-EOF read error at WARN with reason `backend_died_mid_response`, backend, path, and `bytes_copied`; the headers-time success stands and no observer is fed a second event. Chaos tests cover a mid-body death (WARN line, gauges unchanged, slot released) and a pre-headers death (clean 502, failure observed, no leak); the mid-body ejection gap is documented as a known limitation in the session log.
  - Acceptance: the response-body wrapper observes read errors — any non-EOF error after headers is logged at WARN with the new `backend_died_mid_response` reason carrying the backend name, the path, and the bytes already copied; the success recorded when headers arrives stands; no observer is fed; the mid-body ejection gap is documented as a known limitation.

- [PENDING] S4.T7 — Slow-loris client timeouts
  - Spec: `.scratch/s4-t5-t10-connection-lifecycle/issues/04-t7-slow-loris-client-timeouts.md`
  - Depends: S4.D1
  - Acceptance: config gains a `server:` section with `read_timeout` (default 60s when omitted, explicitly non-positive rejected naming the field); a slow-loris client is disconnected at the read timeout; `WriteTimeout` is deliberately omitted with the rationale documented; a reload that changes `server` is rejected naming it.

- [PENDING] S4.T8 — Transport tuning
  - Spec: `.scratch/s4-t5-t10-connection-lifecycle/issues/05-t8-transport-tuning.md`
  - Depends: S4.D1
  - Acceptance: config gains a `transport:` section with `dial_timeout` (default 5s), `response_header_timeout` (default 30s), `max_idle_conns_per_host` (default 100), and `idle_conn_timeout` (default 90s); the proxy runs on a configured `http.Transport` replacing `http.DefaultTransport`, with total `MaxIdleConns` sized from the per-host value and the backend count; a reload that changes `transport` is rejected naming it.

- [PENDING] S4.T9 — pprof audit
  - Spec: `.scratch/s4-t5-t10-connection-lifecycle/issues/06-t9-pprof-audit.md`
  - Depends: S4.T5–T8
  - Acceptance: `net/http/pprof` is mounted on the metrics listener; a goroutine-leak audit test through the app seam asserts the goroutine count returns to baseline within a small delta after a failure-laden load burst.

- [PENDING] S4.T10 — Soak test
  - Spec: `.scratch/s4-t5-t10-connection-lifecycle/issues/07-t10-soak-test.md`
  - Depends: S4.T9
  - Acceptance: a flag-gated one-hour soak test through the app seam cycling steady load, client cancellations, backend deaths, and SIGHUP reloads, with a duration flag to shorten for iteration and a new `make soak` target; tolerances committed as numbers — goroutines ≤ warmup baseline + 10, post-GC `HeapAlloc` ≤ baseline + 8 MB; the race variant keeps the goroutine assertion and drops the heap assertion.

- [PENDING] S4.T11 — Retry-policy ADR (docs-only)
  - Spec: `.scratch/s4-t5-t10-connection-lifecycle/issues/08-t11-retry-policy-adr.md`
  - Depends: S4.T10
  - Acceptance: the ADR records "no retry, ever" as a consequence of the observer and active-connection design, citing the T5/T6 code that forces it.

## Sprint 5

See `MILESTONES.md`. Tasks added as scoped.

