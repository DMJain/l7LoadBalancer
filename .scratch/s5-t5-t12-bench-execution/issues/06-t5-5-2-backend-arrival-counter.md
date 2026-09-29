# 06: Backend arrival counter (S5.T5.5.2)

**What to build:** Each dummy backend reports how many benchmark requests have **arrived** at it. This gives a per-backend request distribution that works the same way for both competitors. It is consumed by the hot-key distribution (ticket 12), the degraded slice (13) and the drain-reload check (15).

**Blocked by:** 05 (same handler; serial to avoid conflicts)

**Status:** ready-for-agent

Spec: `../spec.md` (Dummy-backend instrumentation)

- [ ] `GET /stats` returns 200 with a JSON body of the form `{"backend":"<name>","requests":<n>}`.
- [ ] The count is a single per-process `sync/atomic.Int64`, **not** a mutex-guarded map, because each backend is its own container. It is read with `.Load()`.
- [ ] It increments **on arrival**: the handler's first action, before any injected sleep or failure decision.
- [ ] `/health` and `/stats` do not increment it.
- [ ] `/stats` bypasses the injected sleep and failure rate, as `/health` does.
- [ ] The file's concurrency note documents the counter (AGENTS.md: every file with shared state).
- [ ] TDD, Red-first, through the handler seam with only HTTP-visible assertions:
  - response shape;
  - counts on the payload and default paths and excludes `/health` and `/stats`;
  - **arrival semantics**: with an injected delay, `/stats` reflects a request that hasn't completed yet;
  - N concurrent requests yield exactly N under `-race`.
- [ ] `make test`, `make test-race`, `go vet ./...`, `make fmt` clean.
