# Cross-Tool Skill Availability

This repo's engineering skills (`triage`, `to-tickets`, `to-spec`, `implement`, `tdd`, `code-review`, `domain-modeling`, `grilling`, `setup-matt-pocock-skills`, and the rest of the `mattpocock/skills` set) are installed in two places:

- `~/.agents/skills/` — home-level, managed by the `mattpocock-skills` Claude Code plugin. Read by Claude Code always, and by OpenCode CLI via its default external-skill scan of `~/.claude/` and `~/.agents/`.
- `.agents/skills/` (this repo, gitignored) — project-level real copies, not symlinks. Required for Antigravity IDE, which only discovers skills at a project-root `.agents/skills/` path and never reads the home-level location.
- `.opencode/skills/` (this repo, gitignored) — symlink to `../.agents/skills`. Exists so OpenCode loads every skill exactly once from a single project root. See the next section.

## OpenCode: deterministic single-source loading

OpenCode scans six skill roots and dedupes by skill name, keeping the last match loaded:

- project: `.opencode/skills/`, `.claude/skills/`, `.agents/skills/`
- global: `~/.config/opencode/skills/`, `~/.claude/skills/`, `~/.agents/skills/`

With this repo's setup the same 28 names appear in `~/.claude/skills` (symlinks), `~/.agents/skills` (real store), and the project `.agents/skills` (snapshot). OpenCode loads the matches concurrently, so the winning copy is **nondeterministic** and it logs a `duplicate skill name` warning per collision.

There is no `opencode.json` field to turn the external scans off, `.env` is not read, and a project plugin cannot set the flag early enough. The only supported lever is the process env var `OPENCODE_DISABLE_EXTERNAL_SKILLS=1`. A scoped `opencode()` shell function (in `~/.zshrc`) sets it **only** when OpenCode runs inside a git repo that contains `.opencode/skills`, so other repos keep loading the home-level skills normally.

Result in this repo: `opencode debug skill` reports the built-in skill plus 28 project skills from `.agents/skills`, with zero duplicate warnings. Claude Code (reads `~/.claude/skills`) and Antigravity (reads `.agents/skills`) are unaffected.

## OpenCode: skills don't appear in the `/` menu — commands do

OpenCode has two separate mechanisms, and they are not interchangeable:

- **Skills** (`SKILL.md` under any of the paths above) are *model-invoked only*, via a built-in `skill` tool the agent calls itself (`skill({ name: "triage" })`) when it judges the description matches the task. Per [OpenCode's own docs](https://opencode.ai/docs/skills/), this is the only way skills load — there is no `/` slash-menu entry for them. This is a deliberate upstream design choice, not a misconfiguration in this repo; OpenCode has an open feature request, [#7846](https://github.com/anomalyco/opencode/issues/7846), asking for a way to list/invoke skills manually.
- **Commands** (`.md` files under [`.opencode/commands/`](https://opencode.ai/docs/commands/)) *do* populate the `/` menu. The filename becomes `/name`, and the file body is sent verbatim as the prompt (with `$ARGUMENTS`, `$1`, `!command`, `@file` substitutions).

Typing an unregistered skill name as a slash command (e.g. `/ask-matt` with no matching command file) does not invoke the skill — OpenCode falls back to a fuzzy file match against `.opencode/skills/ask-matt/SKILL.md` and dumps its raw content into the compose box, which looks like it worked but is not going through the real skill tool at all.

**Fix**: this repo ships one thin wrapper command per skill under `.opencode/commands/<name>.md`:

```markdown
---
description: "<copied from the skill's SKILL.md frontmatter>"
---
Call the skill tool with name "<name>", then follow its instructions. Arguments: $ARGUMENTS
```

This gives `/ask-matt`, `/triage`, etc. a real slash-menu entry (with description shown) that correctly routes through the skill tool instead of dumping raw markdown. Unlike `.agents/skills/` and `.opencode/skills/`, these wrapper files are **tracked in git** — they're a few lines each and are this project's own configuration, not a copy of external plugin content.

Verify with `opencode debug config` and check the `command` key — it should list all 28 names, each with a parsed `description` and `template`.

**Keeping wrappers in sync**: when a skill is added to or removed from `.agents/skills/`, add or remove its matching `.opencode/commands/<name>.md` wrapper too. There's no automated sync for this yet — do it by hand (or re-run the generation step) alongside `setup-matt-pocock-skills` updates.

## Keeping the project-level copies in sync

The project-level copies are **snapshots**, not live links. When the `mattpocock-skills` plugin updates a skill, or `~/.agents/skills/<name>/SKILL.md` is edited directly, the copy under this repo's `.agents/skills/<name>/` does not update automatically. Re-run `setup-matt-pocock-skills`, or manually re-copy the changed skill directory, to refresh it.

## If a skill doesn't show up as a registered command

Before relying on a skill in a given tool, confirm that tool actually registered it:

- **Claude Code**: always works — it's the source of truth these skills were authored for.
- **OpenCode**: run `opencode debug skill` and confirm the skill's `name` appears in the list, and run `opencode debug config` and confirm the same name appears under `command` (for `/name` invocation — see the section above).
- **Antigravity**: no CLI equivalent is known; check its skill UI/autocomplete directly.

**If a skill isn't registered — in Antigravity, or in OpenCode without a matching `.opencode/commands/<name>.md` wrapper — typing its slash form (e.g. `/triage`) does not load its actual instructions.** The agent sees the literal text as a plain prompt (or, in OpenCode, gets the raw `SKILL.md` file content dumped into the compose box via a fuzzy file-path fallback) and improvises a best-effort response from general knowledge plus whatever `AGENTS.md` says — no rubric, no forced steps, none of the real `SKILL.md` process. This can look like it worked while actually being a materially weaker, ad hoc substitute. When in doubt, open `.agents/skills/<name>/SKILL.md` directly and follow it manually.
