# 06: S4.T3 — SIGHUP reload orchestration

**What to build:** The first demoable reload. An operator edits the backend
list and sends SIGHUP: unchanged backends keep serving with their state, added
backends take traffic after one successful probe, removed backends stop being
selected (their in-flight requests finish), and a broken or disallowed edit is
rejected with a log line while the old config keeps serving. Spec: stories 1–3,
6, 8, 14–18, 22–23, 25–26, 37–38, 56–57, 59, decisions D13–D14, D16. Design per
ADR-0015.

**Blocked by:** 05.

**Status:** ready-for-agent

- [ ] The application gains a **reload** operation taking a context and an
      already-parsed, validated config. It rejects the reload if the
      non-backend comparison (T1) is non-empty; otherwise diffs against the
      loaded-config record, applies to the registry (T2), replaces the record,
      and hooks up: checker add (as "added") for added backends and remove for
      removed ones; outlier forget for removed; seed series for added (active
      connections 0, healthy 0, circuit closed); delete healthy and
      circuit-state series for removed. The removed backends' active-connections
      series is left in place.
- [ ] `main` registers SIGHUP on a channel of buffer 1 and runs one reload-loop
      goroutine taking a context, the config path, the signal channel, the
      application, and a logger. Per signal: parse (strict) → validate →
      reload. Reloads never overlap; bursts collapse.
- [ ] Log vocabulary gains **config reloaded** (added / removed / unchanged
      counts; WARN when unchanged is zero, INFO otherwise — honouring the
      frozen "config reloaded" line) and **config reload failed** (reason: parse
      error, validation error, or the list of changed non-backend fields).
- [ ] `/readyz` reflects the live selectable set across a reload; `/startupz`
      stays latched.
- [ ] Reload-loop tests (fake signal channel, temp config file, captured
      logger): success; parse failure; validation failure; non-backend change
      rejected naming the field; signal burst collapses to one reload; the
      previous config keeps serving after every failure.
- [ ] Application-level test (chaos harness via build): requests in flight on
      unchanged backends across a reload that adds a backend all succeed; the
      added backend becomes selectable only after its first successful probe;
      a removed backend with no requests in flight leaves All/Selectable and
      its healthy/circuit series; a successive reload diffs against the
      previous reload's config.
- [ ] Hand-off recorded in the PROGRESS entry: a removed backend's in-flight
      requests finish with no bound, and its active-connections series is not
      deleted — both closed by T4.
- [ ] Manual smoke in the session log: `make run`, edit the example config,
      `kill -HUP`, observe log lines and `/metrics`.
- [ ] `make test`, `make test-race`, vet, fmt clean.
