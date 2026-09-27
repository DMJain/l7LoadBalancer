# 02: S4.T13 — Harness: gated counting backends

**What to build:** One additive failure-injection capability in the chaos
harness: a per-backend atomic request counter, incremented at request entry
before any gating, so a test can assert that traffic shifted to one backend
or froze on another without reading proxy internals. The counter is additive
— existing gated backends, `ServeGated`, and every current test are
unchanged. Spec: S4.T12 section, "Gated counting backends" bullet.

**Blocked by:** 01 (S4.T12 — amendment-first).

**Status:** ready-for-agent

- [ ] The harness backend type gains an atomic request counter incremented at
      entry, before gating, readable by tests.
- [ ] Existing chaos tests are untouched and stay green (`make test`,
      `make test-race`).
- [ ] A test proves the counter increments once per request and is readable
      without touching proxy or registry internals.
