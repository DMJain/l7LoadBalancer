# ADR-0015: Zero-downtime reload architecture — in-process snapshot swap, backend identity, and admission

- **Status**: Accepted
- **Date**: 2026-09-25
- **Deciders**: Darshan Jain (project owner) + opencode agent (S4.T1, design session recorded in `.scratch/s4-t0-t4-reload/spec.md`)

## Context

Sprint 4's first exit criterion is "SIGHUP with 1000 in-flight requests drops
zero." An operator edits the backend list, sends SIGHUP, and routing must move
to the new fleet: unchanged backends keep everything the load balancer learned
about them, added backends take traffic only once they are proven, and removed
backends finish what they were doing. The backend set is fixed in five places
at startup — the registry's slice, the health checker's one-goroutine-per-
backend fan-out, the consistent-hash ring built once at selector construction,
the outlier detector's per-backend windows, and the per-backend metric series.

S4.T0 moved the whole wiring graph into `internal/app` behind `Build`/`Run`, so
a reload has one package to attach to and the exit-criterion test can drive the
real system without signals or temp files. S4.T1 builds the pure comparison a
reload is decided on; S4.T2 makes the registry's backend set replaceable while
traffic flows; S4.T3 orchestrates the whole thing from SIGHUP. The decisions
below span T1–T3 and have to be fixed before any of that code is written,
because each is a package-boundary, identity, or concurrency decision that is
expensive to reverse.

This ADR is written from the design-session record in the bundle spec
(decisions D5–D17). It does not cover draining (ADR-0016, S4.T4.0) or the
non-backend hot-reload, reload-outcome metric, client-cancellation
classification, and pre-warming follow-ups (spec Out of Scope).

## Decision

### Why in-process, not a process handoff

1. **Reload swaps the registry's backend set in process; it does not start a
   second process and hand off the listening socket (SO_REUSEPORT).** The
   listener is never closed, so there is no accept-boundary drop and no
   connection migration to design. Unchanged backends keep their instance and
   all runtime state — health, circuit, active connections, EWMA latency —
   which is the operator-visible property the reload exists for and which a
   fresh process cannot preserve without shipping state across a process
   boundary. SIGHUP is a reload signal, not a restart signal: `main` installs
   `SIGHUP` separately from the existing SIGINT/SIGTERM shutdown context, so a
   reload never races shutdown.

   SO_REUSEPORT/process replacement is deferred (spec Out of Scope, post-Sprint
   5): it is the right tool for a binary deployment or a config change larger
   than the backend list, but it is more moving parts than this change needs,
   and the throughput question it answers is not the one Sprint 4 asks.

### Backend identity and the diff

2. **Backend identity is the `(name, URL)` pair.** A name kept with a changed
   URL is one **removed** plus one **added** backend, never an "updated" one:
   state learned about the old host (circuit, EWMA, health) must never be
   applied to a different host. Duplicate names within a file are already
   rejected by `config.Validate`, so the diff assumes uniqueness and needs no
   disambiguation rule. Order is part of neither identity nor equality: two
   configs listing the same identities in a different order diff to all-
   unchanged.

3. **The diff is a pure function over `(old, new)` validated configs**,
   returning added, removed, and unchanged `[]BackendConfig`: added and
   unchanged in the **new file's order**, removed in the **old file's order**.
   The new-file order is what `Registry.All`/`Selectable` then report, so the
   file dictates round-robin rotation order and `LeastConnections`' deterministic
   tie-break. A second pure function returns the names of the non-backend
   fields that differ, comparing **resolved** values so an omitted field and an
   explicit default compare equal after `Validate`'s defaulting. Neither
   function logs, errors, or touches runtime state — S4.T2's apply and S4.T3's
   reload operation consume them.

4. **Only the backend list is reloadable; any non-backend difference rejects
   the reload whole.** This covers `listen`, `algorithm`, `health`, `circuit`,
   `metrics`, and `health_endpoint` (and, from ADR-0016, `reload`). A reload
   that would silently do nothing must be surfaced, not ignored, and a partial
   apply of a knob whose runtime consumers hold derived state (a probe interval
   already baked into a running ticker, a breaker's cooldown) is more dangerous
   than refusing it. The old config keeps serving and the log names the changed
   fields. Hot-reloading these is a proposed ticket, not built here.

### The snapshot

5. **The registry holds one atomic pointer to an immutable snapshot: a
   monotonically increasing version plus the ordered backend slice.** `All`,
   `Selectable`, and `Allow` read the current snapshot; their contracts are
   unchanged and readers never lock. An exported version accessor serves tests
   and logs, and the version and backend set are read in a **single atomic
   load**, so correctness comes from the structure rather than a rule about
   read order. The consistent-hash selector reads version and backends from
   that same one load.

6. **`apply` is the single writer that builds the next snapshot.** It walks the
   new file's backends in order, reusing the existing instance for each
   unchanged identity and constructing a fresh one for each added identity; it
   returns the added and removed instances. Re-adding a previously removed
   identity therefore yields a fresh instance that shares no state with the old
   one. `apply` is called by one goroutine at a time (the reload loop); readers
   are unaffected while it builds.

7. **Removed backends are marked removed before the swap.** `Backend` gains an
   unexported atomic removed flag with a read accessor, method-only in the
   style of ADR-0006/ADR-0010/ADR-0012, amending ADR-0002 decision 5. Marking
   first (rather than at or after the swap) means any request selected just
   before the swap already sees the flag when it completes, so the suppression
   rule below needs no read-order caveat. Removed backends leave `All` and
   `Selectable` at the swap. A request already holding the old pointer is
   otherwise unaffected: it completes against its backend.

8. **A removed backend reports nothing to observers.** The proxy skips the
   entire observer fan-out when the request's backend is removed — normal
   completions, transport failures, and client cancellations alike. This is the
   single rule that keeps every observer ignorant of reload: no circuit, health,
   outlier, or metric write can come from a backend that is no longer part of
   the fleet, so a same-name fresh backend's series reflect only its own
   traffic and no ghost transition is emitted. The proxy's active-connection
   accounting is **not** observer-driven and is deliberately unaffected — a
   removed backend must still release its slot; otherwise `ActiveConns` would
   never drain.

### Selection

9. **The consistent-hash ring rebuilds on demand, keyed on the snapshot
   version.** `ConsistentHashBoundedLoads` caches `(version, ring)` behind an
   atomic pointer and, when `Select` sees a version mismatch, builds a ring from
   that snapshot's full backend list and CASes it in; redundant concurrent
   builds right after a swap are correct and simply lose the CAS. Pull-based
   rebuild is chosen over push-based invalidation because the selector already
   reads the snapshot on every request, so the version check is free, and no
   reload path has to know that a ring exists. Load remains live
   `ActiveConns()` on the backend (ADR-0009), and the ring is placement-only
   (ADR-0008), so rebuilding it loses nothing. The unwired naive comparator
   stays static — it is test evidence, not a production selector. Round-robin's
   modulo index shifting over a changed slice length is accepted, exactly as on
   health flips (S1.T4).

### Admission and the blue/green window

10. **A backend added by reload starts unhealthy and is admitted after one
    successful probe; backends at process startup start healthy.** This is a
    deliberate asymmetry. At startup there is no previously-serving fleet to
    protect and no "old config" to fall back to: the process has not answered a
    request yet, every backend is unproven, and it must come up serving, so
    startup trusts the operator's file and lets the active checker correct it.
    A reload happens in place while traffic is already being served; a typo'd
    URL in an added backend must not produce client-visible 502s against
    traffic the old fleet was serving correctly. The added backend's first
    successful probe is a **reinstatement with reason `initial_probe`**,
    distinct from ejection recovery (`probe_recovered`); a failing first probe
    emits no transition at all, because the backend was never healthy. Recovery
    after ejection still requires the normal two consecutive successes. The
    probe-round-complete latch is never cleared by reload (ADR-0014 decision 4),
    so `/startupz` stays latched.

11. **A reload that replaces every backend (blue/green) is permitted, and its
    brief empty-selectable window is logged at WARN.** With every added backend
    starting unhealthy, `Selectable()` is empty until the first probe
    succeeds, so `/readyz` reports not-ready and client requests get 503 for
    that window. This is the honest, observable cost of decision 10 and is
    accepted: it is logged as a **config reloaded** line at WARN because
    `unchanged=0`, `/readyz` reflects the live empty set so an upstream can fail
    over, and pre-warming added backends (probing before the swap) is a proposed
    ticket, not built here.

12. **The loaded config is held on the application as one atomic pointer and
    replaced by the reload operation after the registry apply succeeds**, so
    each reload diffs against the currently loaded config rather than the
    startup config and successive reloads compose. `Build` seeds it; T2 only
    tests read it, T3's reload replaces it.

## Consequences

- Positive: reload preserves every piece of learned state for unchanged
  backends, which is the property an operator notices, and does so without a
  second process, fd passing, or an accept-boundary drop.
- Positive: purity of the diff (decisions 2–4) makes the whole comparison
  decision exhaustively table-testable with no registry, server, or clock.
- Positive: one structural rule (one snapshot behind one atomic pointer, one
  pre-swap mark) gives removed-backend suppression without any observer knowing
  reload exists.
- Negative: `Backend`'s exported surface grows again with the removed-flag
  accessor, and the flag is another method-only convention rather than a
  type-enforced one (same limitation ADR-0006 and ADR-0011 accepted).
- Negative: a blue/green reload has a real empty-selectable window (decision
  11) whose duration is the time to the first successful probe.
- Negative: round-robin's rotation index is not reset across a reload, so the
  first post-reload rotation can start mid-cycle; accepted as on health flips.
- Neutral: non-backend changes are rejected rather than hot-reloaded; the
  follow-up ticket can add them without changing this ADR's snapshot mechanism.

## Alternatives considered

- **SO_REUSEPORT / start-a-new-process handoff**: rejected in decision 1 —
  cannot preserve per-backend learned state, requires socket handoff or kernel
  load balancing, and answers a throughput question Sprint 4 does not ask;
  deferred to the post-Sprint 5 extension.
- **Diff by name only ("name is identity, URL is a mutable attribute")**:
  rejected in decision 2 — would carry state learned on the old host onto a
  different host, the exact failure the reload must not introduce.
- **Emit an explicit "updated" entry for a name whose URL changed**: rejected in
  decision 2 — a separate kind of change is a second code path (and a second
  admission/reset rule) for a case expressible as one removal plus one
  addition.
- **Order-insensitive diff that sorts backends**: rejected in decision 3 — the
  file order is load-bearing (rotation and tie-break), so the diff must report
  the new file's order, not a canonical one.
- **Reject a reload that removes every backend / leaves zero selectable**:
  rejected in decision 11 — blue/green migration is a legitimate operator move;
  the brief window is logged and observable rather than forbidden.
- **Added backends start healthy, matching startup**: rejected in decision 10 —
  it trades a short empty window for client-visible 502s from a typo, the
  failure the reload path is specifically built to avoid.
- **Push-based ring invalidation from the reload path**: rejected in decision 9
  — requires reload to know the selector's internals; the pull version check on
  a path that already reads the snapshot is free.
- **A `map[*Backend]state` in the reload path to carry state across**: rejected
  in decision 6 — state already lives on the instance, so keeping the instance
  for an unchanged identity is the whole mechanism.
