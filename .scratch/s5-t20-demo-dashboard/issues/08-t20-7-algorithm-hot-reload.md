# 08: S5.T20.7 — Algorithm hot reload

**What to build:** a reload (SIGHUP or `POST /admin/reload`) that changes `algorithm` is applied in place instead of rejected: the new selector is built, swapped in atomically, and serves the next request; requests already in flight are untouched; the process never restarts.

Spec: `../spec.md` — owner decision of 2026-10-07 superseding D1 (see the spec's addendum). Production load balancers accept an algorithm change on reload (Nginx and HAProxy by worker or process replacement, Envoy in process); this project swaps in process.

**Blocked by:** 02 (the shared reload path).

**Status:** ready-for-agent

- [ ] ADR first: ADR-0015 amendment making `algorithm` reloadable (every other non-backend field still rejected; an unknown algorithm still rejected by validation); ADR-0013 amendment for the algorithm gauge.
- [ ] `config.NonBackendChanges` no longer names `algorithm`.
- [ ] The proxy reads its selector through one atomic pointer; a swap is one store; a request selects once, so in-flight requests keep their backend.
- [ ] `App.Reload`: build the new selector first (a failure rejects the whole reload, nothing applied), apply the backend diff, swap the selector, record the config. The `config_reloaded` line names the old and new algorithm when it changed.
- [ ] `lb_algorithm_info{algorithm}` gauge: 1 for the current algorithm, seeded at build, moved on reload.
- [ ] Tests, Red-first: diff no longer names `algorithm`; a reload from round-robin to consistent-hash changes routing observably and logs both names; algorithm and backend changes in one reload; unknown algorithm still rejected; gauge moves; a zero-drop test flips the algorithm many times under concurrent traffic with no failed request; concurrent selector swap and requests clean under `-race`.
- [ ] Existing tests that used an algorithm change as their rejected example use another non-backend field.
- [ ] Docs that say the algorithm is not reloadable are corrected.
- [ ] Verification on the live demo stack: edit `demo/configs/roundrobin.yaml` to `p2c_ewma`, SIGHUP `lb-roundrobin` under traffic: reload SUCCESS, traffic moves off a slow backend, generators report zero errors.
- [ ] `make test`, `make test-race`, `go vet ./...`, `make fmt` clean.
