# 02: S4.T5 — Client cancellation correctness

**What to build:** A client that disconnects mid-request reaches no observer, records no EWMA latency, releases its active-connection slot, and is recorded as 499 (status_class "4xx"). The error handler classifies every failed round trip into exactly one of three buckets, in order: drain cancellation (existing ErrDrainWindowExpired check, unchanged), client-gone (the client's own request context is done), genuine transport failure (unchanged: 502, WARN, observers get the 2s penalty). Client-gone writes 499 to the recorder and logs at INFO with the new client_canceled reason; the whole-request counter still records the request (499/4xx) so client churn stays visible and the 5xx class stays reserved for backend-caused failures. reqState gains a clientCtx field captured in ServeHTTP before the cancel-with-cause derivation (request-goroutine-only per ADR-0007's model). The log vocabulary gains client_canceled with its first emitter. Shutdown-time cancellations classifying as client-gone is an accepted, desirable conflation. Chaos tests prove all three tiers.

**Blocked by:** 01 (S4.D1 tracking amendment)

**Status:** ready-for-agent

- [ ] reqState gains clientCtx; ServeHTTP captures the client request context
- [ ] errorHandler implements the three-tier predicate in order: drain cause, clientCtx.Err(), transport failure
- [ ] Client-gone: observers suppressed, no EWMA record, slot released, 499 written, INFO log with reason client_canceled
- [ ] Transport failure path unchanged (502, WARN, observers get the 2s penalty)
- [ ] Log vocabulary gains client_canceled with its first emitter
- [ ] Chaos test: client disconnect reaches no observer, records no latency, records 499
- [ ] Chaos test: response-header timeout still reaches observers as a failure
- [ ] Chaos test: drain cancellation still classifies as window_expired
- [ ] make test, make test-race, go vet, make fmt green
