# 03: h2c support via `h2c.NewHandler`

**What to build:** An operator sets `h2c: true` in config (without a `tls:` block), starts the LB, and clients connect via HTTP/2 over cleartext. Both prior-knowledge (client speaks HTTP/2 frames immediately) and HTTP/1.1 Upgrade (client sends `Upgrade: h2c` header) connection modes work. After this ticket, `App.Build` has three distinct handler chain branches — plain, TLS, and h2c — not an unconditional h2c wrapper with a no-op path. The h2c handler is outermost because it must inspect raw connection bytes for the HTTP/2 `PRI` preface.

**Blocked by:** 02 (TLS listener — establishes handler chain branching)

**Status:** ready-for-agent

- [ ] `golang.org/x/net` added to `go.mod` (first x/ dependency — consider ADR-0004 amendment noting the exception)
- [ ] `H2C` bool field parsed from config
- [ ] Mutual exclusivity validation with `tls:` block works (may already exist from ticket 02)
- [ ] `H2C` added to `NonBackendChanges()` comparison
- [ ] Handler chain in h2c mode: `h2c.NewHandler(metricsMiddleware(proxyHandler), &http2.Server{})` — h2c outermost
- [ ] h2c handler is only present in h2c mode; plain and TLS modes have a different handler chain
- [ ] Zero-value `http2.Server{}` — no tuning without benchmark data
- [ ] `ReadTimeout: 0` in h2c mode (same rationale as TLS — HTTP/2 is HTTP/2 regardless of encryption)
- [ ] Integration test: h2c prior knowledge — `resp.Proto == "HTTP/2.0"` using `http2.Transport` with `AllowHTTP: true` and custom `DialTLSContext` that dials plaintext
- [ ] Integration test: h2c Upgrade — HTTP/1.1 request with `Upgrade: h2c` header succeeds
- [ ] `make test-race` passes
