# Cross-Tool Skill Availability

This repo's engineering skills (`triage`, `to-tickets`, `to-spec`, `implement`, `tdd`, `code-review`, `domain-modeling`, `grilling`, `setup-matt-pocock-skills`, and the rest of the `mattpocock/skills` set) are installed in two places:

- `~/.agents/skills/` — home-level, managed by the `mattpocock-skills` Claude Code plugin. Read by Claude Code always, and by OpenCode CLI via its default external-skill scan of `~/.claude/` and `~/.agents/`.
- `.agents/skills/` (this repo, gitignored) — project-level real copies, not symlinks. Required for Antigravity IDE, which only discovers skills at a project-root `.agents/skills/` path and never reads the home-level location.

## Keeping the project-level copies in sync

The project-level copies are **snapshots**, not live links. When the `mattpocock-skills` plugin updates a skill, or `~/.agents/skills/<name>/SKILL.md` is edited directly, the copy under this repo's `.agents/skills/<name>/` does not update automatically. Re-run `setup-matt-pocock-skills`, or manually re-copy the changed skill directory, to refresh it.

## If a skill doesn't show up as a registered command

Before relying on a skill in a given tool, confirm that tool actually registered it:

- **Claude Code**: always works — it's the source of truth these skills were authored for.
- **OpenCode**: run `opencode debug skill` and confirm the skill's `name` appears in the list.
- **Antigravity**: no CLI equivalent is known; check its skill UI/autocomplete directly.

**If a skill isn't registered, typing its slash form (e.g. `/triage`) does not load its actual instructions.** The agent sees the literal text as a plain prompt and improvises a best-effort response from general knowledge plus whatever `AGENTS.md` says — no rubric, no forced steps, none of the real `SKILL.md` process. This can look like it worked while actually being a materially weaker, ad hoc substitute. When in doubt, open `.agents/skills/<name>/SKILL.md` directly and follow it manually.
