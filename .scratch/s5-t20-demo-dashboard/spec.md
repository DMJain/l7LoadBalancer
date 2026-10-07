# S5.T20 — Demo dashboard, reload demos, benchmark close

Bundle spec. The analysis behind it is `docs/demo/dashboard-gap-report.md` (gap by gap, with file citations); this spec records the owner's decisions on that report (2026-10-07) and is the authoritative scope boundary for the tickets below.

## Goal

Make the demo dashboard specified in the video brief (Addendum 3) recordable: a state header that shows the active algorithm and the last reload result, an "attempt reload with invalid config" control backed by a real load-balancer endpoint, the zone A/B panels in a demo-only dashboard, an EWMA gauge, and a benchmark summary page whose every row is filled from a clean full-matrix run.

## Decisions (owner, 2026-10-07)

- **D1. Demo B is a traffic switch, not a reload.** ADR-0015 decision 4 stands. Switching algorithm moves the eight generators to another of the four running LB processes. Script wording: "switch to the P2C load balancer", never "reload the algorithm". S2 (last reload) does not change during Demo B.
- **D2. The reload admin endpoint accepts a YAML body.** It lives only on the LB's internal metrics listener. After a body-applied reload the config file on disk is not authoritative until the next SIGHUP. It is a demo and test affordance, not a production control plane. Recorded as an amendment to ADR-0015 and ADR-0013.
- **D3. Benchmark summary table:** no Nginx row (Nginx stays in the spoken HTTP/1.1 sentence). The round-robin 10 KiB row is filled by S5.T20.6, then the **full** matrix is re-run so provenance stays a single clean commit. Title: "L7 Load Balancer — 4 algorithms, 10 KiB responses, 4 backends, HTTP/2, 10-second runs at each peak rate".
- **D4. New panels live in a demo-only dashboard file.** The shared dashboard JSON is unchanged. Recorded as an amendment to ADR-0023 decision 9.
- **D5. C2 has no reset button.** Totals use `increase(...[$__range])`.
- **D6. Stop is the abrupt kill.** The control page labels Kill/Revive as "Stop"/"Start"; narration says "stop". Kill semantics (ADR-0025 amendment decision 9) are unchanged.
- **D7. Latency injection is 200 ms**, not 500 ms (verified by `demo/acceptance.sh`; 500 ms reads near 1 s at p99 because of the 0.5–1 s histogram bucket).
- **D8. Order:** S5.T20.1 → S5.T20.2 → S5.T20.4 → S5.T18 → S5.T20.5 → S5.T20.6. **S5.T20.3 (client-side generator metrics) is deferred**; the LB-side C1–C3 approximations are accepted for the demo.
- **D9. Verification gate.** After each ticket, the dry-run checklist rows (`docs/demo/dashboard-gap-report.md` §9) that depend on it are run against a live `demo/` stack. A failed row stops the bundle.
- **D10. Review stop.** The owner reviews S5.T20.1 before S5.T20.2 starts.

## In scope

The six tickets below, each per its issue file.

## Out of scope

- Algorithm hot reload (ADR-0015 decision 4 stands, D1).
- S5.T20.3 client-side generator metrics (deferred, D8).
- A draining (yellow) health state and its gauge.
- Reload-outcome counters (`lb_config_reloads_total{result}`, still the Sprint-5-deferred proposal); S5.T20.1 exposes the last result over HTTP only.
- Authentication on the admin endpoint (ADR-0005: security hardening out of scope; the listener is never published).
- Any change to the root stack, the bench rig's behaviour (other than S5.T20.6's cold-start guard), or the shared dashboard JSON.

## Tickets

| Issue | ID | Title | Blocked by |
|---|---|---|---|
| 01 | S5.D3 | Tracking amendment | — |
| 02 | S5.T20.1 | LB reload admin endpoint and last-reload status | S5.D3 |
| 03 | S5.T20.2 | Control page: state header, invalid-reload button, controls, `/metrics` | S5.T20.1 |
| 04 | S5.T20.4 | Demo-only dashboard: zone A and B panels | S5.T20.2 |
| 05 | S5.T18 | `lb_p2c_ewma_seconds{backend}` gauge | S5.T20.4 |
| 06 | S5.T20.5 | Benchmark summary page | S5.T18 |
| 07 | S5.T20.6 | Cold-start guard, then full-matrix re-run | S5.T20.5 |

## Addendum (owner, 2026-10-07): algorithm hot reload supersedes D1

The owner rejected D1's framing: production load balancers apply an algorithm change on reload without touching client requests, and the demo must show that rather than moving traffic between processes. S5.T20.7 makes `algorithm` reloadable (ADR-0015 amendment). The dashboard (S5.T20.2, S5.T20.4) stays; the algorithm buttons are to be removed and the demo reshaped around one hot-reloaded LB in a follow-up ticket after the owner reviews S5.T20.7.
