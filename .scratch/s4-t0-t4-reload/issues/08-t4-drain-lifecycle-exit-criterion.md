# 08: S4.T4 — Drain lifecycle and the zero-drop exit criterion

**What to build:** Removed backends now **drain**: each finishes its in-flight
requests until it is idle or the **drain window** expires, at which point the
rest are cancelled; either way it is then forgotten, its last metric series is
removed, and one log line says how the drain ended. This closes Sprint 4's
first exit criterion — SIGHUP with 1000 in-flight requests drops zero — with a
test that demonstrates it. Spec: stories 9, 11, 19, 24, 32–34, 60, 63, 65–67,
decisions D20, D22. Design per ADR-0016.

**Blocked by:** 07.

**Status:** ready-for-agent

- [ ] Reload starts one drain goroutine per removed backend. It waits for
      whichever comes first: active connections at zero (polled ~100ms) or the
      drain window timer; on expiry it retires the backend (T4.0) and waits for
      active connections to reach zero.
- [ ] On completion it deletes the backend's active-connections series only if
      no current backend shares the name, and logs one **backend drained**
      event with reason `idle` or `window_expired` and the number of requests
      cancelled. The log vocabulary gains the event and the `idle` reason.
- [ ] Drain goroutines select on the process shutdown context and exit
      immediately without cleanup; shutdown timing is unchanged.
- [ ] A drain is independent of later reloads: re-adding the same identity
      creates a fresh backend and neither revives nor ends the draining one.
- [ ] **Exit test, case 1** (chaos harness, `-race`): 1000 concurrent requests
      held by gated backends — some unchanged, some on a backend being removed;
      reload adds one and removes one, drain window longer than the hold;
      release; all 1000 return 200. After: removed backend absent from All,
      active connections zero everywhere, one drained line with reason `idle`,
      no series left for it, unchanged backends are the same instances with
      circuit and EWMA state intact.
- [ ] **Exit test, case 2:** drain window shorter than the hold; removed name
      re-added under a new URL; the removed backend's in-flight requests get
      502 with `window_expired`; one drained line with reason `window_expired`
      and the cancelled count; the fresh same-name backend's circuit, outlier,
      and gauge state reflect only its own traffic.
- [ ] Shutdown-during-drain test: cancelling the process context while a drain
      is running returns promptly.
- [ ] PROGRESS entry notes the Sprint 4 exit criterion "SIGHUP with 1000
      in-flight requests drops zero" as met, citing the test.
- [ ] `make test`, `make test-race`, vet, fmt clean.
