# 03: S3.T9 — Chaos Test: Circuit Breaker Trip and Half-Open Recovery

**What to build:** Three chaos-scenario arcs verifying the circuit-gate
axis end-to-end against the full Sprint 3 assembly: the happy-path
recovery (trip → cooldown → half-open trial succeeds → closed), the
trial-failure branch (half-open trial fails → back to Open), and the
"lazy, timer-free" promotion property from ADR-0011 decision 6 (no
premature Half-Open transition without a real request driving the
read-time check). Plus a shell-scripted docker-compose smoke closing
MILESTONES.md's literal "injecting 500s on one backend eventually opens
its circuit" wording end-to-end. Reuses S3.T8's `test/chaos/` harness
(`assemble()` helper, `flippableBackend`, gauge/log assertion helpers,
`require.Eventually` timing discipline) — no new harness code.

**Blocked by:** 02 (S3.T8 lands the shared chaos harness)

**Status:** ready-for-agent

### T9 arc (iv) — happy path: trip → cooldown → half-open success → closed

- [ ] Baseline: backend X healthy, `Serve200()`; `lb_circuit_state
      {backend=X,state="closed"}=1`; captured handler holds no
      `event=circuit.transition` records for X
- [ ] `Serve500()` on backend X; blast at least
      `circuitFailuresBeforeOpen` requests through the proxy → assert
      `lb_circuit_state{backend=X,state="open"}=1`; exactly one
      `event=circuit.transition, backend=X, reason=<open vocab>` record
      via field-allowlist match against S3.T5.2's frozen strings
- [ ] One request during cooldown → 503 with the `Registry.Allow`
      denial path (no `IncActive` accounting per ADR-0012)
- [ ] Wait through `circuitCooldown` (via `require.Eventually` on the
      subsequent trial outcome, not a fixed sleep); `Serve200()` on X
- [ ] Send exactly one request → admitted as the half-open trial → 200
      → circuit closes; assert `lb_circuit_state{backend=X,state="closed"}=1`;
      transition records fire for open→half-open and half-open→closed
      with the frozen `reason` strings

### T9 arc (v) — trial failure reopens

- [ ] Set up through Open exactly as in arc (iv), but leave backend X
      at `Serve500()` through cooldown
- [ ] Wait through `circuitCooldown`; send exactly one request →
      admitted as half-open trial → 500 → circuit back to Open
- [ ] Assert `lb_circuit_state{backend=X,state="open"}=1`; no
      `state="closed"` transition record fires; the second Open
      transition is a real state event, not a duplicate of the first

### T9 arc (vi) — zero traffic during cooldown

- [ ] Trip the circuit as in arc (iv)
- [ ] During `circuitCooldown`, send **no** requests to the proxy; use
      `require.Never` (or a bounded-time non-occurrence assertion) to
      confirm no `state="half_open"` transition record fires and the
      gauge stays at `state="open"=1` for the entire window
- [ ] After cooldown + margin, send exactly one request → the
      Open→Half-Open transition fires **on that request**, mechanically
      proving the "lazy, timer-free" property of ADR-0011 decision 6

### Docker-compose smoke

- [ ] `deployments/docker/chaos/circuit.sh` added alongside
      `eviction.sh` (from S3.T8); shared `README.md` extended with the
      circuit scenario section
- [ ] `circuit.sh` starts with `set -euo pipefail`, passes `bash -n`
      and `shellcheck`
- [ ] LB runs as host process; backends from the existing S1.T9
      compose — no new compose file
- [ ] Failure injection flips one backend's `FAIL_RATE` to `1` (via
      `docker exec` environment override, or a `docker compose up -d`
      restart with the override env var per the existing
      per-backend `${BACKEND_A_FAIL_RATE:-…}` scheme in the S1.T9
      compose)
- [ ] Assertions via the LB's Prometheus HTTP API on `metrics.listen`
      — S3.T7 precedent: blast requests until Prometheus reports
      `lb_circuit_state{backend="backend-a",state="open"}=1`, restore
      the backend to `FAIL_RATE=0`, then assert eventual recovery to
      `lb_circuit_state{backend="backend-a",state="closed"}=1`
- [ ] `README.md` section documents the exact `docker exec` invocation
      (or restart-with-env recipe), the Prometheus query strings, and
      the expected result at each step

### Close-out

- [ ] `make test`, `make test-race`, `go vet`, `make fmt` clean
- [ ] End-to-end `docker compose up` execution is
      `[MANUAL VERIFICATION PENDING]` per S3.T7 precedent
- [ ] `PROGRESS.md`: this ticket added, flipped to `[DONE]` on
      completion, **and Sprint 3 status line flipped to "Sprint 3
      complete"** in the same atomic commit — provided no deviation
      surfaced during T8/T9 that warrants a lazy S3.T10 close-out; if
      one did, the session log names it and T9's completion commit
      leaves Sprint 3 status at "in progress" so the next orient step
      raises the close-out ticket
- [ ] Session log entry appended to `docs/sessions/<YYYY-MM-DD>-<agent>.md`
      including any observed deviations (or explicit "no deviations
      surfaced" if that is the case, so the lazy S3.T10 decision is
      recorded)

## Comments
