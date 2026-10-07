# ADR-0001: Record architecture decisions

**Status:** Accepted

## Context

Many people and tools change this code over time. Without a record of why the project made a decision, a later contributor can reopen a settled question or move away from the design.

## Decision

Each non-trivial architecture or design decision gets an ADR in `docs/adr/`. ADRs have sequential numbers and use the structure in `0000-template.md`.

## Consequences

- The reasons for the design stay with the code.
- Each decision point has a small extra cost.
- Inline comments alone were not used because they are hard to find.
- Commit messages alone were not used because they record what changed, not why.
