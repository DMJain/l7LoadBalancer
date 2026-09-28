# 02: TLS listener with ALPN and self-signed cert

**What to build:** An operator adds a `tls:` block to their config with `cert_file` and `key_file` paths, starts the LB, and clients connect via TLS with HTTP/2 automatically negotiated via ALPN. A `scripts/generate-cert.sh` produces a SAN certificate for local dev and benchmarks. The server disables `ReadTimeout` in TLS mode (HTTP/2 connections are long-lived and multiplexed; a connection-level read deadline kills active streams) and relies on `IdleTimeout` for cleanup. Config validation rejects `tls:` and `h2c: true` appearing simultaneously with an explicit mutual-exclusivity error. TLS fields are added to `NonBackendChanges()` so changing TLS config requires a full restart. Metrics and health endpoints remain plain HTTP regardless.

**Blocked by:** 01 (metric rename)

**Status:** ready-for-agent

- [ ] `scripts/generate-cert.sh` creates a SAN cert covering: `localhost`, `127.0.0.1`, `::1`, `backend1`–`backend4`, `lb`. Must use SANs, not just CN — Go rejects certs without SANs since Go 1.15. Output to a known path; `certs/` directory gitignored.
- [ ] New `TLSConfig` struct with `CertFile` and `KeyFile` string fields added to config
- [ ] Presence-based config: `tls:` block present triggers TLS mode, absence means plain HTTP
- [ ] Validation rejects `tls:` and `h2c: true` simultaneously with an explicit error about mutual exclusivity
- [ ] `App.Build` constructs `http.Server` with `tls.Config` when TLS mode is active
- [ ] `App.Run` calls `ListenAndServeTLS` in TLS mode
- [ ] `ReadTimeout: 0` hardcoded in TLS mode (not a config knob). `IdleTimeout` set from config. Code comment references Go docs on `ReadTimeout` and HTTP/2 semantics.
- [ ] `IdleTimeout` field added to `ServerConfig` struct (was implicit before, now needed as primary timeout in HTTP/2 modes)
- [ ] TLS and h2c fields added to `NonBackendChanges()` — changing them blocks SIGHUP reload
- [ ] Metrics and health endpoints remain plain HTTP regardless of client listener mode
- [ ] Integration test: TLS handshake succeeds, `ConnectionState().NegotiatedProtocol == "h2"`
- [ ] Integration test: `resp.Proto == "HTTP/2.0"` for requests through TLS listener
- [ ] `make test-race` passes
