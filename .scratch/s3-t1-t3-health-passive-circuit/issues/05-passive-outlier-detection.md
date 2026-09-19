# 05: Passive Outlier Detection

**What to build:** A count-based sliding-window passive outlier detector
that registers into the round-trip observer fan-out and ejects a backend
(`MarkUnhealthy()`) after N failures within its window.

**Blocked by:** 01 (needs `MarkUnhealthy`), 02 (needs `RoundTripObserver`/
`Proxy.RegisterObserver` to register into)

**Status:** ready-for-agent

- [ ] A new type in `internal/health` (per that package's existing doc
      comment, which already names "passive outlier detection" as this
      package's job) holds a count-based sliding window of recent outcomes
      per backend; implements `RoundTripObserver`
- [ ] Failure signal: a 5xx response **or** an `errorHandler` transport
      failure, mixed within the same window
- [ ] On N failures within the window, calls `backend.MarkUnhealthy()`
      exactly once (not once per failure); window size and N are unexported
      Go constants
- [ ] No independent timer-based reinstatement in this detector — recovery
      is only via the next successful active probe (S3.T1/ticket 04)
- [ ] `main.go` constructs the detector and registers it via
      `Proxy.RegisterObserver` (02's mechanism), alongside the existing
      latency adapter
- [ ] Test: N failures within the window (mixing 5xx and transport-failure
      outcomes) ejects via `MarkUnhealthy()`, called exactly once
- [ ] Test: failures below the threshold, or that fall outside the window,
      do not eject
- [ ] Test: the detector correctly receives outcomes when registered
      alongside another observer (functional correctness of *this*
      observer's logic — the generic exactly-once/both-hooks fan-out
      guarantee itself is already proven in ticket 02, not re-proven here)
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on completion

## Comments
