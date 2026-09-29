# 16: Results generator — core, protocol, methodology (S5.T10.1)

**What to build:** A results-generator script turns raw harness output into the published tables and methodology section, so no number is ever typed by hand. It writes only inside marker regions of one results document, leaving hand-written narrative untouched. It refuses output recorded from a dirty tree. It's built and tested against a committed fixture, so it doesn't wait for the published run. TDD-exempt (shell), but with a committed fixture test.

**Blocked by:** 10 (the provenance record's format)

**Status:** ready-for-agent

Spec: `../spec.md` (Results generator and document; Testing Decisions)

- [ ] The script takes an optional results directory and document path, defaulting to the real ones. This is the test seam.
- [ ] It writes only between `<!-- BEGIN GENERATED: <section> -->` and `<!-- END GENERATED -->` markers. Everything outside the markers is preserved byte-for-byte.
- [ ] It creates the results document with its marker skeleton if the document doesn't exist.
- [ ] **Core and protocol tables**: rows for algorithm, size, competitor and `comparison` label; columns p50, p90, p95, p99, **p99.9** and max, plus peak throughput. p99.9 is read from the `.hdr` percentile rows and the rest from the JSON reports.
- [ ] **Methodology section**, generated from the provenance record: hardware, versions, the cpuset sentence, the backend-logging-off statement, the Nginx worker count and LB GOMAXPROCS as observed, and the measured wall-clock time.
- [ ] It exits nonzero if the provenance record has `git_dirty: true`.
- [ ] Deterministic output: a second run produces no diff.
- [ ] A small committed fixture result set (core and protocol result files, `.hdr` files and a provenance record) plus a committed shell test script that runs the generator against it and asserts:
  - the tables and p99.9 values are as expected;
  - a second run produces no diff;
  - narrative outside the markers survives;
  - `git_dirty: true` is refused.
- [ ] Verified: `shellcheck -S style` and `bash -n` pass on the script and the test, and the test passes.
