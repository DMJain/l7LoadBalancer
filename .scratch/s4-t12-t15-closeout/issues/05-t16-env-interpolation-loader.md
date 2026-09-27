# 05: S4.T16 — Env interpolation in the loader

**What to build:** `${VAR}` expansion in backend URL strings, resolved in
`config.Load` after YAML decode and before the config is returned, so the
resolved URL is what validation checks and the environment is the only place
a secret lives. Unset, empty, or malformed references fail the load loudly;
non-backend fields pass through untouched. No new exported API surface —
`Config`'s shape is unchanged and callers that never use variables see an
identical contract. Spec: stories 14–29; the S4.T13 section.

**Blocked by:** 03 (S4.T14 — the reload path is proven before a config
feature rides on it).

**Status:** ready-for-agent

- [ ] `Load` gains the expansion step between decode and return; its doc
      comment states the expansion rule (it was "pure deserialization").
- [ ] Syntax: `${VAR}` where `VAR` matches `[A-Za-z_][A-Za-z0-9_]*`; no
      default syntax; non-backend fields pass through untouched.
- [ ] Unset **or empty** variable → load error naming the backend and the
      variable; malformed references (unterminated `${`, empty `${}`, invalid
      name characters) → load error naming the backend. Errors never contain
      the expanded value.
- [ ] The resolved URL is validated by the existing `validateBackendURL`, so
      a typo in an expanded value fails exactly like a literal typo.
- [ ] Reload semantics: interpolation re-runs on every `Load`; since backend
      identity is `(name, URL)` (ADR-0015), a changed expansion is one removed
      plus one added backend.
- [ ] Table-driven `Load` tests with `t.Setenv` (no `t.Parallel()`):
      expansion in host/port/credential positions; multiple variables in one
      URL; variables adjacent to literal text; unset/empty/malformed failures;
      passthrough of a `${...}` in a non-backend field.
- [ ] Composition test: two `Load`s differing only in environment, diffed —
      the affected identity is one removed plus one added backend.
- [ ] `configs/docker.yaml` still loads and validates interpolation-free
      (`TestDockerConfig` untouched).
- [ ] No new exported symbols; `Config` shape unchanged.
- [ ] `make test`, `make test-race`, vet, fmt clean.
