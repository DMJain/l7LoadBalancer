# Sprint 4 retrospective — Hard Subsystems

Written by S4.T20 (issue 09 of `.scratch/s4-t12-t15-closeout/`), 2026-09-28,
mirroring the Sprint 3 close-out shape. Sprint 4 ran across three scoping
passes: `.scratch/s4-t0-t4-reload/` (the `internal/app` seam and the
zero-downtime reload bundle), `.scratch/s4-t5-t10-connection-lifecycle/`
(cancellation correctness, mid-body death, slow-loris timeouts, transport
tuning, the pprof audit, the soak, and the retry ADR), and
`.scratch/s4-t12-t15-closeout/` (the OS-boundary SIGHUP e2e, env interpolation,
the deployment-target ADR, and this retro). The design decisions audited here
are recorded in the three bundle specs' Implementation Decisions sections; the
per-session detail is in `docs/sessions/`.

Companion artifacts: `docs/adr/0015`–`0019`, the amendments ADR-0015 makes to
ADR-0002 decision 5 and ADR-0017 makes to ADR-0007 and ADR-0012, and the
`docs/design/sprint-1-contracts.md` concurrency/log-vocabulary updates that
landed with the reload and connection-lifecycle tickets.

## 1. Deliverables shipped

Every Sprint 4 task with a one-line summary and the commit that landed it,
grouped by bundle. Sub-tickets are indented under the ticket they prefactor;
claim commits (`chore(progress): start …`) are omitted in favour of the
implementation, test, ADR, or review commit that carries the work.

### Reload bundle (`.scratch/s4-t0-t4-reload/`)

| Task | Summary | Commit(s) |
|---|---|---|
| S4.D0 | Amendment-first: Sprint 4 PROGRESS section plus the `internal/app` seam deliverable in MILESTONES, before any code. | `ba1d232` |
| S4.T0 | `internal/app` `Build`/`Run` seam; `main` reduced to flags/load/validate/build/run; chaos `assemble` delegates to `Build`. | `0766bf7`, review `e207513` |
| S4.T1 | `config.DiffBackends` by `(name, URL)` identity + `NonBackendChanges`; ADR-0015 (reload architecture). | ADR `1b957e8`, `cd07d69`, review `deb4157` |
| S4.T2 | Registry versioned snapshot swap, removed-backend observer suppression, version-keyed ring rebuild. | `80ce9e7`, review `050637d` |
| S4.T3.0 | Reload hook-up APIs: checker `Add`/`Remove`, outlier `Forget`, healthy/circuit series deletion, one-probe added-backend admission. | `71e6136` |
| S4.T3 | SIGHUP reload orchestration; non-backend reject; added backends published unhealthy and admitted by one probe. | `7429405`, fix `ba18398` |
| S4.T4.0 | `reload.drain_window`, per-backend retired context, proxy drain-cancel join; ADR-0016 (drain lifecycle). | ADR `245eee7`, `7aa7b29`, review `79ac69f` |
| S4.T4 | Per-removed-backend drain lifecycle plus the in-process zero-drop exit-criterion test. | `630cbae`, review `3c1e58f` |

### Connection-lifecycle bundle (`.scratch/s4-t5-t10-connection-lifecycle/`)

| Task | Summary | Commit(s) |
|---|---|---|
| S4.D1 | Amendment-first: S4.T5–T11 PROGRESS entries; the client-cancellation proposed ticket closed into S4.T5. | `0117806` |
| S4.T5 | Three-tier failed-round-trip classification (drain / client-gone / transport), 499 + `client_canceled`; ADR-0017 (`RearmTrial`). | `e0c07c6`, fix `d87ef1b` |
| S4.T6 | Backend death mid-response: WARN `backend_died_mid_response` with bytes copied; the recorded success stands. | `7fbd5d3`, fix `b2486fa` |
| S4.T7 | `server.read_timeout` slow-loris bound; `NonBackendChanges` names `server`. | tests `9030b56`, `eed68dd` |
| S4.T8 | `transport:` section plus an app-built `http.Transport` replacing `http.DefaultTransport`. | tests `1e2a356`, `a12a85c`, review `cd5d47d` |
| S4.T9 | pprof mounted on the metrics listener plus the goroutine-leak audit. | tests `57fee7d`, `249a2d4`, review `353a462` |
| S4.T10 | Flag-gated one-hour connection-lifecycle soak, `make soak`/`make soak-race`; the full-hour pass recorded. | `7ea2673`, review `07114b5`, pass `98d2bf4` |
| S4.T11 | ADR-0018 — no retry, ever, as a consequence of the observer and active-connection design. | `cf9a5ad`, review `1283490` |

### Close-out bundle (`.scratch/s4-t12-t15-closeout/`)

| Task | Summary | Commit(s) |
|---|---|---|
| S4.T12 | Amendment-first close-out tracking; the stale hot-reload ticket closed; the reload-outcome metric deferred to Sprint 5. | `a5c2345` |
| S4.T13 | Gated counting backends: additive per-backend atomic request counter, taken before gating. | tests `c418375`, `2321831`, review `2ddb5ab` |
| S4.T14 | OS-boundary SIGHUP e2e zero-drop test, `TestChaosSighupReloadZeroDrop1000`, plus `make e2e`. | `a8a6e0c` |
| S4.T15 | `make e2e` target — verified already-implemented; landed with T14 (no code change). | `78c22cc` |
| S4.T16 | `${VAR}` interpolation in backend URLs at `Load`, between decode and validation. | `19c5a2a` |
| S4.T17 | Redaction: `validateBackendURL` errors drop the raw URL across all five branches. | `db0088f` |
| S4.T18 | `configs/example.yaml` documents interpolation. | `a5a0bf9` |
| S4.T19 | ADR-0019 — the digest-pinned distroless Docker image is the deployment target. | `3fa0fb0`, review `e1f2832` |

S4.T20 is this document. S4.T21 (architecture-doc update) is the final
close-out ticket and remains pending.

## 2. Deviations from MILESTONES/spec

### 2.1 R0 — the pre-grilling numbering was reconciled, not rebuilt

The close-out grilling opened from the owner's pre-grilling list using a
T11–T15 numbering that collided with the repo's canonical tickets. The spec
(`.scratch/s4-t12-t15-closeout/spec.md`, "Reconciliation (R0)") settled the
collision, and the canonical close-out amendment (S4.T12, commit `a5c2345`)
then expanded the spec's four bundle tickets into the ten tickets `PROGRESS.md`
actually tracks. Nothing was renumbered, rebuilt, or resurrected; the auditable
mapping is:

| Owner's pre-grilling item | Spec bundle ticket | Canonical repo ticket(s) | Outcome |
|---|---|---|---|
| S4.T11 zero-drop reload test | S4.T12 (e2e) | S4.T13 (harness) + S4.T14 (e2e) | In-process test already existed in S4.T4; the OS-boundary e2e was built |
| S4.T12 env-var interpolation | S4.T13 | S4.T16 (interpolation) + S4.T17 (redaction) + S4.T18 (example config) | Built, split into three tickets |
| S4.T13 reload architecture ADR | — | — | Already ADR-0015 (S4.T1); closed as recorded, not rebuilt |
| S4.T14 retry policy ADR | — | — | Already ADR-0018 (S4.T11); closed as recorded, not rebuilt |
| S4.T15 retro + architecture doc | S4.T15 | S4.T20 (retro) + S4.T21 (architecture doc) | Split into two; T20 is this document, T21 pending |
| (amendment-first fold) | S4.T12's first commit | S4.T12 | Folded into T12; no separate amendment ticket |
| (make `e2e` target) | S4.T15, story 7 | S4.T15 | Landed with T14; T15 verified it |

### 2.2 The first exit criterion was evidenced in-process first, at the OS boundary second

MILESTONES states the criterion as "SIGHUP with 1000 in-flight requests drops
zero". It was first met by `TestChaosReloadDrainExitCriterion1000` in S4.T4
(`test/chaos/reload_test.go`, commit `630cbae`), which drives `App.Reload`
directly through the application seam. OS signal delivery and `main`'s reload
loop were deliberately left untested by every prior bundle, so the boundary the
criterion names was not yet proven. S4.T14 (`a8a6e0c`) closed that gap with
`TestChaosSighupReloadZeroDrop1000`: it builds the real binary, spawns it,
holds 1000 requests across a real `syscall.Kill(pid, SIGHUP)`, rewrites the
config file atomically, and asserts externally visible outcomes only. The
in-process test keeps the registry-view assertions (instance identity,
EWMA/circuit survival); the e2e asserts statuses, per-backend counts, live
traffic shifting to the added backend, and a clean exit. That division of labor
is stated in the e2e's doc comment.

### 2.3 Proposed tickets closed and deferred

Recorded in `PROGRESS.md` under "Proposed tickets" and landed in S4.T12's
amendment commit (`a5c2345`):

- **Closed — client cancellation misclassified as a backend failure.** Absorbed
  into S4.T5 (commit `0117806`), which added the three-tier classification.
- **Closed — hot-reload rejected, not ignored.** Already implemented by S4.T1's
  `NonBackendChanges` (now naming `reload`, `server`, `transport`, and every
  original non-backend field); S4.T12 closed the ticket with the reason on
  record.
- **Deferred — the reload-outcome counter metric (`lb_config_reloads_total`).**
  Deferred to Sprint 5's benchmarking scope, where a richer reload-observability
  story may want more than one counter (close-out grilling Q7).
- **Still open — pre-warming added backends.** Remains an unapproved proposed
  idea; not built in Sprint 4.

### 2.4 S4.T17 changed `validateBackendURL`'s error wording

The spec recorded redaction as a "Further Note"; it landed as its own ticket,
S4.T17 (`db0088f`). All five `validateBackendURL` branches now name only the
backend and the `url` field: the scheme, host, query, and fragment branches drop
their `%q` URL argument, and the parse branch no longer wraps `url.Parse`'s
URL-bearing error — its cause can embed a fragment of the resolved value, so
the branch reports only that the url is invalid. This is a deliberate
message-wording change to already-shipped code: after S4.T16 interpolation the
raw URL can carry a resolved secret, and `main` logs validation errors. No test
asserted the old wording, so nothing else moved. The registry's own URL-parse
error is unreachable for config-sourced URLs (`validateBackendURL` already runs
`url.Parse`, and `app.Build` documents `cfg must already have passed Validate`),
so `internal/backend/registry.go` is unchanged.

### 2.5 Other spec-to-implementation adaptations

- **S4.T5 half-open trial wedge (owner-approved mid-task scope amendment).** The
  spec's "client-gone reaches no observer" rule would have left a half-open
  circuit's single trial permanently taken when a client cancelled mid-trial,
  denying every later request to a live, selectable backend until restart. Fixed
  in-task by `Backend.RearmTrial()`, recorded in ADR-0017 (amending ADR-0012)
  and noted as an owner-approved scope amendment in `PROGRESS.md`.
- **S4.T3 published reload-added backends healthy at the snapshot swap.** The
  two-axis review caught that `Registry.Apply` marked added backends healthy
  before `checker.Add` ran, leaving a window where an unproven URL could receive
  traffic. Fixed at the swap point (commit `ba18398`).
- **S4.T4 same-name re-add drove the active gauge negative.** The review caught
  the removed and re-added same-name instances sharing one
  `lb_active_connections` series; the active seed is now skipped when the name
  is also being removed.
- **S4.T4.0 exported-sentinel rule amendment.** `backend.ErrDrainWindowExpired`
  became a second exported value, contradicting the frozen "only
  `ErrNoHealthyBackends`" convention. The rule was amended explicitly in
  `AGENTS.md`, the contracts doc, and ADR-0016 decision 2 — branchable sentinel
  versus cross-package `context.Cause` comparison value.
- **S4.T14 e2e harness deviations.** The readiness poll consumes one request, so
  the in-flight wait polls against a recorded baseline (`baseline + 1000`) rather
  than a bare `== 1000`; and the live-shift probes are fired asynchronously
  because the unchanged backend is gated and a synchronous probe would block on
  it.
- **S4.T15 was a verification ticket, not an implementation one.** The `make
  e2e` target was landed with S4.T14 (spec story 7); T15 closed by verifying it
  against issue 04's acceptance rather than re-adding it.

## 3. Exit-criteria evidence

| Criterion (MILESTONES) | Evidence |
|---|---|
| SIGHUP with 1000 in-flight requests drops zero | In-process: `TestChaosReloadDrainExitCriterion1000` (`test/chaos/reload_test.go`, S4.T4, commit `630cbae`). OS boundary: `TestChaosSighupReloadZeroDrop1000` (`test/chaos/sighup_e2e_test.go`, S4.T14, commit `a8a6e0c`) — builds and spawns the real binary, sends a real SIGHUP, and proves all 1000 return 200 with the removed backend's count frozen. Run via `make e2e`. |
| `docker kill` a backend mid-response — clean 502, no crash, no leak | `TestChaosBackendKilledBeforeHeaders` (pre-headers death → clean 502, failure observed, no leak) and `TestChaosBackendDiesMidBody` (mid-body death → WARN `backend_died_mid_response`, slot released, the recorded success standing) — S4.T6, `test/chaos/midbody_test.go`. `TestChaosGoroutineLeakAudit` (S4.T9) proves the goroutine count settles back within a delta of 10 after a failure-laden burst. |
| 1-hour soak under load — no goroutine or memory growth | `TestChaosSoakConnectionLifecycle` (S4.T10, commit `7ea2673`), run via `make soak` / `make soak-race`. Committed tolerances: goroutines ≤ warmup baseline + 10, post-GC `HeapAlloc` ≤ baseline + 8 MB. Full-hour pass — cite the `PROGRESS.md` S4.T10 entry as the source of truth: 59.66M requests, 7,175 cancellations, 2,789 failures, 12 reloads, goroutines 35→30, post-GC heap 930 KB→804 KB. |

The deployment-target deliverable is evidenced by ADR-0019 (S4.T19, commit
`3fa0fb0`), which closes the ADR-0005-deferred decision after the reload and
connection-lifecycle work informed it.

## 4. What we'd do differently

- **Agree ticket numbering before grilling, not during.** The owner's
  pre-grilling T11–T15 list collided with the canonical repo tickets, then the
  spec's own T12–T15 numbering collided again with the ten canonical tickets the
  amendment later created. That cost an R0 reconciliation table and a
  re-derivation of every scope boundary. The owner's list should have been built
  from `PROGRESS.md`, or the canonical tickets named as the starting point.
- **Design against the frozen contracts before implementing, especially the
  trial and apply paths.** Two of the sprint's real defects — the S4.T5
  half-open wedge and the S4.T3 added-backend-healthy window — were interaction
  bugs between a new ticket's rule and existing state-machine code. A short
  design pass over the circuit and registry contracts before writing the
  cancellation and apply changes would likely have caught both, avoiding a
  mid-task scope amendment.
- **State the exit criterion's boundary in the ticket that first meets it.** The
  criterion was met in-process in S4.T4, and "the boundary the criterion names is
  not yet proven" had to be reopened as S4.T12 a bundle later. A criterion that
  names the OS boundary should carry an OS-boundary test in the same ticket, or
  the criterion should be worded to match where it is proven.
- **Scope the make target where it belongs.** S4.T15 existed only to verify a
  `make` target already landed by S4.T14; the spec's four-ticket shape became ten
  canonical tickets, which is fine, but the split produced one ticket with
  nothing to build.
- **Record harness semantics in the spec, not the session log.** The e2e's
  readiness-poll baseline and gated-probe async behavior were discovered during
  implementation. Both are correct, but neither was predictable from the spec's
  story text; writing the "in-flight is confirmed by counters" story with the
  baseline arithmetic would have shortened the review.
