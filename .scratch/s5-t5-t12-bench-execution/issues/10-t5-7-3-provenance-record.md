# 10: Provenance record (S5.T5.7.3)

**What to build:** Every harness invocation leaves a machine-readable record of everything a reader needs to interpret or reproduce its numbers: which commit, whether the tree was dirty, what hardware, which versions, which core split, and how long it took. The results generator (16) turns this record into the methodology section, and the reproducer and generator use `git_dirty` to refuse unreproducible results. TDD-exempt.

**Blocked by:** 09

**Status:** ready-for-agent

Spec: `../spec.md` (Provenance)

- [ ] A JSON provenance record at the root of the results directory, written when an invocation starts and updated when it finishes.
- [ ] Fields:
  - `git_sha` (string) and `git_dirty` (boolean): **separate fields, never a `-dirty` suffix**;
  - the slices run;
  - start and finish timestamps (UTC, ISO-8601);
  - host OS;
  - host CPU model, read on the host (macOS and Linux both handled);
  - Docker and Compose versions;
  - Docker's vCPU count and memory;
  - the cpuset per service;
  - the LB's Go version and GOMAXPROCS, from its startup line;
  - Nginx's version and worker count;
  - vegeta's version.
- [ ] The harness does **not** refuse a dirty tree or running containers. It's a development tool, and refusal belongs to the reproducer (11) and the generator (16). It only records the fact.
- [ ] The bench README documents the record and each field.
- [ ] Verified: `shellcheck -S style` and `bash -n` pass; after `./bench/run.sh smoke`, the record parses as JSON, contains every field with a plausible value, and `git_dirty` is correct in both a clean and a dirty tree. The record is shown in the session log.
