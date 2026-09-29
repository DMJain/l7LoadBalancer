# 04: Smoke slice (S5.T5.3)

**What to build:** `run.sh smoke` proves, in about a minute, that every (protocol, algorithm, competitor) combination the full matrix uses comes up and serves 200s through the harness. This is the class of routing bug that hit issue 05, where TLS upstreams returned 400. It's the gate every later ticket verifies against, and it is the reproducer's preflight run. TDD-exempt.

**Blocked by:** 03

**Status:** ready-for-agent

Spec: `../spec.md` (Competitors — smoke slice)

- [ ] The combinations covered are exactly those the matrix uses: h2 × {roundrobin, leastconn, consistent-hash, p2c-ewma} and HTTP/1.1 × roundrobin, each against both competitors (10 attacks).
- [ ] Each attack is 2 seconds at 50 req/s against the 10 KiB endpoint. Any success rate below 100% fails the slice with a nonzero exit that names the combination.
- [ ] `smoke` is **not** part of `all`. The usage text and bench README list it separately.
- [ ] Smoke parameters are constants at the top of the harness, as all other parameters are. No flags.
- [ ] Verified: a green `./bench/run.sh smoke`, output recorded in the session log. Also a negative check: point one combination at a deliberately broken local, uncommitted Nginx config and show the slice fails naming it.
