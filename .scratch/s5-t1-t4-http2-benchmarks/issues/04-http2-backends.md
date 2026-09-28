# 04: HTTP/2 to backends via scheme-driven transport

**What to build:** An operator points backends at `https://` URLs and the LB speaks HTTP/2 to them via ALPN — no extra config needed beyond `force_http2: true` (default) and `tls_skip_verify: true` (for self-signed certs). The `protocol` label on `lb_requests_total` shows whether clients connected via `http/1.1`, `h2`, or `h2c`. Mixed `http://` and `https://` backend URLs work in the same config for incremental migration. A structured log field `backend_proto` gives runtime visibility into backend-side protocol negotiation.

**Blocked by:** 02 (TLS listener — provides TLS infrastructure for testing HTTPS backends). Can run in parallel with 03.

**Status:** ready-for-agent

- [ ] `TransportConfig` struct gains `ForceHTTP2` (default true) and `TLSSkipVerify` (default false) fields
- [ ] `buildTransport()` sets `ForceAttemptHTTP2: true` when `force_http2` is true
- [ ] `buildTransport()` sets `TLSClientConfig` with `InsecureSkipVerify` when `tls_skip_verify` is true
- [ ] **Critical**: `ForceAttemptHTTP2` is explicitly set when `TLSClientConfig` is present. Go's automatic HTTP/2 is silently lost the moment you customize `TLSClientConfig` — the most common Go HTTP/2 mistake. Without this, all HTTPS backends silently fall back to HTTP/1.1 with no error or warning.
- [ ] Mixed `http://` and `https://` backend URLs accepted without validation error or warning
- [ ] `protocol` label added to `lb_requests_total`. Values: `http/1.1`, `h2`, `h2c`. Source: `req.Proto`. Cardinality bounded at 3 values. Goes on the existing counter, not a new one.
- [ ] Structured log field `backend_proto` added for backend-side protocol visibility
- [ ] Integration test: `httptest.NewTLSServer` as backend, LB configured with `https://` URL + `tls_skip_verify: true`, assert backend sees HTTP/2 request
- [ ] Integration test: `http://` backends still produce HTTP/1.1 requests
- [ ] Config validation test: `force_http2` defaults to true when omitted
- [ ] Config validation test: `tls_skip_verify` defaults to false when omitted
- [ ] `make test-race` passes
