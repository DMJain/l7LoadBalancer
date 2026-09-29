# 12: Hot-key distribution on consistent-hash runs (S5.T8.1)

**What to build:** Every consistent-hash result shows where its requests went. The load generator has one address, and the hash key is the client address, so every consistent-hash run is a **hot key** scenario. The per-backend distribution is what shows bounded loads working: Nginx should pin everything to the owner, while the LB spills once in-flight load passes the bound. TDD-exempt.

**Blocked by:** 11

**Status:** ready-for-agent

Spec: `../spec.md` (Degraded slice → distribution; Hot key). Glossary: **Hot key**, **Hash key**, **Load**, **Capacity**.

- [ ] Before and after every measured consistent-hash run in the core slice (both competitors, all sizes, peak and latency), the harness reads each backend's arrival counter from inside the compose network and records the per-backend deltas, as counts and shares, in the result file.
- [ ] Each consistent-hash result names the **hot-key owner** (the plurality backend) and records whether spill occurred (more than one backend received traffic).
- [ ] The reads happen outside the measured window, so they add no load during measurement.
- [ ] The helper that reads and diffs the counters is reusable by the degraded slice (13) and the drain reload (15).
- [ ] Verified: `shellcheck -S style` and `bash -n` pass; a green smoke; a reduced-constant local core run for consistent-hash showing Nginx at about 100% on one backend, and the LB's distribution and spill flag, recorded in the session log.
