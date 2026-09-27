# S4.T12–T15 — Sprint 4 close-out: OS-boundary SIGHUP proof, env interpolation, deployment ADR, retro

Status: ready-for-agent

Bundle spec for the last Sprint 4 tickets. Every design decision recorded here was
locked in the /grill-with-docs session that preceded this file (2026-09-27,
Rounds 1–2, questions Q1–Q7, plus three spec questions settled at the close of
Round 2). The user's pre-grilling ticket list ("S4.T11 zero-drop test … S4.T15
retro") is reconciled against the repo's actual ticket state in decision R0
below; the repo's canonical numbering governs. Vocabulary follows `CONTEXT.md`:
**Backend identity**, **Reload**, **Draining**, **Drain window**, **Selectable**,
**Probe**, **Ejection**.

**Ticket discipline.** Every user story and every implementation decision below
is tagged with exactly one ticket. A ticket builds only what carries its tag.
Where a later ticket changes something an earlier one built, the earlier ticket
says so as a named limitation, not as a TODO it fills itself (AGENTS.md
Step 2.5). The ticket map is the section to check first.

---

## Problem Statement

Sprint 4's engineering is complete, but three things stand between "built" and
"closed":

1. **The first exit criterion is evidenced one layer below where it is
   stated.** "SIGHUP with 1000 in-flight requests drops zero" is met by
   `TestChaosReloadDrainExitCriterion1000`, which drives `App.Reload` directly.
   Every prior bundle deliberately left OS signal delivery and `main`'s reload
   loop untested. The reload mechanism is proven; the boundary the criterion
   names — a real `SIGHUP` delivered by the OS to the real binary — is not.
2. **Deployment secrets have nowhere to live.** Backend URLs are plain strings
   in the YAML config. An operator who needs credentials in a URL (basic-auth
   upstreams, cloud endpoints with keys) must write the secret into the file,
   into version control, into the baked image config, and into every shell
   history that ever touched it.
3. **The sprint cannot close honestly.** The deployment-target ADR deferred
   from Sprint 1 (ADR-0005) was explicitly postponed so the reload and
   connection-lifecycle work would inform it — that work now exists, and the
   ADR was never written. And Sprint 4, like Sprints 1–3, deserves a
   retrospective and an architecture-doc update.

This bundle closes those four things, strictly serial, one ticket per context.

## Solution

Four serial tickets, each blocked by the one before:

1. **S4.T12 — SIGHUP end-to-end zero-drop test.** The exit criterion
   re-demonstrated at the OS boundary: a test that builds the real binary,
   spawns it, holds 1000 requests in flight through gated counting backends,
   rewrites the config file, sends a real `SIGHUP`, and proves zero drops —
   plus, while the requests are held, that live traffic shifts to the added
   backend and never touches the removed one. Carries the amendment-first fold
   (PROGRESS rows + MILESTONES line for T12–T15) as its first commit.
2. **S4.T13 — Config env-var interpolation.** `${VAR}` expansion in backend
   URL strings, resolved at `Load` after decode and before `Validate`, so the
   resolved URL is what validation checks and the environment is the only
   place a secret lives. Unset, empty, or malformed references fail the load
   loudly. Validation error messages stop embedding the raw URL so a resolved
   secret can never reach the logs through an error path.
3. **S4.T14 — Deployment-target ADR.** `docs/adr/0019` records bare binary vs
   Docker vs Kubernetes with the trade-offs, decided in light of the reload
   architecture, the three-listener model, the health-endpoint contract, and
   the distroless image; SO_REUSEPORT process handoff is recorded as
   explicitly post-Sprint-5. Docs-only.
4. **S4.T15 — Sprint 4 retro and architecture-doc update.**
   `docs/sprint-4-retro.md` mirroring the Sprint 3 shape (deliverables table,
   deviations, exit-criteria evidence) and an additive Sprint 4 section in
   `docs/architecture.md`. Docs-only, and last: the retro references the ADR
   and the e2e evidence, so it cannot precede them.

## Reconciliation (R0 — the grilling's numbering decision)

The user's pre-grilling list used a T11–T15 numbering that collides with the
repo's canonical tickets. The grilling reconciled them as follows; this
mapping is the auditable record:

| User's list item | Repo state | Resolution |
|---|---|---|
| S4.T11 zero-drop reload test | Done in S4.T4: `TestChaosReloadDrainExitCriterion1000` | Not rebuilt; the OS-boundary gap it exposed becomes **S4.T12** |
| S4.T12 env-var interpolation | Not scoped, not built | **S4.T13** |
| S4.T13 reload architecture ADR | Done: ADR-0015 | Closed as recorded; not rebuilt |
| S4.T14 retry policy ADR | Done: ADR-0018 (connection-lifecycle S4.T11) | Closed as recorded; not rebuilt |
| S4.T15 retro + architecture doc | Not done | **S4.T15** |

Two proposed-ticket closures ride along in T12's amendment commit: the
"hot-reload rejected, not ignored" ticket is closed (already implemented across
S4.T1/T4.0/T7/T8 — `NonBackendChanges` names `reload`, `server`, `transport`
and every original non-backend field), and the reload-outcome counter metric is
deferred to Sprint 5's benchmarking scope, where a richer reload-observability
story may want more than one counter.

## User Stories

Each story is tagged with the one ticket that delivers it.

### S4.T12 — SIGHUP end-to-end zero-drop test

1. As a reviewer, I want the exit-criterion test to send a real OS `SIGHUP`
   to a real spawned process, so that "SIGHUP with 1000 in-flight requests
   drops zero" is demonstrated at the OS boundary, not only in-process.
2. As a reviewer, I want the test to hold all 1000 requests in flight across
   the signal, so that the zero-drop claim covers requests admitted before and
   completing after the reload.
3. As a reviewer, I want the test to prove live traffic shifts across the
   signal — new requests after `SIGHUP` land on the added backend and never on
   the removed one — so that admission and retirement are proven through the
   real binary, not only the application seam.
4. As a reviewer, I want the test to prove the removed backend's request count
   freezes at its pre-signal value, so that retirement is immediately effective
   through the real signal path.
5. As an operator, I want the reload triggered by rewriting the same config
   file the process was started with, so that the e2e exercises the
   single-source-of-truth path production uses.
6. As an operator, I want the test to shut the spawned process down with
   `SIGTERM` and assert a clean exit, so that reload and graceful shutdown are
   proven to compose.
7. As a maintainer, I want the e2e test behind its own `make` target, so that
   `make test` stays hermetic and fast (precedent: `make soak`).
8. As a maintainer, I want the e2e test to build the binary it runs from the
   current source, so that it can never test a stale artifact.
9. As a maintainer, I want the e2e test to skip cleanly when no Go toolchain is
   available, so that the target degrades gracefully off a development machine.
10. As a maintainer, I want the e2e harness to reuse the chaos package's
    gated-counting backend vocabulary, so that in-process and e2e tests share
    one failure-injection pattern.
11. As a maintainer, I want the e2e's in-flight wait to use a deadline sized for
    process spawn and real socket setup, so that it does not flake on loaded
    machines.
12. As a maintainer, I want the e2e to raise its file-descriptor limit
    best-effort at start, so that 1000 concurrent client and backend
    connections survive the default soft limit on laptops.
13. As a maintainer, I want the division of labor stated explicitly: registry
    views (instance identity, EWMA and circuit state survival) stay
    in-process, and the e2e asserts externally visible outcomes only (status
    codes, per-backend counts), so that neither test duplicates the other.

### S4.T13 — Config env-var interpolation

14. As an operator, I want to write `${VAR}` in a backend URL, so that a
    password or token stays out of the config file, version control, and the
    baked image config.
15. As an operator, I want the variable resolved from the process environment
    at load time, so that one config file works across environments.
16. As an operator, I want several variables in one URL, so that host, port,
    and credentials can each come from the environment.
17. As an operator, I want variables adjacent to literal text, so that I can
    write URLs like `http://${USER}:${PASS}@${HOST}:8080`.
18. As an operator, I want an unset variable to fail the load naming the
    backend and the variable, so that a typo is caught at startup, never at
    request time.
19. As an operator, I want an empty variable to fail the same way, so that an
    empty password cannot silently produce a broken URL.
20. As an operator, I want a malformed reference — unterminated `${`, empty
    `${}`, or an invalid name — to fail the load, so that template typos are
    loud rather than silently literal.
21. As an operator, I want interpolation to apply to backend URLs only, so
    that the feature's blast radius stays small and predictable.
22. As an operator, I want a `${...}` in a non-backend field to pass through
    untouched, so that the loader does not own syntax it cannot validate.
23. As an operator, I want the resolved URL to be what validation checks, so
    that a typo in the expanded value is caught by the existing URL
    validation.
24. As an operator, I want validation error messages to never contain the
    expanded URL, so that a resolved secret cannot reach the logs through an
    error path.
25. As an operator, I want a reload to re-interpolate from the current
    environment, so that changing the environment between reloads resolves to
    the new value.
26. As an operator, I want an environment change to be treated as a backend
    identity change — one removed plus one added backend — so that state
    learned about the old URL is never applied to a new one (ADR-0015 identity
    rule, S4.T1).
27. As an operator, I want `configs/example.yaml` to document interpolation,
    so that the feature is discoverable (precedent: `reload.drain_window`).
28. As a maintainer, I want the shipped `configs/docker.yaml` to remain
    interpolation-free, so that the baked image config needs no environment.
29. As a maintainer, I want no new exported API surface for interpolation, so
    that the loader's contract is identical for callers that never use
    variables.

### S4.T14 — Deployment-target ADR

30. As a maintainer, I want the deployment target recorded as an ADR, so that
    the choice is defensible and reviewable instead of assumed.
31. As a maintainer, I want the decision informed by the in-process reload
    architecture, the health-endpoint contract, the three-listener model, and
    the container artifacts, so that it follows from the system actually
    built.
32. As an operator, I want the trade-offs between bare binary, Docker, and
    Kubernetes recorded against this project's real constraints, so that the
    next deployment decision starts from evidence.
33. As a maintainer, I want SO_REUSEPORT process handoff recorded as
    explicitly post-Sprint-5, so that the in-process swap's limits are
    honestly stated (MILESTONES Post-Sprint-5 list).
34. As a maintainer, I want the ADR index row added, so that the decision is
    discoverable.

### S4.T15 — Sprint 4 retro and architecture-doc update

35. As a maintainer, I want a Sprint 4 retrospective mirroring the Sprint 3
    shape, so that the sprint closes with an auditable account of what shipped
    and what drifted.
36. As a maintainer, I want the deliverables table to carry the commit that
    landed each ticket, so that the retro is traceable.
37. As a maintainer, I want the deviations section to record the R0 numbering
    reconciliation and the closed/deferred proposed tickets, so that the
    sprint's scope history is honest.
38. As a reviewer, I want each exit criterion restated with its evidence, so
    that Sprint 4's close-out is provable rather than asserted.
39. As a new contributor, I want `docs/architecture.md` updated with the Sprint
    4 subsystems — app seam, reload and drain, connection lifecycle,
    transport tuning, soak results, env interpolation, e2e SIGHUP test — so
    that the architecture reference stays true.
40. As a new contributor, I want the soak results recorded in the architecture
    doc, so that the production-resilience claims have numbers.
41. As a maintainer, I want the architecture doc's completeness header updated,
    so that it no longer says "Sprints 1–3".

### Amendment-first (folded into S4.T12)

42. As a maintainer, I want the PROGRESS rows and MILESTONES line for T12–T15
    to land before any close-out code, so that scope is auditable first
    (amendment-first precedent of S4.D0/S4.D1, folded into T12 because the
    confirmed numbering carries no separate amendment ticket).

## Implementation Decisions

### Ticket map

| Ticket | Blocked by | Delivers | ADR | Explicitly NOT in this ticket |
|--------|-----------|----------|-----|-------------------------------|
| S4.T12 SIGHUP e2e zero-drop | S4.T11 (retry ADR — bundle opens after the connection-lifecycle bundle closes) | stories 1–13, 42 | none | a unit test of the one-line signal registration; concurrency stress under `-race` for its own sake; interpolation-aware config loading |
| S4.T13 env interpolation | T12 | stories 14–29 | none (redaction recorded as a Further Note) | non-backend interpolation; default-value syntax; an escape hatch for literal `${` |
| S4.T14 deployment ADR | T13 | stories 30–34 | ADR-0019 written here | any implementation tied to the chosen target |
| S4.T15 retro + architecture doc | T14 | stories 35–41 | none | new subsystem documentation beyond `docs/architecture.md` |

Named hand-offs between tickets (each is a documented limitation of the
earlier ticket, closed by the later one — not work the earlier ticket
half-builds):

- **T12 → T13:** T12 proves the reload path — signal, file re-read, parse,
  validate, apply — before T13 adds a config feature that flows through it.
  T13's tests may use T12's technique (spawn the binary) or the in-process
  seam; the dependency is ordering, not mechanism.
- **T13 → T14:** none structural; the ADR follows the interpolation work
  only in the sprint narrative.
- **T14 → T15:** the retro references the ADR as shipped evidence, so the
  ADR must land first.

### R0 — Numbering reconciliation

See the table in the reconciliation section above. The user's pre-grilling
T11–T15 list is folded into the repo's canonical numbering as S4.T12–S4.T15;
nothing is renumbered, rebuilt, or resurrected.

### R1 — Amendment-first folded into T12 (grilling Q7, confirmed)

T12's first commit lands the tracking amendment alone, per the S4.D0/S4.D1
precedent: PROGRESS.md gains a "Sprint 4 — Close-out" section with the four
ticket entries and their acceptance criteria, and MILESTONES.md's Sprint 4
deliverables gain one line for interpolation (inserted after the
connection-pool-tuning bullet): "Config env-var interpolation for backend URLs
(`${VAR}`), so deployment secrets stay out of the config file and validation
errors never echo resolved values." The two proposed-ticket closures (stale
hot-reload ticket closed, reload metric deferred to Sprint 5) are recorded in
PROGRESS in the same commit. No code in the amendment commit.

### S4.T12 — SIGHUP end-to-end zero-drop test

- **New test file** in the chaos package (`test/chaos`, external
  `package chaos_test`), one test: `TestChaosSighupReloadZeroDrop1000`. It is
  the OS-boundary sibling of `TestChaosReloadDrainExitCriterion1000`; the two
  are explicitly non-overlapping (story 13). The in-process test keeps the
  registry-view assertions (instance identity, EWMA/circuit survival); the
  e2e keeps only externally visible outcomes (status codes, per-backend
  request counts, clean exit).
- **Build the binary under test** with `go build -C` pointed at the module
  root, output into `t.TempDir()`. Skip cleanly via `exec.LookPath("go")`
  when no toolchain is present.
- **Spawn** the binary with `-config <tempdir>/config.yaml`, stdout/stderr
  captured and replayed through `t.Logf` on failure. Readiness is a poll
  against the client listener: any HTTP response (even 503) means the server
  is up.
- **Gated counting backends**: the chaos harness gains an additive per-backend
  atomic request counter (incremented at request entry, before gating), so a
  test can assert traffic shifted or froze without reading proxy internals.
  Existing tests are unaffected (the counter is additive; `ServeGated`
  unchanged). The e2e's config sets the health probe interval far past the
  test window (precedent: `reloadChaosConfig`), so counters see only
  deliberate traffic. The added backend's admission probe is accounted for by
  recording its counter baseline after admission and asserting post-signal
  growth.
- **Client listener port**: the config file needs a concrete `listen`; the
  test grabs a free port by opening and closing a `127.0.0.1:0` listener and
  uses the assigned port (the small race is accepted; alternatives are
  strictly worse). Metrics and health-endpoint listens use `:0`, which is
  valid for a real process and never contacted by the test.
- **The 1000 in flight**: 1000 concurrent GETs through one `http.Client` to
  the client listener; the gated handlers hold them. In-flight is confirmed
  by polling the per-backend counters until they sum to 1000 (mirroring the
  in-process test's `activeTotal` wait), not by sleeping.
- **Config rewrite**: the post-signal config (one backend added, one removed,
  `drain_window` unchanged — it is not reloadable, ADR-0016) is written to a
  temp file in the same directory and moved over the config path with
  `os.Rename`, so the reload loop's re-read sees it atomically.
- **The signal**: `syscall.Kill(process.Pid, syscall.SIGHUP)`.
- **Live-shift proof**: while the 1000 are held, fire probe requests through
  the client listener; assert they land on the added backend (counter grows
  past its admission baseline) and that the removed backend's counter stays
  frozen. This is the assertion the in-process test structurally cannot make
  (all its selections happen before the reload).
- **Release and assert**: release the gates, collect all 1000 statuses, every
  one must be 200; the removed backend's counter must equal its pre-signal
  value; the process must still be alive.
- **Teardown**: `SIGTERM`, wait with a timeout, assert exit status 0.
- **File-descriptor headroom**: the test raises `RLIMIT_NOFILE` soft to hard
  at start, best-effort (Darwin/Linux allow it; failure is logged, not
  fatal), and the `make` target's help text documents a `ulimit -n 10240`
  invocation for restrained environments. 1000 held client connections plus
  1000 backend connections exceed the default 256 soft limit on laptops.
- **Deadlines**: the in-flight wait uses a constant sized for process spawn
  and real socket setup (30s suggested; the in-process suite's 3s does not
  apply across a process boundary).
- **Make target**: `make e2e` runs the single test with a generous `-timeout`.
  One target, no race variant: the e2e's subject is the OS boundary — signal
  delivery, process lifecycle, real sockets — and the concurrency claims are
  already covered by the in-process suite under `-race`. Running the e2e
  under `-race` would test the race detector's overhead, not the system.

### S4.T13 — Config env-var interpolation

- **Seam**: `config.Load` gains one expansion step after YAML decode and
  before the config is returned; `Validate` and every consumer see only
  resolved values. `Load`'s doc comment is updated to state the expansion
  rule (it was "pure deserialization"; resolving the file's template syntax
  at read time is a deliberate, documented extension).
- **Syntax**: `${VAR}` where `VAR` matches `[A-Za-z_][A-Za-z0-9_]*`. No
  default syntax (`${VAR:-x}` is not parsed). Non-backend fields pass
  through untouched — a `${...}` elsewhere stays literal (story 22).
- **Expansion algorithm**: scan for `${`; read the variable name; look it up
  in the process environment. Unset **or empty** → load error naming the
  backend and the variable. Malformed references — unterminated `${`, empty
  `${}`, or a name with invalid characters → load error naming the backend
  (the offending template text is not a secret and may be quoted; the
  expanded value must never be).
- **Field scope**: backend URL strings only. The loader's contract for every
  other field is unchanged.
- **Validation**: the resolved URL is validated by the existing
  `validateBackendURL`, so a typo in an expanded value fails exactly like a
  literal typo (story 23).
- **Redaction (grilling Q6, landed)**: `validateBackendURL`'s error messages
  drop the raw URL and name only the backend and the field, so a resolved
  secret cannot reach the logs through the error path (`main` logs load and
  validation errors). Verified: no test asserts the message wording. The
  registry's URL-parse error is unreachable for config-sourced URLs —
  `validateBackendURL` already runs `url.Parse` — so no second change is
  needed; the redaction is structural, not a scrubbing pass.
- **Reload semantics**: interpolation re-runs on every `Load`, so an
  environment change between reloads resolves to the new value; since backend
  identity is `(name, URL)` (ADR-0015), a changed expansion is one removed
  plus one added backend — state learned about the old URL is never applied to
  the new one.
- **Config examples**: `configs/example.yaml` documents the feature;
  `configs/docker.yaml` stays interpolation-free (its round-trip test,
  `TestDockerConfig`, is untouched).
- **No new API**: no exported symbol, no `Config` shape change. Callers that
  never use variables see an identical contract (story 29).

### S4.T14 — Deployment-target ADR

- **Artifact**: `docs/adr/0019-*.md`, index row added. Docs-only.
- **Context the ADR must cite**: ADR-0005 and its 2026-09-22 amendment (the
  demo stack does not settle the target), ADR-0014 (the health-endpoint
  contract and its orchestrator-probe semantics), ADR-0015 (the in-process
  reload architecture and why no socket handoff exists), the distroless
  image and `probe` subcommand (S3.T10), and the repo-root compose demo stack
  (S3.T11).
- **Decision options**: bare binary, Docker, Kubernetes — each against the
  system's actual properties: three listeners, SIGHUP-driven in-process
  reload, a self-probe HEALTHCHECK, no external state, no clustering.
- **Explicit non-decision**: SO_REUSEPORT socket handoff for reload across
  process restart stays on the MILESTONES Post-Sprint-5 extension list; the
  ADR records this so the in-process swap's limit is on the record.
- **The call is the owner's**: the grilling settled *when* and *why* (the
  informing work now exists); the target itself is decided in the ADR-writing
  session, not pre-baked into this spec.

### S4.T15 — Sprint 4 retro and architecture-doc update

- **Retro artifact**: `docs/sprint-4-retro.md`, mirroring
  `docs/sprint-3-retro.md`'s shape: a deliverables-shipped table with the
  commit that landed each ticket, a deviations section, exit-criteria
  evidence, and a what-we-do-differently section. The deviations must
  include: the R0 numbering reconciliation; the in-process-first exit
  criterion now evidenced at the OS boundary; the closed and deferred
  proposed tickets; and any message-wording change T13 lands in
  `validateBackendURL`.
- **Exit-criteria evidence**: 1000-in-flight zero-drop (in-process
  `TestChaosReloadDrainExitCriterion1000` **and** the e2e SIGHUP test from
  T12); backend death mid-response → clean 502 (S4.T6); one-hour soak with
  committed tolerances (S4.T10: 59.66M requests, 7,175 cancellations, 2,789
  failures, 12 reloads, goroutines 35→30, post-GC heap 930 KB→804 KB).
- **Architecture doc**: additive Sprint 4 sections in `docs/architecture.md`
  — the app seam, reload and drain, connection lifecycle, transport tuning,
  soak results, env interpolation, and the e2e test — plus the decision-index
  rows for ADR-0015 through ADR-0019. The header's "Sprints 1–3 are complete"
  reference is updated to reflect the Sprint 4 close-out.

## Testing Decisions

A good test exercises external behaviour through the highest seam that can
observe it. Per ticket:

- **T12 — the real process.** The only seam that can prove the exit criterion
  at the OS boundary is a spawned binary sent a real signal. External
  outcomes only: HTTP statuses, per-backend counters, process exit status —
  never the registry or snapshot internals (those stay in-process, story 13).
  Prior art: the in-process exit-criterion test (`reload_test.go`), the
  gated flippable-backend harness (`helpers_test.go`), the soak's flag- and
  target-gating precedent (`make soak`, `-soak` flag).
- **T13 — the loader's contract.** Table-driven `Load` tests with `t.Setenv`
  for expansion, unset/empty/malformed failures, passthrough, and multi-var
  URLs; one composition test loading two configs that differ only in
  environment and diffing them (remove+add of the affected identity).
  Prior art: `config_load_test.go`, `config_validate_test.go`,
  `docker_config_test.go` (the baked-config round-trip that must stay
  interpolation-free), `diff_test.go`.
- **T14, T15 — none.** Docs-only tickets, TDD-exempt per AGENTS.md.
- Concurrency and timing: T12 runs outside `-race` by design (see ticket
  decisions); its waits are deadline-based polls, never sleeps. T13's tests
  are pure functions over strings and environment — no concurrency.

## Out of Scope

Recorded here so none of this is built inline:

- A unit test of `main`'s one-line signal registration. T12 covers the whole
  path end-to-end; a unit test of `signal.NotifyContext` would test the
  standard library.
- A reload-outcome counter metric. Deferred to Sprint 5 benchmarking scope
  (grilling Q7).
- Interpolation of non-backend fields, default-value syntax, or an escape
  hatch for a literal `${` in a URL. Malformed references error (R-syntax
  decision above); there is deliberately no escape hatch.
- Hot-reload rejection work. The proposed ticket is closed — already
  implemented across S4.T1/T4.0/T7/T8.
- Pre-warming added backends (probing before the swap). Still a proposed
  idea, not approved.
- SO_REUSEPORT process handoff, gRPC, rate limiting, WebSocket upgrade
  validation, HTTP/2 — all Post-Sprint-5 per MILESTONES.
- Any restructuring of the chaos harness beyond the additive counter.
- New MILESTONES deliverables beyond the single interpolation line (R1).

## Further Notes

- **Frozen contracts touched**: none structurally. `Config` keeps its shape
  (URL stays a string); `Load`'s doc comment gains the expansion rule;
  `validateBackendURL`'s error message wording drops the raw URL (redaction,
  grilling Q6). The sprint-1 contracts doc needs no change.
- **Log vocabulary**: no new events or reasons. Interpolation introduces no
  new log lines; reload lines are unchanged.
- **Make targets**: one addition, `make e2e`. `make test` and `make
  test-race` are unchanged (the e2e is opt-in).
- **Go toolchain**: `make e2e` requires `go` on PATH (the test skips
  cleanly otherwise) and builds the binary under test from current source.
- **Environment sensitivity**: T13's tests mutate process environment via
  `t.Setenv`, which is unsafe for parallel tests — the interpolation tests
  must not call `t.Parallel()` (consistent with the existing config tests).
- **Ticket files**, one per ticket under this directory's `issues/`, strictly
  serial, each `Blocked by:` the one before: `01` S4.T12, `02` S4.T13, `03`
  S4.T14, `04` S4.T15.
