# 01: S5.T16.1 — Dummy-backend admin listener

**What to build:** in the demo, the owner can change any backend's latency, jitter and failure rate while it serves traffic, and the very next proxied request reflects the change — no restart, no recreate. Outside the demo (bench rig, repo-root stack) nothing changes: the listener does not exist unless explicitly switched on. The payload paths also accept a request body, so the traffic generator can send uploads.

Spec: `../spec.md` — *Dummy-backend admin listener (S5.T16.1)*; ADR-0023 decision 7.

**Blocked by:** None (can start immediately). S5.D1 is done.

**Status:** ready-for-agent

- [ ] **Prefactor first, no behaviour change:** the backend's fixed sleep/failure settings become one immutable profile value (`sleep_ms`, `jitter_ms`, `fail_rate`) held behind an atomic pointer and read once per request; the env values are the initial profile (jitter 0). Every existing dummy-backend test stays green unchanged — that is the gate for the prefactor.
- [ ] `ADMIN_ENABLED` parsed with the `envBool` strictness: unset/empty → off; `true`/`false` accepted; any other value → startup error naming `ADMIN_ENABLED`.
- [ ] When on, a second `http.Server` listens on `:9091`; it is never a path on the proxied port. When off, no admin listener is started.
- [ ] Admin contract: `POST` only (other methods → 405 with `Allow: POST`); JSON body with optional `sleep_ms`, `jitter_ms`, `fail_rate`; unknown fields → 400; negative ms or `fail_rate` outside [0, 1] → 400 naming the field; **an omitted field keeps its current value**; the response is the full profile now in effect.
- [ ] Effective sleep is `sleep_ms` plus a uniform offset in `[-jitter_ms, +jitter_ms]`, clamped at ≥ 0.
- [ ] `/health` and `/stats` keep bypassing injected sleep and failure.
- [ ] The payload paths (`/200b`, `/10kb`, `/1mb`) accept `POST` with a body, which is read fully and discarded; the response is the same as for `GET`. `GET` behaviour is unchanged; other methods and other paths keep today's 405 behaviour.
- [ ] The startup log line records whether the admin listener is enabled and its address. The file's concurrency comment names the profile pointer alongside the arrival counter.
- [ ] The `SLEEP_MS`/`FAIL_RATE` env path is untouched; the bench compose and root compose are not edited.
- [ ] Tests, Red-first, through `httptest` and HTTP-visible behaviour only: env parsing table (unset/empty/true/false/invalid); method and unknown-field and range rejection; omitted-field-keeps; a set profile changes request latency and status on the request handler; jitter bounds respected and never negative; control paths still bypass; `POST` body accepted on payload paths; concurrent set-while-serving under `-race`.
- [ ] `make test`, `make test-race`, `go vet ./...`, `make fmt` clean.
