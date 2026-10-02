# 03: S5.T14.2 — `docs/design-decisions.md`, topics 1–4

**What to build:** the first half of the design-decisions document: four topics a fresh CS graduate can follow and a senior reviewer can check. A reader finishes it understanding what the project chose, what the alternatives were, and what each choice costs.

Spec: `../spec.md` — *Writing rules*, *Design-decisions document: seven topics*, *Approved headline claims*.

**Blocked by:** 02 (S5.T14.1, the approved dossier).

**Status:** ready-for-agent

## In scope / out of scope (AGENTS.md Step 2.5)

- **In scope:** creating the document with a short opening and topics 1–4 only.
- **Out of scope:** topics 5–7 and the closing paragraph (next ticket); any README or retrospective text; edits to ADRs or `RESULTS.md`; new measurements; any claim not approved in the spec or the dossier. If a topic exposes a missing ADR, record a proposed ticket and stop.

## Acceptance criteria

- [ ] The document opens with one short paragraph on what it is and how it is organised. It does not announce its audience.
- [ ] **Topic 1 — `net/http/httputil` over a framework or a hand-built proxy:** problem, options, choice, cost; links ADR-0005, ADR-0007 and the index entries for the reverse-proxy and h2c choices; names the three third-party dependencies exactly (the HTTP/2 package, the Prometheus client, the YAML library) and does not call the request path standard-library-only.
- [ ] **Topic 2 — bounded-loads consistent hashing:** problem, options, choice, cost; links ADR-0008 and ADR-0009; one worked example using the capacity formula from ADR-0009 with real numbers taken from the approved dossier; evidence from C5 and any approved N1 figure.
- [ ] **Topic 3 — P2C with EWMA latency:** problem, options, choice, cost; links ADR-0010 and ADR-0023; one worked example with one slow backend using real numbers from the dossier; **states directly that the estimate has no decay, how a backend can be starved after its latency recovers, and that the state is stale after a load-balancer switch**; uses C7 to explain why the project has no live divergence evidence.
- [ ] **Topic 4 — reload architecture:** problem, options, choice, cost; links ADR-0015, ADR-0016 and ADR-0019; one worked drain example with a named window; evidence from C3 including the raw figures (the p99 values and the frozen arrival count), which appear here and not in the README.
- [ ] Each topic follows the order problem in plain words → options → choice → what it costs, with the why before the how.
- [ ] Every number has a unit and a plain-words meaning, and traces to an approved claim (C1–C7) or an N-claim the owner approved in the dossier. No number is typed from memory.
- [ ] No degraded-scenario numbers; the degraded scenario, where mentioned, is described as unreliable with its cause marked UNVERIFIED.
- [ ] Writing rules hold: each term the topics use is defined in one plain sentence at first use in this document (Layer 7, reverse proxy, percentiles, req/s, EWMA, P2C, consistent hashing, bounded loads, hot key, drain, SIGHUP and reload, h2c, cold start, and any others used); no ticket IDs; no unexplained "slice", "cell", "core slice" or "bisect"; no marketing words; neutral voice; under about 20 words per sentence on average.
- [ ] The reader check for these four topics is done: every term that needed a definition and where it is defined is listed in the session log. A term used before its definition fails.
- [ ] Docs-only (TDD-exempt). The claim commit and the work commit are each shown to the owner for approval first.
