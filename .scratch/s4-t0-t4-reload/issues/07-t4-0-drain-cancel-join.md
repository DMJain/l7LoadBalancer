# 07: S4.T4.0 — Drain window config, retired context, and drain-cancel join + ADR-0016

**What to build:** The mechanism that lets a removed backend's in-flight
requests be cut off on demand: a `reload.drain_window` config field, a
per-backend **retired** context, and the proxy joining each outbound request to
it so that retiring a backend cancels its in-flight requests with a clean 502
that is logged as `window_expired` and counted as no backend's failure. ADR-0016
is written first. Nothing retires a backend in production yet (T4 does).
Spec: stories 10, 12–13, 44–45, 61–62, 64, decisions D18–D19, D21.

**Blocked by:** 06.

**Status:** ready-for-agent

- [x] **ADR-0016 (drain lifecycle)** is written and committed in the design
      step, before code: retired context + after-func join (and why not a
      per-request goroutine or a cancel-func registry); two-phase polling
      drain; cancel-at-window with 502; truncated body after headers;
      abandonment on shutdown; independence from later reloads. Amends ADR-0002
      decision 5. `AGENTS.md` gains its row.
- [x] Config gains a top-level `reload` block with `drain_window` (pointer
      duration, default 30s when omitted, zero/negative rejected naming the
      field, nested typos rejected by strict decoding). The T1 non-backend
      comparison includes it. The example config documents it.
- [x] `Backend` gains an unexported retired context, created at construction,
      with method-only access to retire it and to observe it.
- [x] The proxy derives each outbound request context with cancel-with-cause
      and registers an after-func on the chosen backend's retired context,
      stopped when the request completes. No goroutine per request.
- [x] A drain-cancelled request before headers: 502, slot released, WARN line
      with reason `window_expired`; observers not called (the T2 removed flag).
      After headers: truncated body, the success already recorded stands.
      The log vocabulary gains the `window_expired` reason.
- [x] Tests Red first: config load/validate cases; retiring a backend with
      gated in-flight requests yields 502s with the reason and active
      connections return to 0; requests on other backends unaffected; a
      post-headers retirement truncates without a recorded failure;
      `-race` clean.
- [x] A benchmark of the proxy request path with the join; result recorded in
      the session log.
- [x] `make test`, `make test-race`, vet, fmt clean.
