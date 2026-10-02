# 04: S5.T14.3 — `docs/design-decisions.md`, topics 5–7 and close-out

**What to build:** the second half of the design-decisions document and its sign-off, so the complete seven-topic document is ready for the README and retrospective to link.

Spec: `../spec.md` — *Writing rules*, *Design-decisions document: seven topics*, *Approved headline claims*.

**Blocked by:** 03 (S5.T14.2).

**Status:** ready-for-agent

## In scope / out of scope (AGENTS.md Step 2.5)

- **In scope:** topics 5–7, the closing paragraph, and the reader check and number trace for the whole document.
- **Out of scope:** rewriting topics 1–4 beyond fixes the whole-document reader check requires; any README or retrospective text; edits to ADRs or `RESULTS.md`; new measurements; unapproved claims.

## Acceptance criteria

- [ ] **Topic 5 — failure-mode interaction:** problem, options, choice, cost; links ADR-0011, ADR-0012, ADR-0013, ADR-0014, ADR-0017 and ADR-0018; defines the circuit breaker and its three states, and active versus passive health checks, at first use; evidence C4 with the revised wording (a graceful stop, 1750 req/s, 3 failed requests in a 9 ms span about 0.7 s after the stop, no retry per ADR-0018); the phrase "time to detection" does not appear; the ejection mechanism is cited only as the approved dossier allows.
- [ ] **Topic 6 — honest benchmark comparison with Nginx:** problem, options, choice, cost; links ADR-0020, ADR-0021 and ADR-0022 and the results document's limitations; evidence C1, C2, C6 and C7; defines matched, nearest-equivalent, peak and rig-limited at first use; states that no round-robin small-response comparison exists over HTTP/2 and that the HTTP/1.1 comparison is labelled as such; C6 is ordinal with the table of peaks and the search step per size, no ratio and no "noise" wording; the degraded scenario is described as unreliable, cause UNVERIFIED, no numbers.
- [ ] **Topic 7 — concurrency model:** problem, options, choice, cost; links the frozen concurrency ownership table and ADR-0007, ADR-0010, ADR-0012, ADR-0013, ADR-0015 and ADR-0016 plus the index entries for the round-robin counter and the least-connections scan; **every primitive named is cited to a file and line from the approved dossier**, and a primitive the dossier marks "not found" is not claimed; each primitive carries the reason it was chosen over the alternatives.
- [ ] Each topic follows problem in plain words → options → choice → cost, why before how.
- [ ] A closing paragraph, "decisions not covered here", points to the ADR index.
- [ ] Every number in the whole document has a unit and a plain-words meaning, and is listed in the session log with the claim that approves it. A number with no approving claim is removed or sent to the owner.
- [ ] **Reader check for the whole document** (all seven topics): every term that needed a definition and where it is defined, listed in the session log; no term is used before it is defined in this document.
- [ ] Writing rules hold across the whole document: no ticket IDs; no unexplained "slice", "cell", "core slice" or "bisect"; no marketing words; neutral voice; the audience is never announced.
- [ ] All ADR links in the document resolve.
- [ ] Docs-only (TDD-exempt). The claim commit and the work commit are each shown to the owner for approval first.
