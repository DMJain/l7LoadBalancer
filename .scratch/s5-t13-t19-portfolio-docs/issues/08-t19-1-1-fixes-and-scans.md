# 08: S5.T19.1.1 — Safe fixes and report-first scans

**What to build:** the repository's known stale or exposed items are fixed where the fix is safe, and every judgement call is reported to the owner. Nothing is deleted without the owner's decision, and no history is rewritten.

Spec: `../spec.md` — *Hygiene pass: full checklist* (items 5–9, 13–17).

**Blocked by:** 07 (S5.T13.2).

**Status:** ready-for-agent

## In scope / out of scope (AGENTS.md Step 2.5)

- **In scope:** the safe fixes and report items named below.
- **Out of scope:** rewriting history of any kind, including the seven attribution trailers; pruning the planning directory, the OpenCode config file or the agent-tooling docs without the owner's decision; adding CI, linters or hooks (deferred by project policy); pushing anything; build, test and link verification (next ticket).

## Acceptance criteria

- [ ] **Resume directory:** added to `.gitignore`; the working tree no longer lists it as untracked; the findings note records that it was never committed.
- [ ] **Stale audit document:** moved to the history area with `git mv`, with the header "Snapshot as of 2026-09-19, superseded." and every link to the old location updated; a search for the old location returns nothing stale.
- [ ] **Go version text:** the agent guidance and the LSP guidance state the module's Go version (1.25.1, not "Go 1.22+"); the two agent-guidance symlinks still resolve to the guidance file.
- [ ] **Licence and progress file:** the licence is present (MIT, kept as is); the progress file has no in-progress entry other than this ticket's own.
- [ ] **Secret scan** over the working tree and all history (key-shaped strings for common providers, `.env` files, certificates, PEM headers): each hit listed with file, commit and match, or "no hits". Nothing is deleted.
- [ ] **Local-path and employer-reference scan** over the tree and all history (absolute home paths, the employer name, internal-looking hostnames): each hit listed, or "no hits".
- [ ] **Attribution-trailer inventory:** every commit with a `Co-Authored-By` trailer listed with hash, date, subject and trailer text (seven as of 2026-10-02), and its position relative to the published-benchmark commit. The finding is reported and the owner decides; no rewrite is performed.
- [ ] **Commit-history review (report only):** non-conventional subjects, the author identities, and commits that mix concerns.
- [ ] **Ship-or-prune record:** the owner's stated decision for the planning directory, the OpenCode config file, the agent-tooling docs and the history docs is recorded. The history docs ship. For the other three the ticket stops and asks, offering the scan results (no secrets, no absolute local paths, no employer references as of 2026-10-02).
- [ ] Findings are written to the session log with each judgement call marked for the owner.
- [ ] Docs and config only (TDD-exempt). The claim commit and each work commit are shown to the owner for approval first.
