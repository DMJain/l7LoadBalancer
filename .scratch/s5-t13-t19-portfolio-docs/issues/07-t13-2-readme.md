# 07: S5.T13.2 — README rewrite

**What to build:** a README a reviewer can read in about five minutes: what the project is, how a request flows, how to run it and the local demo, what was measured and how far to trust it, and where to read more.

Spec: `../spec.md` — *README*, *Writing rules*, *Approved headline claims*.

**Blocked by:** 05 (S5.T15) and 06 (S5.T13.1). The README links the two new documents and reuses the request-path diagram, so all three must exist first.

**Status:** ready-for-agent

## In scope / out of scope (AGENTS.md Step 2.5)

- **In scope:** the README only.
- **Out of scope:** the demo video and its link (S5.T17.2 fills the marked slot); the AI-disclosure line (the owner writes it into its marked slot); any claim not approved; fixing the results document; images that go stale.

## Acceptance criteria

- [ ] About 120–150 lines, in this order: three-sentence summary; request-path diagram; quickstart; local live demo; headline results; dependency line; marked AI-disclosure slot; links; licence.
- [ ] The three-sentence summary claims only what the repository demonstrates, and each algorithm or concurrency claim in it is backed by the design-decisions document.
- [ ] Quickstart commands are the real make targets and were executed once during this ticket (the full fresh-clone run happens in the release-verification ticket).
- [ ] The demo section says "local live demo", gives the one-command start and the acceptance check, points to the demo script, holds the marked video slot, and cites ADR-0023 for why there is no public URL.
- [ ] Headline results use only C1–C7 as approved, with C4's revised wording and C3's README wording ("the removed backend received no further requests after the reload applied", with no raw arrival count). C6 appears as an ordinal claim with the table of peaks and the search step per size, the caveat "single run, ordinal claim only", no ratio and no "noise" wording. No round-robin small-response comparison over HTTP/2 is implied; the HTTP/1.1 comparison is labelled HTTP/1.1 with the HTTP/2 gap named in the same sentence.
- [ ] The degraded-backend result is described as unreliable, cause UNVERIFIED, no numbers.
- [ ] Wherever the README links the results document, one sentence says the "Time to detection" column there measures the width of the error window.
- [ ] Every number in the README has its unit defined in the README (a quoted p99 means p99 is defined there; req/s is defined there).
- [ ] The dependency line reads exactly: "Built on `net/http/httputil`. Third-party: `x/net/http2` (h2c and HTTP/2 to backends), `client_golang` (metrics), `yaml.v3` (config)." The phrase "stdlib request path" does not appear.
- [ ] Marked slots present and empty: the demo video link and the AI-assisted development disclosure.
- [ ] Links to the design-decisions document, the retrospective, the architecture document, the ADR index and the results document all resolve, and the MIT licence is stated.
- [ ] Writing rules hold: each term defined at first use in the README, and only the terms it uses; no ticket IDs; no unexplained "slice", "cell", "core slice" or "bisect"; no marketing words; the audience is never announced; neutral voice.
- [ ] The reader check is done and logged: every term that needed a definition and where it is defined.
- [ ] Docs-only (TDD-exempt). The claim commit and the work commit are each shown to the owner for approval first.
