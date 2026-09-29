# 03: Nearest-equivalent competitors (S5.T5.2)

**What to build:** Every LB algorithm now runs head-to-head against Nginx. consistent-hash and p2c-ewma gain **nearest-equivalent** Nginx competitors, each stating its gap. The harness drops the solo-algorithm path, so the core slice grows from 36 to 48 runs. Every result records whether its comparison was **matched** or **nearest-equivalent**. All Nginx competitors also get the fixed worker count that the CPU pinning (ticket 08) relies on. TDD-exempt.

**Blocked by:** 02

**Status:** ready-for-agent

Spec: `../spec.md` (Competitors; Fairness). Glossary: **Competitor**, **Nearest-equivalent**, **Hot key**.

- [ ] h2 consistent-hash competitor: Nginx's consistent `hash` on the remote address (the same hash key as the LB). Its header comment states the gap: ketama with no bounded loads.
- [ ] h2 p2c-ewma competitor: Nginx's `random two least_conn`. Its header comment states the gap: two-choice over active connections with no latency signal.
- [ ] Apart from the upstream algorithm, both are identical to the h2 round-robin competitor: TLS listener, HTTPS upstreams with verification off, and the same keepalive pool.
- [ ] `worker_processes 2` in **all five** competitor configs, replacing `auto`, each with a one-line comment tying it to the competitors' cpuset (cores 0–1). Why it's hardcoded: `auto` counts online CPUs and ignores the cpuset. **Do not parameterise it.**
- [ ] The harness's protocol/algorithm → competitor mapping is a path join, and the solo-algorithm list and code path are removed.
- [ ] Every result file's header line records `comparison=matched` (round-robin, least-connections) or `comparison=nearest-equivalent` (consistent-hash, p2c-ewma).
- [ ] Harness header comments, usage text and the bench README slice table state core = 48 runs and `all` = 62 at this point.
- [ ] Verified: `nginx -t` on all five configs; `shellcheck -S style` and `bash -n`; a reduced-constant local run of the core slice for the two new algorithms, which produces labelled Nginx results (noted in the session log).
