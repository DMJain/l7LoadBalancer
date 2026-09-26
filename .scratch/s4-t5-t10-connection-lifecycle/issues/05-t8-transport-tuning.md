# 05: S4.T8 — Transport tuning

**What to build:** A transport: config section with four pointer fields and exported defaults: dial_timeout (5s), response_header_timeout (30s), max_idle_conns_per_host (100), idle_conn_timeout (90s). The proxy's ReverseProxy gains a custom http.Transport (DialContext with the dial timeout, ResponseHeaderTimeout, MaxIdleConnsPerHost, IdleConnTimeout) replacing http.DefaultTransport. Implementation note, not a config field: the transport's total MaxIdleConns is sized at max_idle_conns_per_host x backend_count (or 0 = unlimited) in the construction code, so the per-host knob is not silently capped by the stdlib default of 100. Behavior tests: a non-accepting address fails at the dial timeout; a gated backend fails at the response-header timeout; a reload changing transport is rejected.

**Blocked by:** 01 (S4.D1 — parallel to 02)

**Status:** ready-for-agent

- [ ] Config gains the four transport fields with defaults and validation
- [ ] ReverseProxy runs on a configured http.Transport (dial, response-header, pool)
- [ ] Total MaxIdleConns sized from per-host x backend count (or 0)
- [ ] Behavior test: dial timeout against a non-accepting address
- [ ] Behavior test: response-header timeout against a gated backend
- [ ] NonBackendChanges names "transport"; a reload changing it is rejected
- [ ] make test, make test-race, go vet, make fmt green
