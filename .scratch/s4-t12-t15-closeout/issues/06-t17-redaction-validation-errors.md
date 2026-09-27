# 06: S4.T17 — Redaction: validation errors never contain the expanded URL

**What to build:** One redaction contract: `validateBackendURL`'s error
messages drop the raw URL and name only the backend and the field, so a
resolved secret cannot reach the logs through the error path (`main` logs
load and validation errors). The change is small and test-safe — verified
that no test asserts the message wording. The registry's URL-parse error
needs no change: it is unreachable for config-sourced URLs because
`validateBackendURL` already runs `url.Parse` — record that structural
argument in the session log. Spec: S4.T13 section, "Redaction" bullet
(grilling Q6).

**Blocked by:** 05 (S4.T16 — interpolation; the redaction only matters once
secrets can be in URLs).

**Status:** ready-for-agent

- [ ] `validateBackendURL` error messages name only the backend and the field;
      the raw URL (resolved or template) never appears.
- [ ] A test proves an invalid interpolated URL produces a load error that
      does not contain the expanded value.
- [ ] The registry parse error is confirmed unreachable post-validation; the
      structural argument is recorded in the session log.
- [ ] `make test`, `make test-race`, vet, fmt clean.
