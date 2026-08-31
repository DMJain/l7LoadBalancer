# Session logs

Each coding session appends a file here named `YYYY-MM-DD-<agent>.md` (e.g. `2026-09-05-claude-code.md`; if multiple sessions per day per agent, add `-2`, `-3`).

## Template

```
# Session: <YYYY-MM-DD> — <agent-name>

## Goal
What sprint task(s) were targeted.

## Done
- Task IDs completed (reference `PROGRESS.md`).
- Commit SHAs.

## Decisions
Any design decisions made this session. Cross-reference the ADR if written.

## Open items / handoff notes
Anything left unfinished, uncertain, or worth flagging for the next agent.
```
