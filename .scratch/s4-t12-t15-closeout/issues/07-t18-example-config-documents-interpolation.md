# 07: S4.T18 — Example config documents interpolation

**What to build:** The `configs/example.yaml` documentation block for
interpolation, so the feature is discoverable (precedent: the
`reload.drain_window` documentation). Spec: story 27; the S4.T13 section,
"Config examples" bullet.

**Blocked by:** 05 (S4.T16 — interpolation).

**Status:** ready-for-agent

- [ ] `configs/example.yaml` documents the `${VAR}` syntax, the
      unset/empty/malformed failure behavior, and the backend-URL-only scope.
- [ ] The documented examples match the implementation's actual behavior.
- [ ] Docs-only: no code, no tests (TDD-exempt per AGENTS.md).
