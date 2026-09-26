# 03: S4.T6 — Backend death mid-response

**What to build:** A backend killed after sending response headers but before completing the body is logged at WARN with the new backend_died_mid_response reason, carrying backend, path, and bytes already copied. The success recorded when headers arrived stands — no observer is fed a second (failure) event for the same request, because that would corrupt the outlier window's counts (you can't un-ring the bell; distinct from ADR-0016's voluntary drain stop). Detection lives in the existing body wrapper (releaseBody): any read error that is not io.EOF is mid-body death, with bytes copied tracked trivially in Read; the slot release stays once-guarded. Pre-headers death is unchanged: clean 502, failure observed — the exit-criterion chaos test proves it (docker kill a backend mid-response: clean 502, no crash, no leak). The known gap is documented: a backend that consistently dies after headers is never ejected by passive detection; fixing it means deferring observer notification to body completion, out of scope.

**Blocked by:** 02 (S4.T5 — same errorHandler/releaseBody path)

**Status:** ready-for-agent

- [ ] releaseBody observes read errors; a non-EOF error is mid-body death
- [ ] Mid-body death logs WARN with reason backend_died_mid_response, backend, path, bytes copied
- [ ] No observer feed; the headers-time success stands (outlier window and circuit unchanged)
- [ ] Slot release still exactly once
- [ ] Log vocabulary gains backend_died_mid_response with its first emitter
- [ ] Chaos test: backend killed mid-body produces the WARN line with bytes, success stands
- [ ] Chaos test: backend killed pre-headers produces a clean 502, failure observed, no crash, no leak (exit criterion)
- [ ] Known gap (mid-body ejection invisibility) documented in the session log
- [ ] make test, make test-race, go vet, make fmt green
