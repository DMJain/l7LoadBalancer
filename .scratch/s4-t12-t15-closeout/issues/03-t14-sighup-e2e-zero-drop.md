# 03: S4.T14 — SIGHUP end-to-end zero-drop test

**What to build:** The exit criterion "SIGHUP with 1000 in-flight requests
drops zero" re-demonstrated at the OS boundary: one test in the chaos package
that builds the real binary, spawns it, holds 1000 requests in flight through
gated counting backends, rewrites the config file, sends a real `SIGHUP`,
proves zero drops, and proves live traffic shifts across the signal. It is
the OS-boundary sibling of the in-process
`TestChaosReloadDrainExitCriterion1000`; the two are explicitly non-overlapping
(story 13 of the spec) — registry views stay in-process, the e2e asserts
externally visible outcomes only. Spec: stories 1–13; the S4.T12 section.

**Blocked by:** 02 (S4.T13 — the gated counting harness).

**Status:** ready-for-agent

- [ ] Build the binary under test with `go build -C` pointed at the module
      root, output in `t.TempDir()`; skip cleanly via `exec.LookPath("go")`
      when no toolchain is present.
- [ ] Spawn the binary with `-config` pointing at a temp config; capture
      stdout/stderr and replay through `t.Logf` on failure; poll the client
      listener until it answers (any HTTP response means up).
- [ ] Grab a free `127.0.0.1:0` port for the config's `listen` (metrics and
      health-endpoint listens use `:0`); raise `RLIMIT_NOFILE` soft-to-hard
      best-effort at start.
- [ ] Fire 1000 concurrent GETs through one `http.Client`; confirm via the
      per-backend counters (sum == 1000) that all are held before the signal.
- [ ] Rewrite the config atomically (temp file + `os.Rename`): one backend
      added, one removed, `drain_window` unchanged (not reloadable, ADR-0016).
- [ ] Send `syscall.Kill(pid, syscall.SIGHUP)`; while the 1000 are held, fire
      probe requests and assert they land on the added backend (counter grows
      past its admission-probe baseline) and never on the removed one
      (counter frozen).
- [ ] Release the gates; assert all 1000 return 200, the removed backend's
      counter equals its pre-signal value, and the process is still alive.
- [ ] Teardown: `SIGTERM`, wait with a timeout, assert exit status 0.
- [ ] The in-flight wait uses a deadline sized for process spawn and socket
      setup (30s constant), never sleeps; the test's doc comment states the
      in-process/e2e division of labor.
- [ ] PROGRESS entry marks the exit criterion as evidenced at the OS boundary,
      citing both tests.
- [ ] `make test`, `make test-race`, vet, fmt clean.
