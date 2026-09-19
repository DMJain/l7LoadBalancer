# LSP: OpenCode

OpenCode ships 28 built-in language servers but LSP is **disabled by default**. This repo turns it on so agents working through OpenCode get real diagnostics and go-to-definition instead of guessing from grep.

## Config

`opencode.jsonc` at the repo root (tracked in git — this is a one-line project setting the repo itself owns, not a copy of external plugin content, so it doesn't fall under the `.agents/skills/`-style gitignore rule described in `docs/agents/skills.md`):

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "lsp": true
}
```

`"lsp": true` enables every built-in server. The one that matters for this repo is `gopls` for `.go` files, which OpenCode resolves on demand through the `go` toolchain — this repo already requires Go 1.22+, so no extra install step. `gopls` does not need to be pre-installed or on `PATH`.

## Verifying

```
opencode debug config                              # confirm "lsp": true resolved
opencode debug lsp diagnostics internal/backend/backend.go
```

The second command should return `[]` for a clean file. Verified during setup: it also correctly flagged a deliberately-introduced `declared and not used` error in a scratch `_test.go` file, confirming gopls actually starts and analyzes code (not just a config no-op).

## Scope

- Claude Code and Antigravity are unaffected — `opencode.json`/`opencode.jsonc` is an OpenCode-only config surface, separate from the skill/command mirroring described in `docs/agents/skills.md`.
- Per-server overrides (disable a specific built-in, add a custom server for a language this repo doesn't use yet) go under `"lsp": { "<name>": { ... } }` in the same file. See the upstream docs: https://opencode.ai/docs/lsp/.
