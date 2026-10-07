# 02: S5.T20.1 — LB reload admin endpoint and last-reload status

**What to build:** a way to attempt a reload over HTTP and to read the result of the last reload attempt, both on the LB's internal metrics listener, sharing one code path with SIGHUP.

Spec: `../spec.md` — D2, D9, D10; gap report §2 (D2 end-to-end verification).

**Blocked by:** 01.

**Status:** ready-for-agent

- [ ] ADR written first: amends ADR-0015 (a second reload trigger; all reloads serialized; the file is not authoritative after a body-applied reload until the next SIGHUP) and ADR-0013 (the metrics listener gains admin routes; not a production control plane).
- [ ] `config.Parse(r io.Reader, source string)` holds the strict decode and `${VAR}` expansion; `config.Load` opens the file and calls it. Error format for files unchanged (`config: load <path>: …`).
- [ ] One app-level operation parses, validates and applies a config, serialized against every other reload, and records the outcome. The SIGHUP loop and the endpoint both call it; the parse/validation failure logging moves with it unchanged (same `event`/`reason`).
- [ ] `POST /admin/reload` on the metrics listener: body is YAML (size-capped); `200` with the recorded status on success; `422` with the error text on any rejection (parse, validation, non-backend change, apply); `405` for other methods; `413` over the cap.
- [ ] `GET /admin/reload/last`: `{"result":"none"}` before any attempt, else `{"result":"success"|"rejected","time":RFC3339,"error":"…"}`.
- [ ] A rejected attempt leaves the loaded config and the registry unchanged.
- [ ] A reload applied through the endpoint uses the application's run context (probers and drains outlive the HTTP request).
- [ ] Tests, Red-first: unknown algorithm, duplicate backend name, malformed URL, unparseable YAML and a non-backend change each return `422` with a readable message and leave the registry unchanged; a valid body returns `200` and changes the registry; `last` reflects each attempt; SIGHUP-path tests still pass; a race test drives endpoint and SIGHUP reloads concurrently.
- [ ] Verification (D9): with the demo stack rebuilt, posting an unknown-algorithm body to `lb-roundrobin`'s metrics listener from inside the demo network returns `422` naming the algorithm, `last` shows `rejected`, and the generators see no errors.
- [ ] `make test`, `make test-race`, `go vet ./...`, `make fmt` clean.
