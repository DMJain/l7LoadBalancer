# Spec: S1.T10 — Sprint 1 Retro / Architecture Doc Update

Status: ready-for-agent

---

## Problem Statement

Sprint 1 (S1.T1–T9) is fully implemented and `[DONE]`, but `docs/architecture.md` is still the original three-section placeholder with no component map or request-path diagram — a future agent picking up Sprint 2 has no written architecture reference to onboard from. Two documentation gaps exist alongside this: (1) `docs/design/sprint-1-contracts.md`'s concurrency-ownership table has no cross-link to ADR-0006 or ADR-0007, even though both ADRs directly amend the fields that table describes (`Backend.healthy`, `Backend.active`); (2) `MILESTONES.md`'s Sprint 1 deliverable list still names a "custom `Transport` pattern" that was reassigned to Sprint 4 *before* S1.T6 was ever implemented (per the pre-implementation S1.T6 scope in `.scratch/s1-t3-t9-backend-selector-proxy/spec.md`), but `MILESTONES.md`'s original wording was never corrected to match, and no record of that clarification exists anywhere obvious. Separately, one Sprint 1 implementation decision — removing the scaffold's `-addr` CLI flag during S1.T7 — was explicitly flagged for owner sign-off during code review and never formally closed. Left alone, these are loose threads a Sprint 2+ agent will either trip over or silently re-decide.

## Solution

Execute S1.T10 per its frozen acceptance criteria (`PROGRESS.md:139-147`): rewrite `docs/architecture.md` with a request-path diagram, component map, decision index, a "Deviations from plan" section covering the two items above plus the resolved `-addr` sign-off, and a forward-pointer section linking ADR-0006/0007 to the frozen contract's concurrency table — without editing `sprint-1-contracts.md` itself, preserving its freeze guarantee. Append a Sprint 1 closing session-log entry that serves as the retro (no separate retro document — the frozen acceptance criteria don't call for one). Mark S1.T10 `[DONE]` in `PROGRESS.md`. Two additional candidate undocumented decisions (the `LeastConnections` tie-break rule, docker-compose chaos-knob defaults) were evaluated against the project's ADR bar during design review and neither qualifies — this task produces **zero new ADRs**, which is the correct outcome of the audit, not a shortfall.

## User Stories

1. As a future agent onboarding to Sprint 2, I want a written architecture doc describing the request path, so that I don't have to reverse-engineer it from source before I can safely make a change.
2. As a future agent, I want a component map in `architecture.md`, so that I understand package boundaries without re-deriving `AGENTS.md`'s dependency graph from scratch.
3. As a future agent reading `sprint-1-contracts.md`, I want to discover that ADR-0006/0007 amend its concurrency table, so that I don't build against a stale mental model of `Backend.healthy`/`Backend.active` semantics.
4. As the project owner, I want `sprint-1-contracts.md` to remain byte-identical to its frozen state, so that "frozen" keeps meaning something to future readers who diff against it.
5. As a future agent, I want `architecture.md` to forward-point explicitly to ADR-0006/0007, so that the amendment is discoverable from the doc a new agent reads first, not just from the ADR directory.
6. As the project owner, I want the `-addr` CLI flag deviation formally recorded as signed off, so that S1.T10 doesn't leave a dangling "flagged, never answered" note for a later sprint to stumble on.
7. As a future agent doing Sprint 4's connection-pool-tuning work, I want the "custom Transport pattern" wording ambiguity resolved and documented, so that I don't waste time investigating whether a custom `RoundTripper` was silently dropped from Sprint 1.
8. As a code reviewer, I want the deviations section to state precisely why the `-addr` and Transport items are or aren't deviations, so that the retro reads as an audit trail with evidence, not a vague summary.
9. As a future agent, I want a Sprint 1 closing session-log entry, so that the per-session log history stays complete and chronologically traceable.
10. As the project owner, I want it confirmed that the deployment-target decision was never actually lost, so that a stale recollection doesn't get enshrined as false project history.
11. As a future agent implementing Sprint 2 selectors, I want `architecture.md` to reference the algorithm identifier table and `Selector` interface, so that the doc orients me to exactly where Sprint 2 work plugs in.
12. As the project owner, I want the diagram format to match the existing docs' ASCII convention unless the content genuinely doesn't fit it, so that a new diagramming tool isn't silently introduced as a side effect of a docs-only task.
13. As a future agent, I want it explicit that the `LeastConnections` tie-break rule and docker-compose chaos-knob defaults were considered for ADRs and rejected — with reasoning — so that I don't re-raise them as "missing ADR" gaps in a later audit.
14. As the project owner, I want the architecture rewrite, the session log, and the `PROGRESS.md` update to land in one atomic commit, so that Sprint 1 has an unambiguous close-out point.
15. As a future agent picking up Sprint 2, I want a "deliberately not here yet" boundary list in `architecture.md`, so that I don't assume unimplemented subsystems (health checking, circuit breaking, hot-reload) already exist.
16. As the project owner, I want the audit scope for this task to cover `MILESTONES.md`, all 7 ADRs, and `sprint-1-contracts.md`, so that the deviations review is comprehensive rather than spot-checked.
17. As a future agent, I want any newly-discovered non-trivial undocumented decision evaluated against a consistent three-part bar (hard to reverse / surprising without context / real trade-off) before an ADR gets drafted, so that ADR proliferation doesn't dilute the ones that actually matter.

## Implementation Decisions

### `docs/architecture.md` (full rewrite)

- **Overview**: retained and refined from the current placeholder text.
- **Request-path diagram**: ASCII text by default, matching the two existing ASCII diagrams already in `AGENTS.md` (request-path flow, package dependency graph). Switch to Mermaid only if the ASCII version reads as cramped at first draft (would be this repo's first Mermaid diagram — explicitly sanctioned by S1.T10's own acceptance text, "text or Mermaid diagram"); if the switch happens, note it as a new convention in the Deviations section.
- **Component map**: filled in per `AGENTS.md`'s package dependency graph (`proxy` → `balancer`/`backend` → `config`; `logger` as a leaf; `metrics`/`health`/`circuit` marked as future-sprint additions, not yet present).
- **Decision index**: table of the 7 accepted ADRs plus the already-tracked TBD rows for Sprint 2–4 (consistent hashing, P2C-EWMA, circuit breaker concurrency, reload architecture, deployment target).
- **New "Deviations from plan" section** — exactly two entries:
  1. **S1.T7 `-addr` flag removal**: recorded as owner-signed-off during the S1.T10 retro. Rationale: `cfg.Listen`, part of the frozen YAML schema and checked by `config.Validate`, is the single validated source of truth; a CLI override was considered but evaluated against the project's three-part ADR bar and rejected (trivially reversible — a contained, one-flag, no-ripple change — even though it's plausibly surprising and represents a real trade-off, reversibility alone fails the bar). No ADR. Pointer to `docs/sessions/2026-09-18-opencode.md` for the original S1.T7 reasoning.
  2. **`MILESTONES.md`'s "custom Transport pattern" wording**: documented as a pre-implementation scope clarification, not a mid-implementation deviation. `.scratch/s1-t3-t9-backend-selector-proxy/spec.md:115` (and `:171`) explicitly deferred `http.Transport` tuning to Sprint 4 *before* S1.T6 was coded, consistent with `AGENTS.md:291` and `MILESTONES.md:63`; "RoundTripper" appears nowhere in the project's source or git history prior to this task. **Correction (2026-09-19, during S1.T10 implementation):** this entry originally also cited `sprint-1-contracts.md:115`; that line is the error-sentinel paragraph and the frozen contracts doc never mentions Transport. See `docs/sessions/2026-09-19-claude.md` → Decisions. `MILESTONES.md`'s original bullet (written in the initial scaffold commit, never edited since) simply was never updated to match the already-settled scope.
- **New "Later amendments to Sprint 1 contracts" section**: forward-pointer only — ADR-0006 (amends the concurrency table's `Backend.healthy` row) and ADR-0007 (amends the `Backend.active` row / proxy lifecycle). `sprint-1-contracts.md` itself is not edited.
- **New "Deliberately not here yet" section**: tight bullet list, subsystem name + owning sprint, one line each — e.g. health checking / circuit breaking / metrics (Sprint 3); hot-reload, connection-lifecycle hardening, connection-pool tuning, retry policy (Sprint 4); HTTP/2, benchmarks (Sprint 5); deployment-target decision (Sprint 4; ADR-0005 frames the window as Sprint 4/5); consistent-hash-bounded-loads and P2C-EWMA selectors (Sprint 2).

### ADR audit outcome

- Three candidate undocumented decisions were evaluated against the project's three-part ADR bar (hard to reverse / surprising without context / real trade-off — all three must hold). None qualify:
  - **`LeastConnections` tie-break rule** (first-in-registry-order wins): already documented with rationale in `AGENTS.md`'s decisions index (#10); trivially reversible.
  - **docker-compose `SLEEP_MS`/`FAIL_RATE` defaults**: test-harness tuning knobs, trivially reversible, no real alternatives analysis at stake.
  - **S1.T7 `-addr` removal**: surprising and a real trade-off, but trivially reversible — fails the bar on that leg alone.
- Net result: this task produces **zero new ADRs**. This is the intended outcome of applying the bar rigorously, not an incomplete audit.

### `docs/sessions/2026-09-19-claude.md` (new file)

- Standard session-log template (`## Goal` / `## Done` / `## Decisions` / `## Open items`), matching the format already used by `docs/sessions/2026-09-18-opencode.md`.
- Serves as the Sprint 1 retro in full — no separate standalone retro document, per the frozen acceptance criteria and explicit design-review decision.
- Records that the deployment-target decision was checked and confirmed **not** lost or orphaned (tracked consistently in ADR-0005, `MILESTONES.md:66`, `AGENTS.md:321`, all pointing to Sprint 4) — logged here as a closed non-issue; no footnote added to `architecture.md` (explicitly declined, see Out of Scope).

### `PROGRESS.md`

- S1.T10 line changed from `[TODO]` to `[DONE]`.
- All of the above — `architecture.md` rewrite, new session log, `PROGRESS.md` update — land together in **one atomic commit**, per `AGENTS.md`'s task protocol Step 6 (docs-only tasks are exempt from Red-Green-Refactor but still follow Steps 0–2 and 6).

## Testing Decisions

### What makes a good test here

Not applicable in the conventional sense. `PROGRESS.md`'s frozen S1.T10 entry states "Test approach: none (docs-only)", and `AGENTS.md`'s TDD exception explicitly exempts docs-only/infra-only tasks from Red-Green-Refactor while still requiring the orient/claim/design steps and the atomic close-out commit.

### Seams

None — no Go logic is added or modified. "Correctness" here means factual verification: every claim in the rewritten `architecture.md` and its deviations section must be checked against the actual current state of the files it references, not asserted from memory or a prior session's summary (a stale-memory claim about `MILESTONES.md` and a "Fly.io" note was caught and corrected this way during design review — see Further Notes).

### Verification approach (substitutes for tests)

- Every deviations-section entry cites verifiable evidence (file:line) for its claim, matching the standard already applied during this design-review session (e.g., the `-addr` sign-off gap traced to specific session-log line ranges; the Transport wording traced through full `git log -p` history).
- Cross-links (ADR-0006/0007 pointers, the forward-pointer section) must resolve to files that actually exist at the stated paths.
- After the rewrite, read `docs/architecture.md` once end-to-end to confirm it doesn't silently reintroduce a claim already refuted during design review (e.g., no "Fly.io" note, no reopening of the `-addr` decision as unresolved).

### Prior art

This spec's Implementation Decisions section is itself the prior art — every claim in it was independently fact-checked via three rounds of dispatched research sub-agents during the design-review session that produced this spec, rather than drafted from assumption.

## Out of Scope

- A separate standalone retro narrative document beyond the session log + `architecture.md`'s deviations section — explicitly rejected during design review as scope creep beyond the frozen acceptance criteria. A deeper process retro (velocity, tooling friction, TDD overhead) is a legitimate future ask but must be named as its own task.
- Editing `docs/design/sprint-1-contracts.md` — content stays byte-identical to preserve its freeze guarantee; only `architecture.md` gets the forward-pointer.
- Re-litigating the `-addr` decision's technical merits — already decided during design review (kept removed); this task only formalizes the sign-off.
- Drafting an ADR for the freeze-policy question raised during design review (should a "frozen" doc ever be edited for citation-only fixes?) — explicitly deferred by the owner to a future task, not S1.T10.
- Any new Sprint 2+ implementation work (consistent-hash-bounded-loads, P2C-EWMA, health checking, circuit breaking, hot-reload, the deployment-target decision itself) — `architecture.md` documents these as "not here yet," it doesn't implement or further scope them.
- A reassurance footnote about the deployment-target decision in `architecture.md` — explicitly declined by the owner.

## Further Notes

### Correction of a stale prior-session recollection

A pre-session memory summary claimed `MILESTONES.md` defines S1.T10 and that a "locked-decisions block" notes "leaning toward Fly.io" for the deployment target. Both were checked against the actual repository during design review and found false: S1.T10 is defined in `PROGRESS.md` only (`MILESTONES.md` has no task-ID-level content at all), and "Fly.io" appeared nowhere in this repo's tracked history before this task (`git log --all -p` returned zero hits, case-insensitive, at design time; this spec is now itself tracked and mentions the term). The deployment-target decision is intact and correctly tracked — nothing needs fixing there.

### Design-review process note

This spec is the output of a full grilling + domain-modeling design-review session — three rounds of fact-finding sub-agents were dispatched mid-session specifically to avoid deciding documentation content from assumption or stale memory. An implementing agent should trust this spec's citations over re-deriving them, but should spot-check anything that looks stale by the time of implementation (git state moves).

### Why zero new ADRs is the correct outcome, not a shortcut

It would be easy to read "audit for undocumented decisions" as implying some minimum number of new ADRs are expected. That's not the case here: three real candidates were found and evaluated against the project's own three-part bar, and none qualified. Forcing an ADR onto a trivially-reversible, already-rationalized decision would violate the project's own stated principle (`domain-modeling` skill, and `AGENTS.md`'s "offer ADRs sparingly" spirit) that ADRs exist for genuinely hard trade-offs, not routine implementation choices.
