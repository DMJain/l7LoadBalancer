# 02: Round-Trip Observer Fan-Out

**What to build:** Generalize the proxy's existing single hardcoded
`state.backend.RecordLatency(...)` call at `modifyResponse`/`errorHandler`
into a generic `RoundTripObserver` fan-out, sized for three listeners from
the start (latency recording, passive-outlier detection, circuit breaker)
even though only the first is wired by the end of this ticket. Prefactor:
unblocks both S3.T2 (passive outlier detection) and S3.T3 (circuit breaker),
neither of which needs the other to register into this mechanism.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] `internal/proxy` gains a `RoundTripObserver` interface —
      `ObserveRoundTrip(b *backend.Backend, d time.Duration, success bool)` —
      per ADR-0011 decision 9
- [ ] `Proxy` gains an additive `RegisterObserver(o RoundTripObserver)`
      method; `New(reg, sel)`'s existing two-argument signature is untouched
- [ ] `modifyResponse` computes `success := resp.StatusCode < 500` and
      `d := time.Since(state.dispatchStart)`; `errorHandler` always passes
      `success = false, d = p2cFailurePenalty`; both loop over every
      registered observer, unconditionally — regardless of any future
      circuit state (recording is unconditional, gating is conditional; no
      observer call site should ever gain an early-return guard)
- [ ] The existing hardcoded `RecordLatency` call is replaced by registering
      a small adapter observer that does the same thing; `main.go` registers
      it after constructing the `Proxy`
- [ ] Test: every existing P2C-EWMA / proxy latency-recording test still
      passes, unchanged in outcome, now routed through the new mechanism
- [ ] Test: registering two observers (the latency adapter plus a test spy)
      and sending one request through a 5xx-fixture backend invokes each
      exactly **once** — not zero, not twice — asserting invocation *count*,
      not just content
- [ ] Test: the same invocation-count assertion against a connection-refused
      fixture backend, specifically exercising the `errorHandler` path (the
      fan-out's other terminal hook)
- [ ] Test: a successful 2xx request invokes each observer exactly once with
      `success=true` and a real, non-zero duration
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on completion

## Comments
