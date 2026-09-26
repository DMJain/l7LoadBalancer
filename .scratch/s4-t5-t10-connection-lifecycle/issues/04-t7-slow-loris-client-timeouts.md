# 04: S4.T7 — Slow-loris client timeouts

**What to build:** A server: config section with read_timeout (pointer duration, default 60s, non-positive rejected naming the field, non-reloadable via NonBackendChanges). The client-facing server's ReadTimeout bounds the full request read including the body, so a slow-loris client — slow headers (already bounded by the 5s ReadHeaderTimeout) or slow body — is disconnected at the bound. WriteTimeout is deliberately omitted and the omission documented: it spans end-of-request-headers through the entire response body copy, so a slow-but-healthy upstream trips it. The sharpened known gap is recorded in the spec and config docs: a client that stalls reading while the proxy still has buffered data to send pins one goroutine; a finite body bounds the pin; the worst case is a dead-but-not-RST'd client holding it for the OS TCP retry window (Linux tcp_retries2 ~13-30 min), but only while data remains to write. Idle keep-alives remain bounded by the stdlib fallback chain (IdleTimeout -> ReadTimeout -> ReadHeaderTimeout), which resolves to 5s today.

**Blocked by:** 01 (S4.D1 — parallel to 02)

**Status:** ready-for-agent

- [ ] Config gains server.read_timeout (pointer, default 60s, non-positive rejected naming the field)
- [ ] Client-facing server wires ReadTimeout
- [ ] Slow-loris test: slow-body client disconnected at the bound
- [ ] WriteTimeout omission documented in config docs and spec rationale
- [ ] NonBackendChanges names "server"; a reload changing it is rejected
- [ ] make test, make test-race, go vet, make fmt green
