# 07: Wire main.go End-to-End

**What to build:** Replace `cmd/l7LoadBalancer/main.go`'s placeholder 501 handler with the real wiring path — config → registry → selector → proxy → server — so `make run` starts a load balancer that actually balances load, closing out Sprint 1's exit criteria.

**Blocked by:** 01, 02, 03, 05 (needs both selector implementations, since the config-to-selector factory must dispatch on either `round_robin` or `least_conn`)

**Status:** ready-for-agent

- [ ] Wiring order: `config.Load(*configPath)` → fatal + non-zero exit on error (no silent fallback) → `backend.NewRegistry` → `balancer.NewFromConfig(cfg, reg)` → `proxy.New(reg, sel)` → `http.Server`
- [ ] `NewFromConfig` (the config-string → selector-type switch) lives in `balancer`, not `main.go` — `main.go` stays a thin wiring layer
- [ ] Existing SIGINT/SIGTERM graceful-shutdown behavior preserved unchanged
- [ ] `main.go` logging goes through the existing `internal/logger` setup (dedup, no new flag)
- [ ] Manual smoke test: `make run` against `configs/example.yaml` distributes requests across backends per the configured algorithm — documented in the Sprint 1 session log (no new automated test; covered by tickets 02/03/05's suites)
