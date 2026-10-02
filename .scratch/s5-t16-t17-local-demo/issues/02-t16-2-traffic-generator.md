# 02: S5.T16.2 — Traffic generator

**What to build:** a small Go client that sends realistic traffic to an LB: Poisson arrivals at its Zipf-ranked share of a total rate, a mix of response sizes and request-body sizes. The total rate and the target LB can be changed while it runs, and it reports honestly what it offered, sent, dropped and completed, so a viewer-facing rate claim can be checked.

Spec: `../spec.md` — *Traffic generator (S5.T16.2)*; ADR-0023 decision 6; ADR-0021/ADR-0022 for the resource bounds.

**Blocked by:** None (can start immediately). S5.D1 is done.

**Status:** ready-for-agent

- [ ] A new Go program in the repository's single module, with its own small image (dummy-backend precedent); no new third-party dependency.
- [ ] Startup env, strictly parsed (dummy-backend conventions, errors name the variable): `RANK` (1..N), `CLIENTS` N (default 8), Zipf exponent (default `1.0`), initial total rate, initial target LB, the allowlist of target LB base URLs, the in-flight bound.
- [ ] Share: client k sends at `total × (1/k^s) / Σ_{i=1..N} (1/i^s)`; no coordination between clients.
- [ ] Arrivals are Poisson (exponential inter-arrival at the client's own rate), scheduled open-loop.
- [ ] Mix defaults: response size mostly `/200b` and `/10kb` with a small `/1mb` share; request bodies a mix of none / small / medium sent as `POST` to the payload paths (which S5.T16.1 makes accept a body). Weights are constants named in one place and documented.
- [ ] Bounded resources: a fixed maximum of in-flight requests; when an arrival is due and the bound is full it is counted as **dropped**, never queued without limit; response bodies drained and discarded; the compose file (S5.T16.3.2) sets `GOMEMLIMIT`/`GOGC` and the program documents that it expects them.
- [ ] Control endpoint on its own port (internal network only): set total rate and/or target; read current settings and the offered / sent / dropped / completed / error counters. A target not on the allowlist, a negative rate, or an unknown field → 400. A target change applies to the next arrival; in-flight requests to the old target complete normally.
- [ ] Startup log line records rank, share, target and bounds (`log/slog`, canonical field names).
- [ ] Tests, Red-first, against an `httptest` server standing in for an LB and through the control endpoint only: share-by-rank table (ranks × exponents); received rate within a generous tolerance over a fixed window; arrivals are not a fixed interval (loose dispersion check); the size mix appears in received requests; runtime rate and target change take effect; disallowed target rejected; the in-flight bound holds against a slow stand-in and excess arrivals are counted as dropped; race-clean.
- [ ] `make test`, `make test-race`, `go vet ./...`, `make fmt` clean.
