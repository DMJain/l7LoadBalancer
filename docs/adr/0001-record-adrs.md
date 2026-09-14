# ADR-0001: Record architecture decisions

- **Status**: Accepted
- **Date**: 2026-08-31
- **Deciders**: project owner + bootstrap agent

## Context

The project rotates between multiple coding agents. Without a record of *why* decisions were made, later agents will re-litigate settled questions and diverge from the design.

## Decision

Every non-trivial architectural or design decision gets an ADR in `docs/adr/`, numbered sequentially, following the template in `0000-template.md`.

## Consequences

- Positive: preserved context across sessions and agents; traceable design record.
- Negative: small friction on decision points.

## Alternatives considered

- Inline comments only: rejected — not discoverable, not narrated.
- Git commit messages only: rejected — commits describe *what*, ADRs describe *why*.
