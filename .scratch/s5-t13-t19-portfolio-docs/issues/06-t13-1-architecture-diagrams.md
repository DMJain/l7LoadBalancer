# 06: S5.T13.1 — Architecture diagrams

**What to build:** two diagrams in the architecture document that a reader can trust: how one request travels through the load balancer, and which packages depend on which, derived from the real code.

Spec: `../spec.md` — *Architecture document*, *Writing rules*.

**Blocked by:** 01 (S5.D2). Independent of the design-decisions and retrospective tickets, so it can run in parallel with them.

**Status:** ready-for-agent

## In scope / out of scope (AGENTS.md Step 2.5)

- **In scope:** the architecture document's two Mermaid diagrams and the short text around them.
- **Out of scope:** the README (next ticket reuses the request-path diagram); changing package boundaries or any code; editing the agent guidance (the version and dependency wording are fixed in the hygiene ticket); images that go stale.

## Acceptance criteria

- [ ] A request-path Mermaid diagram showing a client request through the selector, the reverse proxy, the backend and the response path back, including where an active-connection count is released, with a caption sentence per step.
- [ ] A package-dependency Mermaid diagram of the entry point and each internal package, **derived from the module's real import graph** (`go list`), not copied from the agent guidance.
- [ ] The derivation is recorded in the session log (the command used and the edges it produced), and any difference from the package graph in the agent guidance is reported, including the two known text differences (the stated Go version, and the request path described as standard-library with one exception while the module also depends on the Prometheus client and the YAML library).
- [ ] The text names the dependency rule "backend does not import balancer" and says why in plain words.
- [ ] Both diagrams render (parser check or hosted preview, recorded in the session log).
- [ ] Writing rules hold in the new text: each term defined at first use in this document (Layer 7, reverse proxy, selector, and any others used); no ticket IDs; no marketing words; neutral voice; under about 20 words per sentence on average.
- [ ] The reader check for this document's new text is done and logged: every term that needed a definition and where it is defined.
- [ ] Docs-only (TDD-exempt). The claim commit and the work commit are each shown to the owner for approval first.
