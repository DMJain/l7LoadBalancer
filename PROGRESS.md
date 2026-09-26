# PROGRESS

Live state of the project. Every agent updates this file per the protocol in `AGENTS.md`.

## Current status

**Sprint 1 complete.** S1.T0 through S1.T10 (including S1.T0.5) are all [DONE].

**Sprint 2 complete.** All four algorithms are implemented and config-selectable: S2.T1.1 (ring), S2.T1.2 (`naiveConsistentHash` comparator), S2.T2 (`ConsistentHashBoundedLoads`), S2.T3 (`PowerOfTwoChoicesEWMA`), closed out by S2.T8 (retro / architecture close-out: deviations audit, ADR sweep, architecture-doc updates).

**Sprint 3 complete.** All of Sprint 3 (S3.T0.1–T13, including T6.5 and the four demo-stack tickets T10–T13) is [DONE]. Scoped in four passes: `.scratch/s3-t1-t3-health-passive-circuit/` (one spec + six tickets), `.scratch/s3-t4-t7-observability/` (one spec + ten tickets), `.scratch/s3-t6-5-t8-t9-chaos/` (one spec + three tickets: the reinstatement-gate fix plus the T8/T9 chaos tests), and `.scratch/s3-t10-t13-demo-stack/` (one spec + seven tickets: this MILESTONES amendment, the T12 prefactors + handlers, the T10 probe subcommand + Dockerfile, the T11 root compose, and the T13 retro). Done: S3.T0.1 (`MarkHealthy`/`MarkUnhealthy`), S3.T0.2 (round-trip observer fan-out), S3.T0.3 (probe/cooldown config schema), S3.T1 (active health checks), S3.T2 (passive outlier detection), S3.T3 (circuit breaker + `Registry.Selectable()` rename), S3.T4 (metrics package + `metrics.listen` endpoint), S3.T5.1 (logger test coverage), S3.T5.2 (`event`/`reason` transition vocabulary), S3.T5.3 (health transition log lines), S3.T5.4 (circuit transition log lines), S3.T6.1 (whole-request counter + histogram wired into the proxy), S3.T6.2 (active-connections gauge + its startup seeding), S3.T6.3 (backend-healthy gauge + its startup seeding), S3.T6.4 (circuit-state gauge + its startup seeding), S3.T7 (Grafana dashboard + Prometheus/Grafana observability demo stack), S3.T6.5 (active-checker reinstatement gate `==` → `>=`), S3.T8 (backend eviction/recovery chaos test + `docker stop` smoke), S3.T9 (circuit-breaker trip/half-open chaos test + 5xx-injection smoke), S3.T10–T13.D0 (MILESTONES.md amendment), S3.T12.0 (health-endpoint prefactor APIs: `ProbeRoundComplete`/`RecordProbe`/`health_endpoint.listen`), S3.T12 (health endpoint handlers + third listener + ADR-0014), S3.T10.0 (probe subcommand + version/commit injection), S3.T10 (multi-stage distroless Dockerfile + `.dockerignore` allowlist + baked `configs/docker.yaml` + OCI labels + ADR-0005 amendment), S3.T11 (repo-root `docker-compose.yml` + `prometheus-stack.yml` + ADR-0013 decision 18), S3.T13 (Sprint 3 retro + additive `docs/architecture.md` update, spec D39–D44).

**Sprint 4 in progress.** The zero-downtime reload bundle is scoped in `.scratch/s4-t0-t4-reload/` as one spec plus eight strictly serial tickets (each `Blocked by:` the one before): S4.D0 (tracking amendment), S4.T0 (application seam), S4.T1 (config diffing + ADR-0015), S4.T2 (registry snapshot swap), S4.T3.0 (reload hook-up APIs), S4.T3 (SIGHUP orchestration), S4.T4.0 (drain-cancel join + ADR-0016), and S4.T4 (drain lifecycle). S4.D0, S4.T0, S4.T1, S4.T2, S4.T3.0, S4.T3, S4.T4.0, and S4.T4 are [DONE]. The bundle's Sprint 4 exit criterion — SIGHUP with 1000 in-flight requests drops zero — is met, demonstrated by `TestChaosReloadDrainExitCriterion1000` (see the S4.T4 entry).

## Proposed tickets (awaiting owner approval)

Not approved, not claimed, not implemented. Surfaced by the S4 reload grilling (`.scratch/s4-t0-t4-reload/spec.md`, *Out of Scope*; decisions D1–D2) and recorded per AGENTS.md Step 2.5. (Prior S3 entries were approved and promoted into the `.scratch/s3-t6-5-t8-t9-chaos/` bundle: S3.T6.5, S3.T8 landed from that bundle and live in the Sprint 3 list above, and S3.T9 was approved and promoted into the same bundle and is claimed above.)

- A reload-outcome counter metric, `lb_config_reloads_total{result}`.
- Fix the pre-existing misclassification where a client cancellation is reported to observers as a backend failure (same cancellation-cause mechanism; belongs to Sprint 4's connection-lifecycle deliverable). On removed backends T2's suppression already hides it; on live backends it is unchanged.
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

## Sprint 5

See `MILESTONES.md`. Tasks added as scoped.

