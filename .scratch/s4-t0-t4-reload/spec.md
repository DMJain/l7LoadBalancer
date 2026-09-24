# S4.T0–T4 — Zero-downtime SIGHUP reload: app seam, config diff, registry swap, orchestration, draining

Status: ready-for-agent

Bundle spec for the first Sprint 4 tickets. Every design decision recorded here
was locked in the /grill-with-docs session that preceded this file (Rounds 1–4,
decisions D1–D22, questions Q1–Q27). Kickoff prompts for the individual tickets
consume this spec directly. Vocabulary follows `CONTEXT.md`: **Backend
identity**, **Reload**, **Unchanged / added / removed backend**, **Draining**,
**Drain window**, **Selectable**, **Probe**, **Ejection**.

**Ticket discipline.** Every user story and every implementation decision below
is tagged with exactly one ticket. A ticket builds only what carries its tag.
Where a later ticket changes something an earlier one built, the earlier ticket
says so as a named limitation, not as a TODO it fills itself (AGENTS.md
Step 2.5). The ticket map is the section to check first.

---

## Problem Statement

An operator running the load balancer today cannot change the backend fleet
without restarting the process. Adding a backend, retiring one, or pointing a
name at a new host means stopping the process, which drops every in-flight
request and resets every piece of runtime state the load balancer has learned —
health, circuit state, active-connection counts, EWMA latency — for backends
that did not change at all. The backend set is fixed at startup in five
places: the registry's slice, the active health checker's one-goroutine-per-
backend fan-out, the consistent-hash ring built once at selector construction,
the passive outlier detector's per-backend windows, and the per-backend metric
series seeded once at startup.

There is also no programmatic seam for exercising the running system. The
chaos tests duplicate `main`'s wiring by hand, and the Sprint 3 retro recorded
that duplication as debt. A reload path added directly to `main` would make
`main` larger and its most important new behaviour — "a reload with 1000
requests in flight drops zero" — testable only by spawning processes and
sending real signals.

Sprint 4's first exit criterion is exactly that: SIGHUP with 1000 in-flight
requests drops zero.

## Solution

Six sequential tickets, each blocked by the one before:

0. **S4.D0 — Tracking amendment** (docs-only). `MILESTONES.md` Sprint 4 gains
   the application seam as a deliverable; `PROGRESS.md` gains S4.T0–T4 entries
   with acceptance criteria and the four proposed follow-up tickets. Lands
   before any code, per the Sprint 3 amendment-first precedent.
1. **S4.T0 — Application seam.** A new internal package owns the whole wiring
   graph behind a build step and a run step. `main` shrinks to flags, file I/O,
   and signals. The chaos-test assembly delegates to build.
2. **S4.T1 — Config diffing.** Pure functions: added / removed / unchanged
   backends by backend identity (name, URL), and the non-backend fields that
   differ. ADR-0015 (reload architecture) is written first.
3. **S4.T2 — Atomic swap.** The registry's backend set becomes one immutable
   versioned snapshot behind an atomic pointer, replaced by an apply step. A
   removed backend reports nothing to observers. The consistent-hash ring
   rebuilds on demand. The loaded config is held as an atomic-pointer record.
4. **S4.T3 — SIGHUP orchestration.** The reload loop and the application's
   reload operation: parse → validate → reject on non-backend change → diff →
   apply → hook up the checker, outlier detector, and metrics → log. Added
   backends start unhealthy and are admitted by one successful probe.
5. **S4.T4 — Draining.** `reload.drain_window` (default 30s); each removed
   backend drains until idle or the window expires, then its remaining requests
   are cancelled with 502s that count as no backend's failure. ADR-0016 (drain
   lifecycle) is written first. Carries the exit-criterion test.

From the operator's side: edit the backend list, send SIGHUP, and routing moves
to the new fleet immediately — unchanged backends keep everything they knew,
new backends take traffic once they have answered one probe, removed backends
finish what they were doing within a bounded window — and a bad edit is
rejected with a log line while the old config keeps serving.

## User Stories

Each story is tagged with the one ticket that delivers it.

### Operator — reloading

1. [T3] As an operator, I want to send SIGHUP to reload the backend list from the config file, so that I can change the fleet without restarting the process.
2. [T3] As an operator, I want the reload to re-read the same config path the process was started with, so that there is one source of truth for configuration.
3. [T3] As an operator, I want in-flight requests to unchanged backends to complete normally across a reload, so that a reload is invisible to their clients.
4. [T2] As an operator, I want unchanged backends to keep their health, circuit state, active-connection count, and EWMA latency across a reload, so that the load balancer does not forget what it learned about a backend I didn't touch.
5. [T1] As an operator, I want a backend whose URL I changed (same name) to be treated as one removed plus one added backend, so that state learned about the old host is not applied to a different host.
6. [T3] As an operator, I want a backend I added to receive traffic only after it has answered one successful probe, so that a typo'd URL does not produce client-visible 502s.
7. [T2] As an operator, I want a backend I removed to stop receiving new requests immediately, so that retirement takes effect at once.
8. [T3] As an operator, I want a removed backend's in-flight requests to be allowed to finish, so that retiring a backend does not cut off its clients.
9. [T4] As an operator, I want a configurable drain window that bounds how long a removed backend may keep requests in flight, so that a stuck backend cannot linger forever.
10. [T4] As an operator, I want requests still in flight on a removed backend when the drain window expires to be cancelled with a 502, so that the bound is real and observable.
11. [T4] As an operator, I want a drain to finish early when the removed backend becomes idle, so that I don't wait out the whole window for nothing.
12. [T4] As an operator, I want the drain window to default to 30s when I omit it, so that the common case needs no configuration.
13. [T4] As an operator, I want a zero, negative, or malformed drain window rejected at load time, naming the field, so that a typo is caught early.
14. [T3] As an operator, I want a reload whose file fails to parse to be rejected while the previous config keeps serving, so that a broken edit never takes the load balancer down.
15. [T3] As an operator, I want a reload whose file fails validation rejected the same way, so that invalid fleets never reach the diff.
16. [T3] As an operator, I want a reload that changes any field other than the backend list rejected whole, with the log line naming the offending fields, so that an edit that would silently do nothing is surfaced instead of ignored.
17. [T3] As an operator, I want each reload diffed against the currently loaded config, not the startup config, so that successive reloads compose.
18. [T3] As an operator, I want several SIGHUPs in quick succession to collapse into at most one pending reload, so that reloads never overlap or pile up.
19. [T4] As an operator, I want an earlier reload's drain to run to completion independently of later reloads, so that re-adding a backend neither revives nor cuts short a drain already in progress.
20. [T2] As an operator, I want re-adding a removed backend identity to create a fresh backend instance, so that the removed instance and the fresh one never share state.
21. [T2] As an operator, I want the order of backends after a reload to follow the new file's order, so that the file dictates tie-break order.
22. [T3] As an operator, I want a reload that replaces every backend (blue/green) to be allowed, so that I can migrate to a new fleet.
23. [T3] As an operator, I want such a reload logged at WARN with `unchanged=0`, so that I understand the brief window with nothing selectable.
24. [T4] As an operator, I want SIGINT/SIGTERM to still shut the process down gracefully while drains are in progress, so that reload never blocks shutdown.

### Operator — observability

25. [T3] As an operator, I want one "config reloaded" log line per successful reload carrying the added, removed, and unchanged counts, so that I can confirm what a reload did.
26. [T3] As an operator, I want one "config reload failed" log line per rejected reload carrying the reason, so that I can fix the file.
27. [T3] As an operator, I want a reload-added backend's admission logged as a health reinstatement with reason `initial_probe`, so that I can tell first admission apart from recovery (`probe_recovered`).
28. [T3] As an operator, I want a reload-added backend's failing first probe to log nothing, so that a backend that was never healthy produces no false transition.
29. [T3] As an operator, I want a reload-added backend's health gauge to start at 0 and flip to 1 on admission, so that the dashboard shows it is not yet serving.
30. [T3] As an operator, I want a reload-added backend's active-connections and circuit-state series to exist from the moment it is added, so that the dashboard renders it immediately.
31. [T3] As an operator, I want a removed backend's health and circuit-state series deleted when it is removed, so that the dashboard stops showing a backend that is no longer routable.
32. [T4] As an operator, I want a removed backend's active-connections series deleted when its drain finishes (unless a current backend has the same name), so that its in-flight count stays visible while it drains and never goes negative.
33. [T4] As an operator, I want one "backend drained" log line per drain with reason `idle` or `window_expired` and the number of requests cancelled, so that I know how each retirement ended.
34. [T4] As an operator, I want each drain-cancelled request's 502 logged with a `window_expired` reason, so that I can distinguish drain cancellations from backend failures.
35. [T2] As an operator, I want round trips that complete on a removed backend to affect no circuit, outlier, or health state or gauge, so that a same-name fresh backend's dashboard reflects only its own behaviour.
36. [T3] As an operator, I want a probe that was in flight when its backend was removed to produce no transition log or gauge write, so that removals never emit ghost transitions.
37. [T3] As an operator, I want `/readyz` to keep reflecting the live selectable set across a reload, so that an upstream failover sees the brief empty window of a blue/green reload.
38. [T3] As an operator, I want `/startupz` to stay 200 once latched, even when a reload adds backends not yet probed, so that a reload never makes an orchestrator think the process restarted.

### Selection and request path

39. [T2] As a request, I want to be routed only among the current snapshot's selectable backends, so that a removed backend never receives me after the swap.
40. [T2] As a consistent-hash user, I want the ring to reflect the reloaded backend set, so that added backends take their share of keys and removed ones stop owning any.
41. [T2] As a consistent-hash user, I want keys whose owner was unchanged to keep sticking to it where the ring allows, so that session affinity survives reloads as far as consistent hashing permits.
42. [T2] As a consistent-hash user, I want the ring never cached stale under a newer snapshot version, so that a reload is always reflected.
43. [T2] As a user of the other three selectors, I want reload to cost them nothing extra, so that round-robin, least-connections, and P2C-EWMA pick up the new set through the snapshot they already read on every request.
44. [T4] As a request in flight on a removed backend whose drain window expired before response headers arrived, I want a clean 502, so that I fail fast rather than hang.
45. [T4] As a request whose body was streaming when the drain window expired, I accept a truncated body, and the backend's already-recorded success stands, so that a backend that really did answer is not recorded as failing.

### Maintainer / contributor

46. [D0] As a maintainer, I want `MILESTONES.md` and `PROGRESS.md` amended with the S4 tickets and their acceptance criteria before any code, so that scope is auditable before it is built.
47. [D0] As a maintainer, I want the four deferred follow-ups recorded as proposed tickets, so that they are not lost and not built inline.
48. [T0] As a maintainer, I want the whole wiring graph in one importable internal package, so that tests and `main` build the system the same way.
49. [T0] As a maintainer, I want the chaos tests to build the system through that package, so that the Sprint 3 duplication debt is closed and wiring cannot drift between production and tests.
50. [T0] As a maintainer, I want `AGENTS.md`'s package graph and directory map to show the new package, so that the orientation doc stays true.
51. [T1] As a maintainer, I want config diffing as pure functions over two configs, so that it is exhaustively table-testable.
52. [T1] As a maintainer, I want the reload architecture recorded in ADR-0015 before any reload code, so that the design is defensible and reviewable before it is built.
53. [T2] As a maintainer, I want the snapshot version and backend set read in one atomic load, so that correctness comes from structure rather than a read-order rule.
54. [T2] As a maintainer, I want removed backends marked removed before the snapshot swap, so that any request selected before the swap already sees the flag when it completes.
55. [T2] As a maintainer, I want a single rule — a removed backend reports nothing to observers — so that the observers themselves stay ignorant of reload.
56. [T3] As a maintainer, I want the application's reload operation to take an already-parsed config, so that integration tests drive reloads without temp files or signals.
57. [T3] As a maintainer, I want `main`'s reload loop to take the signal channel and logger as parameters, so that it is testable with a fake signal channel and a captured logger.
58. [T3] As a maintainer, I want the active checker to add and remove probers individually, so that reload never restarts probing of unchanged backends.
59. [T3] As a maintainer, I want the reload log events and the `initial_probe` reason added to the frozen log vocabulary, so that log strings are never invented at call sites.
60. [T4] As a maintainer, I want the drain events and reasons added to the frozen log vocabulary in the ticket that first emits them, so that vocabulary lands with its first user.
61. [T4] As a maintainer, I want the drain signal delivered through a per-backend context joined into each request, so that the hot path pays one registration per request and no goroutine per request.
62. [T4] As a maintainer, I want a benchmark showing that per-request cost, so that the hot-path claim is evidenced, not asserted.
63. [T4] As a maintainer, I want drain completion detected by polling active connections, so that the request path pays nothing for drain bookkeeping.
64. [T4] As a maintainer, I want the drain lifecycle recorded in ADR-0016 before the drain code, so that it is defensible.
65. [T4] As a reviewer, I want an exit-criterion test that holds 1000 requests open across a reload and sees all 1000 succeed, so that the Sprint 4 criterion is demonstrated, not claimed.
66. [T4] As a reviewer, I want that test to also prove unchanged backends kept their state (same instance, circuit and EWMA not reset), so that the identity contract is demonstrated end to end.
67. [T4] As a reviewer, I want a second case where the drain window expires first, proving the 502s and the untouched state of a same-name fresh backend, so that the window and the suppression rule are both demonstrated.

## Implementation Decisions

### Ticket map

| Ticket | Blocked by | Delivers | ADR | Explicitly NOT in this ticket |
|--------|-----------|----------|-----|-------------------------------|
| S4.D0 tracking amendment | — | stories 46–47 | none | any code; any spec change |
| S4.T0 application seam | D0 | 48–50 | none (package decision recorded in PROGRESS + AGENTS.md) | a reload operation; any behaviour change |
| S4.T1 config diffing | T0 | 5, 51–52 | ADR-0015 written first | using the diff anywhere; the `reload` config block |
| S4.T2 atomic swap | T1 | 4, 7, 20, 21, 35, 39–43, 53–55 | covered by ADR-0015 | anything that calls apply outside tests; unhealthy start for added backends; checker/outlier/metrics hook-up |
| S4.T3.0 hook-up APIs | T2 | mechanism for 27–31, 36, 58 | covered by ADR-0015 | the reload operation; the reload loop; reload log events |
| S4.T3 orchestration | T3.0 | 1–3, 6, 8, 14–18, 22–23, 25–26, 37–38, 56–57, 59 | covered by ADR-0015 | drain window; retired context; drain log events; deleting the active-connections series |
| S4.T4.0 drain-cancel join | T3 | 10, 12–13, 44–45, 61–62, 64 | ADR-0016 written first | drain goroutine; `backend drained` event; exit test |
| S4.T4 draining | T4.0 | 9, 11, 19, 24, 32–34, 60, 63, 65–67 | covered by ADR-0016 | client-cancel classification; reload metric; pre-warm |

Named hand-offs between tickets (each is a documented limitation of the earlier
ticket, closed by the later one — not work the earlier ticket half-builds):

- **T2 → T3:** after T2, apply exists and is exercised only by tests; nothing
  in production calls it. Backends created by apply start however construction
  starts them (healthy). T3 changes added backends to start unhealthy.
- **T3 → T4:** after T3, a removed backend's in-flight requests finish with no
  bound, and its active-connections series is left in place (it cannot be
  deleted at removal: in-flight requests still decrement it by name, which
  would recreate it negative). T4 bounds the drain and deletes the series at
  drain completion.
- **T1 → T4:** T1's non-backend comparison covers every field that exists at
  T1. T4 adds the `reload` block and extends the comparison to include it.

### S4.D0 — Tracking amendment (D1, D2)

- `MILESTONES.md` Sprint 4 deliverables gain the application seam (from the
  Sprint 3 retro's handoff). Zero-downtime SIGHUP reload is already listed; its
  wording is untouched.
- `PROGRESS.md` gains S4.T0–T4 entries with acceptance criteria drawn from this
  spec's ticket sections, and the four follow-ups under *Proposed tickets*.
- Docs-only, TDD-exempt; lands alone as one commit touching only those two
  files.

### S4.T0 — Application seam (D3, D4)

- A new internal package (approved in the grilling) owns the wiring graph:
  metrics collector, backend registry with seeded series, circuit breaker as
  registry gate and observer, selector from config, proxy with every
  round-trip observer registered, active health checker, and the client,
  metrics, and health-endpoint servers.
- Its interface in T0 is two operations: **build** from a validated config and
  a logger, returning an application value that exposes the client handler,
  the collector, and the registry (what the chaos tests read today); and
  **run**, which serves until the context is cancelled and then performs the
  existing graceful shutdown sequence. No reload operation exists in T0.
- `main` keeps flags, the `probe` subcommand, config loading, and signal
  handling, and becomes: flags → load → validate → build → run. Startup log
  lines are unchanged.
- The chaos-test assembly delegates to build instead of duplicating wiring;
  chaos assertions are unchanged.
- The new package sits above `proxy`, `health`, `circuit`, `metrics`,
  `balancer`, `backend`, and `config`, is imported only by `main` and tests,
  and keeps the graph acyclic. `AGENTS.md`'s dependency graph and directory map
  are updated in this ticket.
- Behaviour-preserving refactor: no observable change.

### S4.T1 — Config diffing (D5, D6, D7)

- **Backend identity** is the (name, URL) pair. A name kept with a new URL is
  one removed plus one added backend. Duplicate names within a file are already
  rejected by validation, so the diff may assume uniqueness.
- A pure diff function over (old, new) configs returns added, removed, and
  unchanged backend configs by identity: added and unchanged in the new file's
  order, removed in the old file's order.
- A second pure function returns the names of the non-backend fields that
  differ (listen, algorithm, health, circuit, metrics, health_endpoint),
  comparing resolved values so an omitted field and an explicit default compare
  equal after validation's defaulting.
- Only the backend list is reloadable; the policy of rejecting on any
  non-backend difference is T3's, which consumes this function.
- **ADR-0015 (reload architecture)**, written and committed in this ticket's
  design step before any code, records the decisions T1–T3 implement:
  in-process registry snapshot swap vs SO_REUSEPORT process handoff; (name,
  URL) identity; the backends-only reject rule; the single-load versioned
  snapshot; pull-based ring rebuild keyed on the version; the removed flag set
  before the swap; removed-backend observer suppression; the new-file-order
  rule; added backends starting unhealthy and admitted after one successful
  probe, and the asymmetry with startup; the permitted empty-selectable
  blue/green window with its WARN. `AGENTS.md`'s ADR table and key-decisions
  list gain its row.

### S4.T2 — Registry snapshot swap and loaded-config record (D8–D12)

- The registry holds one atomic pointer to an immutable snapshot of a
  monotonically increasing version and the ordered backend slice. All,
  Selectable, and Allow read the current snapshot; their contracts are
  unchanged. An exported version accessor serves tests and logs; the
  consistent-hash selector reads version and backend set from one load.
- A new **apply** operation takes the diff and builds the next snapshot by
  walking the new file's backends in order, reusing the existing instance for
  each unchanged identity and constructing a fresh one for each added identity.
  It returns the added and removed instances. It is called by one goroutine at
  a time; readers never lock.
- Before swapping, apply marks each removed backend as removed. `Backend` gains
  an unexported atomic flag with a read accessor, amending ADR-0002 decision 5
  in the method-only style of ADR-0006 / ADR-0010 / ADR-0012.
- Removed backends leave All and Selectable at the swap.
- The proxy's observer fan-out is skipped entirely when the request's backend
  is removed — covering normal completions, transport failures, and client
  cancellations on removed backends (and, from T4, drain cancellations). The
  proxy's active-connection accounting (increment/decrement and the gauge) is
  not observer-driven and is deliberately unaffected. `proxy.New(reg, sel)` is
  untouched.
- The consistent-hash bounded-loads selector caches (version, ring) behind an
  atomic pointer and, on a version mismatch in Select, builds a ring from that
  snapshot's full backend list and CASes it in. Redundant concurrent builds
  right after a swap are correct. Load remains live active connections on the
  backend (ADR-0009), so rebuilding the placement-only ring (ADR-0008) loses
  nothing. The unwired naive comparator stays static.
- The application holds an atomic pointer to the loaded config, set by build,
  with a read accessor. In T2 only tests read it; T3's reload replaces it.
- Round-robin's modulo index shifting with slice length is accepted, as on
  health flips (S1.T4).
- The sprint-1 contracts doc's concurrency-ownership rows for the registry
  backend set and the loaded config are updated from "Sprint 4: atomic.Pointer
  swap" to the as-built description.

### S4.T3 — SIGHUP orchestration (D13–D17)

- The application gains a **reload** operation taking a context and an
  already-parsed, validated config. It rejects the reload if the non-backend
  comparison is non-empty; otherwise it diffs against the loaded-config record,
  applies to the registry, replaces the record, hooks up subsystems, and logs.
- `main` installs SIGHUP notification on a channel of buffer 1 and runs a
  reload loop taking a context, the config path, the signal channel, the
  application, and a logger. For each signal: parse (strict unknown-field
  decoding) → validate → call reload. One goroutine; reloads never overlap;
  signal bursts collapse.
- Subsystem hook-up inside reload:
  - **Active checker** gains add and remove. Each prober has its own cancel
    function in a mutex-guarded map off the request path. Remove cancels it; a
    prober checks its own context after a probe returns and before applying any
    outcome, so a probe in flight at removal never marks, logs, or writes a
    gauge. Added probers probe immediately rather than after one interval.
  - **Added backends start unhealthy** and are admitted by one successful
    probe (recovery after ejection still needs two). Admission is the existing
    health-reinstated event with the new `initial_probe` reason; a failing
    first probe emits nothing. The probe-round-complete latch is never cleared
    by reload (ADR-0014 decision 4). Startup behaviour — backends start healthy
    — is unchanged.
  - **Outlier detector** gains forget, deleting a removed backend's window.
  - **Metrics:** added backends get series seeded (active connections 0,
    healthy 0, circuit closed). Removed backends get their healthy and
    circuit-state series deleted. Their active-connections series is **not**
    deleted (see the T3 → T4 hand-off). The collector gains the deletion
    operations for the two series it deletes.
- Logging through the frozen vocabulary, new constants added here: a **config
  reloaded** event with added / removed / unchanged counts, at WARN when
  unchanged is zero and INFO otherwise (honouring and extending the frozen
  "config reloaded" line); a **config reload failed** event with the reason
  (parse error, validation error, or the list of changed non-backend fields);
  the `initial_probe` reason. The health-reinstated event's documentation is
  widened to cover first admission.

### S4.T4 — Draining (D18–D22)

- Config gains a top-level `reload` block with `drain_window`: a pointer
  duration, default 30s when omitted, rejected when zero or negative, and not
  reloadable — T1's non-backend comparison is extended to include it.
  `configs/example.yaml` documents it.
- `Backend` gains an unexported **retired** context, created at construction
  and never cancelled unless the backend is removed and its window expires,
  with method-only access (amending ADR-0002 decision 5 again).
- The proxy derives each outbound request's context from the client request
  context with cancel-with-cause, and registers an after-func on the chosen
  backend's retired context that cancels with a drain-window-expired cause; the
  registration is stopped when the request completes. No goroutine per request
  and no mutex-guarded cancel registry.
- The error handler still releases the active-connection slot and responds 502
  for a drain-cancelled request; T2's removed flag already keeps it out of the
  observers. The cause only sets the WARN line's reason to `window_expired`.
- If the window expires after response headers were sent, the client receives
  a truncated body; the success recorded when headers arrived stands.
- Reload starts one drain goroutine per removed backend. It waits for whichever
  comes first: active connections at zero (polled on a ~100ms ticker) or the
  drain window timer. On expiry it cancels the retired context and waits for
  active connections to reach zero. It then deletes the backend's
  active-connections series only if no current backend shares the name, and
  logs a **backend drained** event with reason `idle` or `window_expired` and
  the number of requests cancelled. Drain constants are added to the log
  vocabulary here.
- Drain goroutines also select on the process shutdown context and exit
  immediately without cleanup; the existing server shutdown grace period covers
  in-flight requests. No deferred work may block shutdown.
- A drain is independent of later reloads: re-adding the same identity creates
  a fresh backend (T2) and neither revives nor ends the draining one.
- **ADR-0016 (drain lifecycle)**, written in this ticket's design step before
  code, records: the retired context and after-func join (and why not a
  per-request goroutine or a cancel-func registry); the two-phase polling
  drain; cancel-at-window with 502; truncated body after headers; abandonment
  on shutdown; independence from later reloads. `AGENTS.md` gains its row.

## Testing Decisions

A good test exercises external behaviour through the highest seam that can
observe it: HTTP outcomes, registry views, gauge values through the collector's
registry, and structured log lines through the existing capture handler. Tests
do not reach into snapshot internals, ring internals, or prober maps.
Concurrency tests run under `-race` with `require.Eventually` deadlines sized
for race-detector overhead (the S3.T8/T9 convention). Blocking is deterministic
— backends gated on channels, never sleeps. Each ticket's tests are
Red-first and cover only that ticket's tagged stories.

### Seams

Highest first; the first two are new and were agreed in the grilling, the rest
exist:

1. **The application seam** (built in T0, reload added in T3): build (and
   reload with a parsed config), observed through the client handler, the
   collector, the registry, and captured logs. The chaos harness and the
   exit-criterion tests use it.
2. **`main`'s reload loop** (T3): driven by a fake signal channel, a temp
   config file, and a captured logger. Covers only what `main` owns.
3. **Package-level seams:** the pure diff and non-backend-change functions;
   registry apply and views; checker add/remove; outlier forget; proxy
   observer suppression; consistent-hash selection across a version change.

### Per ticket

- **D0:** none — docs-only.
- **T0:** the full existing suite stays green; the chaos tests pass with
  unchanged assertions after switching their assembly to build. A build-level
  test confirms the handler routes to configured backends and every backend's
  seeded series exists. Prior art: `test/chaos` harness, `cmd/l7LoadBalancer`
  main tests.
- **T1:** table-driven diff tests — identical configs; pure add; pure remove;
  URL change under one name (one removed plus one added); reorder only (all
  unchanged, new order); mixed. Non-backend-change tests per field, including
  omitted-versus-explicit-default equality. Prior art: config validate/load
  table tests.
- **T2:** apply keeps unchanged instances (pointer identity) with their state;
  added instances are fresh; removed instances are marked and absent from All
  and Selectable; the snapshot follows the new order; the version increases. A
  proxy test proves a request selected before removal and completing after it
  reaches no observer while its active-connection slot is still released. A
  consistent-hash test proves the ring reflects the new set after a version
  change and that concurrent selects across a swap are race-free. A
  loaded-config accessor test. Prior art: registry tests, proxy observer tests,
  ring and consistent-hash tests.
- **T3:** checker add/remove, including a race test that removes a backend
  while its probe is blocked mid-flight and asserts no transition log and no
  gauge write; an added backend is not selectable until exactly one probe
  succeeds, then is, with exactly one health-reinstated line with reason
  `initial_probe` (not `probe_recovered`) and its healthy gauge 0 → 1; a
  failing first probe logs nothing. Removed backends' healthy and circuit series
  are gone and their active-connections series remains. Reload-loop tests with
  a fake signal channel: success, parse failure, validation failure, non-backend
  change rejected naming the field, and a signal burst collapsing into one
  reload. An application-level reload test with unchanged and added backends
  and requests in flight on the unchanged ones, all succeeding. Prior art:
  checker tests, transition-log tests, chaos eviction tests.
- **T4 (exit criterion)**, in the chaos harness, under `-race`:
  - **Case 1:** 1000 concurrent requests held open by gated backends — some on
    backends that stay unchanged, some on a backend being removed; reload adds
    one and removes one with a drain window longer than the hold; release the
    gates; all 1000 return 200. After: the removed backend is absent from All,
    active connections are zero everywhere, one drained line with reason
    `idle`, no series left for the removed backend, and unchanged backends are
    the same instances with circuit and EWMA state intact.
  - **Case 2:** drain window shorter than the hold, with the removed name
    re-added under a new URL; the removed backend's in-flight requests get 502
    with reason `window_expired`; one drained line with reason `window_expired`
    and the cancelled count; the fresh same-name backend's circuit, outlier, and
    gauge state reflect only its own traffic.
  - Plus: drain-window config load/validate cases and its inclusion in the
    non-backend comparison; a shutdown-during-drain test proving the drain
    goroutine exits without blocking; a benchmark of the proxy request path
    with the retired-context join, recorded in the session log. Prior art:
    chaos circuit tests, the proxy 100-concurrent active-connection leak test,
    config tests.

## Out of Scope

Recorded as proposed tickets by D0, not built in any ticket here:

- A reload-outcome counter metric (`lb_config_reloads_total{result}`).
- Fixing the pre-existing misclassification where a client cancellation is
  reported to observers as a backend failure (same cancellation-cause
  mechanism; belongs to Sprint 4's connection-lifecycle deliverable). On
  removed backends T2's suppression already hides it; on live backends it is
  unchanged.
- Hot-reload of the algorithm, health timing, circuit cooldown, listen
  addresses, or the drain window. Changing them is rejected, not ignored.
- Pre-warming added backends (probing before the swap, so a blue/green reload
  has no empty-selectable window).

Also out of scope, owned elsewhere:

- SO_REUSEPORT / process-replacement reload (post-Sprint 5 extension; ADR-0015
  records why in-process swap was chosen).
- The Half-Open scan-promotion log gap, the deployment-target ADR, the
  retry-policy ADR, connection-pool tuning, slow-loris timeouts, and the
  goroutine-leak audit — other Sprint 4 tickets.
- `docs/architecture.md` updates for reload — the Sprint 4 retro ticket, per
  the Sprint 1–3 pattern.
- Testing that the OS delivers SIGHUP to a notification channel (the one-line
  signal registration in `main` stays untested).

## Further Notes

- Frozen contracts touched, each in the tagged ticket: the `Registry` API
  (All, Selectable, Allow, SetCircuitGate) keeps its shape and gains apply and
  a version accessor [T2]; `proxy.New(reg, sel)` is untouched; `Backend` gains
  two unexported fields — the removed flag [T2] and the retired context [T4] —
  each amending ADR-0002 decision 5 through its ADR; the log vocabulary gains
  reload events and `initial_probe` [T3] and drain events and reasons [T4];
  the metrics collector gains deletion of the healthy and circuit-state series
  [T3] and of the active-connections series [T4].
- The frozen "config reloaded" line in the sprint-1 contracts doc is honoured
  and extended with the three counts [T3].
- ADR-0014 decision 4 already requires that a reload path not un-latch
  `/startupz`; reload never touches the probe-round latch [T3].
- Ticket files, one per ticket under this directory's `issues/`, strictly
  serial, each `Blocked by:` the one before: `01` S4.D0, `02` S4.T0, `03`
  S4.T1, `04` S4.T2, `05` S4.T3.0, `06` S4.T3, `07` S4.T4.0, `08` S4.T4. The
  `.0` tickets were split out at ticket-breakdown time (S3.T12.0 precedent) so
  each fits one context: S4.T3.0 carries T3's additive package APIs (checker
  add/remove with initial-probe admission, outlier forget, healthy/circuit
  series deletion, the `initial_probe` reason); S4.T4.0 carries ADR-0016, the
  `reload.drain_window` config, the retired context, and the proxy's
  drain-cancel join with the `window_expired` reason and benchmark. The story
  tags above name the parent ticket (T3 / T4); the ticket files say which half
  delivers each.
