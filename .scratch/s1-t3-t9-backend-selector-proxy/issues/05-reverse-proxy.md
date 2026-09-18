# 05: Reverse Proxy

**What to build:** `internal/proxy.Proxy`, wrapping `httputil.ReverseProxy`, so an incoming HTTP request is actually selected, dispatched to a live backend, connection-counted, and logged end-to-end — the first point where the whole request lifecycle (not just an algorithm in isolation) becomes real and observable.

**Blocked by:** 01, 02 (needs the Registry and a working concrete `Selector` for its integration-test fixture; LeastConnections is not required to exist for this ticket)

**Status:** ready-for-agent

- [ ] `New(registry, selector) http.Handler` wraps one `*httputil.ReverseProxy` built once
- [ ] `ServeHTTP` calls `selector.Select` itself (not `Director`) and short-circuits an immediate 503 on `ErrNoHealthyBackends`, bypassing `ReverseProxy` entirely for that path
- [ ] On success, `ServeHTTP` calls `IncActive()` and attaches the chosen backend to the request context (unexported context-key type); `Director` reads it back and only sets `req.URL.Scheme`/`Host`
- [ ] `ActiveConns` decrement happens via a response-body wrapper whose `Close()` decrements exactly once, installed in `ModifyResponse` — never in `Director` or directly in `ModifyResponse`
- [ ] `ErrorHandler` also decrements on backend round-trip failure, logs it, and responds 502
- [ ] One structured "request complete" `slog` line per request (fields: `backend`, `method`, `status`, `latency_ms`, `remote_addr`, `path`) on the success, 503, and 502 paths alike — `WARN` for 5xx, `INFO` otherwise
- [ ] Test: distribution over N requests matches the selector's expected pattern
- [ ] Test: no healthy backend → 503, no panic/hang
- [ ] Test: 100 concurrent in-flight requests → `ActiveConns` returns to 0 within a small drain window
- [ ] Test: backend round-trip failure → 502 and `ActiveConns` still decremented
