# ADR-0005: Scope of "production-grade"

**Status:** Accepted. Amended by ADR-0019, which settles the deployment target that this ADR left open.

## Context

The project calls itself a "production-grade" Layer 7 load balancer. The term means different things to a reviewer, a contributor and the owner. The term also controls what the project builds and what it leaves out. The meaning must be fixed in writing.

## Decision

"Production-grade" in this project means:

- The load balancer shows the patterns that a real L7 load balancer needs. These are pluggable selection algorithms behind a clean interface and correct concurrent shared state. Atomics, mutexes and channels are each chosen for a stated reason. The patterns also include active and passive health checks, a circuit breaker for each backend, zero-downtime reload, a correct connection lifecycle and reproducible benchmarks against Nginx.
- Each non-trivial design decision has a written reason in an ADR, in `AGENTS.md`, or in a code comment that points to one of them. The bar is "can explain and justify", not "tested at scale".
- Benchmark numbers come from a clean checkout and compare with Nginx under stated conditions. The project never claims a general "faster than Nginx" result.

It does **not** mean:

- Protection against hostile internet traffic: no WAF, no DDoS protection, no rate limiting.
- TLS certificate rotation or renewal.
- Kernel or OS tuning (sysctl, NIC offload, CPU pinning). The load balancer uses Go runtime defaults.
- SLO definitions or alerting. The project has metrics and a Grafana dashboard, but no SLOs on top.
- A formal security review.
- Multi-tenancy. One instance serves one configured backend set.
- Secrets management beyond environment-variable interpolation.
- Disaster recovery. There is no state to back up. The config is a file.
- Capacity planning, an SLA, or an on-call runbook.
- Proof under multi-region or real production traffic.

## Consequences

- The README and other external text can point to this ADR for the scope.
- A contributor can test a new feature against one question: does it deepen a demonstrated pattern, or does it claim operational readiness that the project does not support?
- This ADR blocks no technical work.
- If the purpose of the project changes (for example, it serves real traffic), write a new ADR instead of an edit to this one.
- An undefined term was not used, because a technical reviewer sees the gap between the word and the missing features.
- "Passes the exit criteria" was not used as the definition, because it does not capture the rule that each decision must be defensible.
