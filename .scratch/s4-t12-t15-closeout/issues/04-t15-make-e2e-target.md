# 04: S4.T15 — `make e2e` target

**What to build:** One make target that runs the SIGHUP end-to-end test, so
the e2e is runnable without disturbing the default suite. `make test` and
`make test-race` are unchanged — the e2e is opt-in because it builds a binary
and spawns a process. One target, no race variant: the e2e's subject is the
OS boundary (signal delivery, process lifecycle, real sockets), and the
concurrency claims are already covered by the in-process suite under `-race`.
Spec: S4.T12 section, "Make target" bullet.

**Blocked by:** 03 (S4.T14 — the e2e test).

**Status:** ready-for-agent

- [ ] `make e2e` runs the single e2e test with a generous `-timeout`.
- [ ] The target's help text documents a `ulimit -n` invocation for
      restrained environments.
- [ ] `make test` and `make test-race` are unchanged.
- [ ] `make e2e` passes locally.
