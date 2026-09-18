# ADR-0005: Scope of "production-grade"

- **Status**: Accepted
- **Date**: 2026-09-18
- **Deciders**: Darshan Jain (project owner)

## Context

AGENTS.md describes this project as "a production-grade Layer 7 HTTP load
balancer" without defining the term. Left unstated, it reads differently to
a hiring reviewer, a future contributor, and the project owner months from
now — and it shapes the README, the Sprint 5 design-decisions doc, and every
judgment call about what to build vs. defer to "Post-Sprint 5 — Optional
extensions." Better to fix the claim now, in writing, than let it calcify
through repetition across docs and get walked back later under scrutiny.

The scope boundary below was stated directly by the project owner during the
S1.T3–T10.5 design-review conversation on 2026-09-18. It was not previously
recorded in AGENTS.md, MILESTONES.md, or any prior ADR — this ADR is that
record.

## Decision

"Production-grade" in this project means:

- Demonstrates the patterns a real production L7 LB needs: pluggable
  selection algorithms behind a clean interface, concurrency-correct shared
  state (atomics/mutex/channels chosen deliberately, each justified),
  active + passive health checking, per-backend circuit breaking,
  zero-downtime reload, connection lifecycle correctness, and honest,
  reproducible benchmarking against Nginx.
- Every non-trivial design decision is defensible: documented in an ADR, in
  AGENTS.md, or in a comment pointing at one of those. The bar is "can
  explain and justify," not "battle-tested at scale."
- Benchmark numbers are reproducible from a clean checkout and compared to
  Nginx honestly under stated conditions — never claimed as a general
  "faster than Nginx" result.

It explicitly does **not** mean:

- Hardened against adversarial internet traffic — no WAF, no DDoS
  mitigation, no rate limiting (rate limiting is a Post-Sprint-5 *optional*
  extension per MILESTONES.md, not a commitment).
- A TLS certificate rotation/renewal story.
- Kernel- or OS-level tuning (sysctl, NIC offload, CPU pinning, etc.) — runs
  on stock Go runtime defaults throughout.
- SLO instrumentation or alerting — Sprint 3 ships metrics and a Grafana
  dashboard, but defining and alerting on service-level objectives is a
  separate, unclaimed layer on top of that.
- A formal security review — this is a demonstration/portfolio project, not
  one that has undergone adversarial security testing.
- Multi-tenancy — one load balancer instance serves one configured backend
  set; no tenant isolation, quotas, or per-tenant config.
- Secrets management beyond environment-variable interpolation — no
  Vault/KMS integration, no secret rotation.
- A disaster-recovery story — no cross-region failover, no backup/restore
  procedure for state (there is none to back up; config is a file).
- Capacity planning or an SLA — no sizing guidance, no uptime commitment, no
  on-call runbook.
- Validation under multi-region or real production traffic volume.
- A settled deployment target (bare binary vs. Docker vs. Kubernetes) —
  deliberately deferred to Sprint 4/5, where the reload and
  connection-lifecycle work will inform that choice rather than the other
  way around.

## Consequences

- Positive: README and any external-facing description can point at this
  ADR instead of re-litigating scope every time "production-grade" comes
  up.
- Positive: a contributor evaluating a new feature can check it against
  "does this deepen a demonstrated pattern, or does it imply operational
  readiness we don't back up" before adding it.
- Negative: none — this is a claims-scoping document, not a technical
  constraint. It blocks no planned Sprint 1–5 work.
- Neutral: revisit (supersede, don't silently edit) if the project's purpose
  ever changes — e.g., if it's actually deployed to serve real traffic.

## Alternatives considered

- **Leave "production-grade" undefined and let each doc/reader infer its own
  meaning**: rejected — exactly the kind of fuzzy language that erodes
  credibility with a technical reviewer who notices the gap between
  "production-grade" and "no rate limiting, no security review, no
  disaster-recovery story."
- **Define it narrowly as "passes Sprint 1–5 exit criteria"**: rejected —
  too mechanical; it doesn't capture the "defensible decisions" framing
  that AGENTS.md is actually going for.
