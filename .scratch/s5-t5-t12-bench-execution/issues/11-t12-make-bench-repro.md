# 11: `make bench-repro` — one-command reproducer (S5.T12)

**What to build:** One command takes a stranger from a fresh `git clone` to the full result set. It refuses to start under any condition that would make the results unreproducible or skewed. This is the Sprint 5 exit criterion in executable form, and the published run (19) is produced by it. It is a benchmark reproducer, not a deployment, so ADR-0019 stands. TDD-exempt.

**Blocked by:** 06 (the bench stack's backend instrumentation is complete), 10 (the harness records provenance)

**Status:** ready-for-agent

Spec: `../spec.md` (Reproducer)

- [ ] `make bench-repro` runs these steps in order and stops at the first failure:
  1. **Preflight**. Each check prints what's wrong and how to fix it:
     - Docker, Compose v2, openssl and make are present;
     - Docker reports ≥ 8 vCPUs;
     - **no container is running at all**. Otherwise it prints each one's ID and image, and it **never stops containers itself**;
     - the working tree is clean, untracked files included. Otherwise it prints the dirty files.
  2. Generate the certificates.
  3. Build the bench images.
  4. Run the smoke slice.
  5. Run the full matrix.
- [ ] `make help` lists the target with a one-line description that includes the expected duration (hours).
- [ ] The bench README gains a "Reproducing the published numbers" section: prerequisites (Docker Desktop with ≥ 8 vCPUs assigned, openssl, make), the one command, and the expected duration (about 2–2.5 h on Docker Desktop).
- [ ] Verified, with each result recorded in the session log:
  - each refusal fires: a tracked file touched; any container running; a vCPU threshold raised locally, uncommitted, to simulate too few;
  - from a fresh clone of HEAD in the scratchpad, `make bench-repro` gets through a green smoke. Stopping there is acceptable; the full run is ticket 19.
