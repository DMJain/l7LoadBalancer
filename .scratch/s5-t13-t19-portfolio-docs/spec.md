# Sprint 5 Spec: Portfolio Documentation and v0.1.0 (S5.T13–T15, S5.T19)

Status: ready-for-agent

Synthesised from the 2026-10-02 grilling session. It promotes the proposed S5.T13–T15 and adds S5.T19.1 and S5.T19.2. The owner's pre-grilling titles (S5.T18 README, S5.T19 design-decisions, S5.T20 what-I'd-do-differently, S5.T21 final pass) are renumbered: T13–T15 already existed as proposals, and S5.T18 is the proposed P2C-EWMA latency gauge, which stays proposed. The owner chose S5.T19 for the final pass because T19 and T20 were unused.

---

## Problem Statement

A reviewer who opens the repository finds a working system and no explanation of it.

- The README is 30 lines. It has no diagram, no account of what was measured, and no pointer to the local live demo.
- The reasoning behind the major decisions is spread across 25 ADRs. No single document makes the argument end to end.
- The project's known weaknesses are recorded only in tracking files: a latency estimate with no decay, a benchmark run that was noisy and incomplete, and a degraded-backend scenario that did not isolate the slow backend. A reviewer would not find them.
- The published benchmark numbers are rig-limited and uneven. A careless summary would overstate them, for example by implying a round-robin small-response comparison against Nginx over HTTP/2 that was never produced.
- The repository has never had a hygiene pass. It has an untracked personal directory, a stale Go version in the agent guidance, a stale agent-written audit document, seven commits carrying AI-attribution trailers, and no release tag.
- The owner's reader is not an expert. A fresh CS graduate knows HTTP, servers, threads and requests, but not consistent hashing, EWMA, percentiles, circuit breakers or this project's vocabulary. A senior reviewer reads the same text, so simplicity must never cost precision.

## Solution

Three documents, a diagram set, and a release, delivered in this order:

1. A design-decisions document that argues seven topics, each with its ADRs and evidence.
2. A retrospective document that lists the project's weaknesses, each with a checkable source.
3. A rewritten README, plus request-path and package-graph diagrams, linking the two documents above.
4. A repository hygiene pass that reports findings and rewrites no history.
5. An annotated `v0.1.0` tag, created only when a real recorded demo video is linked and the owner confirms the message.

Every number shipped in a document traces to an owner-approved claim. All reader-facing text follows one set of writing rules, and each document is re-read as the target reader before it is marked done.

## User Stories

1. As a reviewer opening the repository, I want the README to say in three sentences what the project is and what it demonstrates, so that I know within a minute whether to keep reading.
2. As a reviewer, I want a diagram of the request path in the README, so that I can see how a request flows before reading prose.
3. As a reviewer, I want a package-dependency diagram in the architecture document, derived from the real code, so that I can trust it matches what is built.
4. As a new engineer, I want quickstart commands that I can run as written, so that I can build and run the load balancer without guessing.
5. As a reviewer, I want a section on the local live demo with its one-command start and its acceptance check, so that I can run it myself.
6. As a reviewer, I want the README to say why there is no public URL and cite ADR-0023, so that I do not think a deployment is missing.
7. As a reviewer, I want a real recorded demo video linked from the README, so that I can watch the system without running it.
8. As a reviewer, I want the headline benchmark results stated with their limits, so that I can judge how far to trust them.
9. As a reviewer, I want it said plainly that the published run was noisy and incomplete (67 of 71 result sets), so that I do not discover that on my own.
10. As a reviewer, I want the degraded-backend scenario described as unreliable, with its suspected cause marked unverified, so that I am not told an unproven explanation as fact.
11. As a reviewer, I want the README not to imply a round-robin small-response comparison against Nginx over HTTP/2, so that I am not misled about a comparison that was skipped.
12. As a reviewer, I want the HTTP/1.1 round-robin comparison cited and labelled HTTP/1.1, with the missing HTTP/2 comparison named in the same sentence, so that I know exactly what exists.
13. As a reviewer, I want the Nginx comparison stated as an ordinal result with a table of peaks and the search step size per response size, so that I can check that each gap exceeds the measurement resolution.
14. As a reviewer, I want reload results stated as "the removed backend received no further requests after the reload applied", so that I understand the guarantee without reading raw counts.
15. As a reviewer, I want the backend-stop result to say what was done, at what request rate, with what retry policy and what happened, so that the number has context.
16. As a reviewer, I want the misleading "time to detection" column explained wherever the results document is linked, so that I do not misread the error-window width as a detection delay.
17. As a reviewer, I want hot-key behaviour reported without overreaching, so that I see the bounded ring spill and the unbounded hash not spill, and understand why one cell showed no spill.
18. As a reader, I want a design-decisions document with seven topics, so that I can follow the argument in one place instead of 25 ADRs.
19. As a reader, I want each decision explained as the problem, the options, the choice and its cost, with the why first, so that I can judge the reasoning and not only the outcome.
20. As a reader, I want one small worked example with real numbers for each algorithm, so that I understand how each behaves.
21. As a reader, I want the no-decay limitation of the latency estimate stated directly in the P2C section, so that I am not left to discover the starvation risk.
22. As a reviewer, I want the concurrency model explained with each primitive cited to the code, so that I can verify every claim about atomics, channels and mutexes.
23. As a reviewer, I want a retrospective that names the project's weaknesses and what the project would do instead, each with a source, so that I can see honest self-assessment backed by evidence.
24. As a reviewer, I want the retrospective to state facts about the seven attribution-trailer commits and the missing commit-message hook, so that I see the cause without a recommendation to rewrite history.
25. As the owner, I want marked slots in the retrospective and the README for my own words, so that nothing is written in my voice without my review.
26. As the owner, I want the README dependency line written in my exact wording, so that third-party dependencies are named accurately and the request path is not called standard-library-only.
27. As the owner, I want a hygiene pass that reports findings and applies only the safe fixes, so that nothing destructive happens without my say-so.
28. As the owner, I want the tree and the full history scanned for secrets, absolute local paths and employer references, so that I know what the public repository exposes.
29. As the owner, I want the personal resume directory kept out of the repository, so that job-search material is not published.
30. As the owner, I want the stale audit document moved to the history area with a superseded header, so that the process stays visible without reading as current.
31. As the owner, I want the Go version in the agent guidance corrected to match the module, so that the repository does not contradict itself.
32. As the owner, I want the README quickstart run from a fresh clone, including the demo and its acceptance check, so that the instructions are proven to work.
33. As the owner, I want a dead-link check and a placeholder scan, so that no broken link or leftover marker ships.
34. As the owner, I want every shipped number traced to an approved claim, so that no figure escapes review.
35. As the owner, I want the v0.1.0 tag blocked until the demo video link is real, so that the release is not tagged half-finished.
36. As the owner, I want the tag message shown to me and any push to need a separate confirmation, so that nothing outward-facing happens by default.
37. As a fresh graduate reading any of these documents, I want each technical term defined once, in one plain sentence, where it is first used, so that I never have to leave the page to follow the text.
38. As a senior reader, I want every number to be exact and carry a unit and a meaning, so that plain language never costs me precision.
39. As an agent working on the repository, I want the new glossary terms Matched, Peak and Rig-limited defined, so that the vocabulary is consistent across documents.

## Implementation Decisions

### Scope and numbering

- The bundle is S5.D2 (tracking amendment), S5.T14, S5.T15, S5.T13, S5.T19.1, S5.T19.2, strictly serial in that order. S5.T19.2 is also blocked by S5.T17.2, the owner-recorded demo video.
- Only T13–T15 and the final pass are promoted. S5.T18 (the P2C-EWMA latency gauge) and the other existing proposals stay proposed.
- One new proposal is recorded and not built: the "Time to detection" column in the published results measures the width of the error window (last failed request minus first), not the delay before detection. A fix changes the benchmark scripts and regenerates the results, so it needs its own ticket.
- S5.D2 also amends S5.T17.2 so that the owner fills the README's marked video slot.
- The README is written after the design-decisions and retrospective documents so it never links to a document that does not yet exist.
- The bundle's spec, its issue files and the three new glossary entries land in one commit, following the precedent of earlier spec commits that carried their glossary changes. S5.D2 is then claimed and executed on its own.
- The owner approves every commit message, and no ticket is marked in-progress, before the owner has reviewed the bundle.

### Writing rules (all reader-facing documents: README, design-decisions, retrospective, architecture, and the new glossary entries)

- Existing ADRs are not rewritten.
- The reader is a fresh CS graduate who knows HTTP, servers, threads and requests, and does not know Go internals, consistent hashing, EWMA, percentiles, circuit breakers or project vocabulary. A senior reviewer reads the same text.
- Define each technical term in one plain sentence the first time it appears in that document. A document defines only the terms it uses. Terms covered where used: Layer 7, reverse proxy, p50/p99/p99.9, req/s, EWMA, P2C, consistent hashing, bounded loads, hot key, circuit breaker and its three states, health check (active versus passive), drain, SIGHUP and reload, h2c, ALPN, HTTP/2 multiplexing, cold start, rig-limited, peak.
- Any number quoted in the README has its unit defined in the README.
- No ticket IDs. No "slice", "cell", "core slice" or "bisect" unless explained in plain words first. ADR numbers are fine as links.
- Short sentences, one idea each, active voice, under about 20 words on average.
- For each design decision: the problem in plain words, the options, the choice, what it costs, with the why before the how.
- Each algorithm gets one small worked example with real numbers.
- Every number carries a unit and a plain-words meaning. Every number and claim stays exact.
- No marketing words. Never announce the audience. The tone is one engineer explaining to another.
- Neutral voice ("the project"). Owner-voice content is left as marked empty slots.
- Acceptance check for each document: before marking it done, re-read it as the target reader and list in the session log every term that needed a definition and where it is defined. A term used before its definition fails.

### Approved headline claims

Every number in a shipped document traces to one of these. Anything else needs owner approval first.

- **C1.** One uninterrupted benchmark-reproduction run from clean commit `8c2b7d4` produced 67 of 71 result sets and was flagged noisy. Two scenarios, round-robin through the load balancer over HTTP/2 at 200 B and at 10 KiB responses, were skipped.
- **C2.** Rig: Apple M3, 8 vCPUs and 8 GiB assigned to Docker. The load balancer and Nginx each use two pinned cores, each backend one core, and the load generator two cores. The throughput ceiling is soft because the cores sit inside a virtual machine.
- **C3.** The no-op reload and the drain reload both passed under 60 s of steady HTTP/2 round-robin load, with zero non-2xx responses and zero transport errors. The README says only that the removed backend "received no further requests after the reload applied". The design-decisions document alone carries the raw figures: p99 of 1.752 ms before and 1.762 ms after the no-op reload, 1.856 ms before and 2.140 ms after the drain reload, and the removed backend's arrival count frozen at 95710. One run each, round-robin only.
- **C4.** Stopping one of four backends mid-run (a graceful container stop at t=29 s of a 60 s run) at 1750 req/s (50% of a 3500 req/s peak found in that run, HTTP/2, 10 KiB responses) produced 3 failed requests, all within a 9 ms span starting about 0.7 s after the stop. The load balancer never retries (ADR-0018), so those 3 reached the client. The phrase "time to detection" is not used, because the harness computes it as last error minus first error. The ejection mechanism is cited to ADR-0011 only after the documentation writer verifies it against that ADR. The retry-policy ADR is ADR-0018, not a T14 ADR, because no such ADR exists.
- **C5.** Hot-key behaviour only. In every core Nginx result the unbounded hash put at least 97.97% of requests on one backend, and the load balancer's bounded ring spilled, for example 82.27% on the owner and 17.19% on a second backend at 10 KiB peak load. Nginx and load-balancer rows come from the same algorithm, size and load combination. At 1 MiB and 30% load no spill occurred. This is explained as expected behaviour, never as a measurement: the capacity rule in ADR-0009 floors at one in-flight request, and at roughly 60 req/s with a latency near 8 ms the average concurrency is about 0.5 by Little's law (arrival rate times latency).
- **C6.** Over HTTP/1.1, round-robin, matched comparison, Nginx is ahead at all three response sizes. The claim is ordinal only, from a single run, with no ratio and no "noise" wording. The same sentence says the HTTP/2 round-robin comparison at 200 B and 10 KiB is absent because the load-balancer scenarios were skipped. A table gives the peaks and the search step per size, and every gap exceeds its step: 200 B, Nginx 11500 req/s, load balancer 8000, gap 3500, step 500; 10 KiB, 6000 and 4500, gap 1500, step 500; 1 MiB, 350 and 275, gap 75, step 25.
- **C7.** The degraded-backend scenario is unreliable. Every median sat near the injected 50 ms, which is consistent with the delay reaching all four backends. The suspected cause, an exported environment variable leaking through a shared compose definition, is marked UNVERIFIED wherever it appears. No degraded-scenario numbers are quoted.
- Deliberately not quoted: HTTP/2 core-run peaks, load-balancer-versus-Nginx throughput wins from the HTTP/2 core run, any p99 tail comparison.
- Candidate claims that do not come from the results document (bounded versus unbounded hot-key figures, P2C latency-bias figures, the soak-test result, any code-size figure) are pulled from their tests or ADRs and sent to the owner with their source before any use. One hot-key figure appears as 3,996 in the agent guidance and 4,005 in the test and ADR-0009; the writer resolves it from the test and ADR.

### Design-decisions document (S5.T14): seven topics

Each topic is problem, options, choice, cost, evidence. A closing paragraph points to the ADR index for decisions not covered.

1. **Why `net/http/httputil` rather than a framework or hand-built proxy.** ADR-0005, ADR-0007, the INDEX entries for the reverse-proxy choice and for h2c. Names the three third-party dependencies exactly: the HTTP/2 package, the Prometheus client and the YAML library.
2. **Bounded-loads consistent hashing.** ADR-0008 (ring, 150 virtual nodes, hash pipeline) and ADR-0009 (epsilon 0.25, load metric, capacity formula, fallback). Evidence C5 and any approved N-claim. Includes a worked capacity example.
3. **Power of two choices with EWMA latency.** ADR-0010 (backend-owned latency state, cold start, 2 s failure penalty) and ADR-0023 (no decay, stale state after an LB switch). States the no-decay starvation limit directly. Evidence C7 explains why no live divergence evidence exists. Includes a worked example with one slow backend.
4. **Reload architecture.** ADR-0015 (immutable snapshot swap, backend identity, what is reloadable), ADR-0016 (drain lifecycle and window), ADR-0019 (SIGHUP and the container as deployment target). Evidence C3. Includes a worked drain example with a named window.
5. **Failure-mode interaction.** ADR-0011, ADR-0012, ADR-0013, ADR-0014, ADR-0017, ADR-0018. Evidence C4.
6. **Honest benchmark comparison with Nginx.** ADR-0020, ADR-0021, ADR-0022 and the results document's limitations. Evidence C1, C2, C6, C7.
7. **Concurrency model (atomics, channels, mutexes and why).** The concurrency ownership table in the frozen contracts, ADR-0007, ADR-0010, ADR-0012, ADR-0013, ADR-0015, ADR-0016, and the INDEX entries for the round-robin counter and the least-connections scan. Every primitive claimed is verified by searching the code and cited to a file and line; a primitive with no use site found is not claimed.

### README (S5.T13)

- About 120–150 lines, in this order: three-sentence summary; Mermaid request-path diagram; quickstart; local live demo; headline results; dependency line; marked AI-disclosure slot; links; licence.
- The live-demo section says "local live demo", gives the one-command start and the acceptance check, points to the demo script, holds a marked video-link slot, and cites ADR-0023 for why there is no public URL.
- Headline results use only C1–C7 as approved, with units defined in place. Where it links the results document it says the "Time to detection" column measures the width of the error window.
- The dependency line reads exactly: "Built on `net/http/httputil`. Third-party: `x/net/http2` (h2c and HTTP/2 to backends), `client_golang` (metrics), `yaml.v3` (config)." The phrase "stdlib request path" is not used.
- The AI-disclosure line is written by the owner into its marked slot. The README leaves the slot empty.
- The licence stays MIT as is.

### Architecture document (S5.T13)

- Gains the request-path Mermaid diagram and the package-dependency Mermaid diagram.
- The package graph is derived from `go list`, not copied from the agent guidance. On 2026-10-02 the two agree exactly for all nine packages and the entry point. The writer re-derives it at writing time and reports any difference.
- Two discrepancies found while verifying and carried into the hygiene pass: the agent guidance says "Go 1.22+" while the module says Go 1.25.1, and it describes the request path as standard-library with one exception while the module also depends on the Prometheus client and the YAML library.

### Retrospective (S5.T15)

- Each item states what happened, what it cost, what the project would do instead, and a source (ADR, tracking entry, retro, results section, or commit).
- Required items: no-decay EWMA; no cold-start guard for the first peak-search step; degraded-scenario leak with cause UNVERIFIED; rig-limited benchmarking; stale P2C state after an LB switch; the seven attribution-trailer commits and the missing commit-message hook; the "Time to detection" column; health probes counted as backend arrivals; the narrative number check missing tables at section boundaries.
- The trailer item states facts only: seven commits, all ancestors of `8c2b7d4`, all already pushed, the commit-message hook deferred by project policy. It does not recommend a rewrite. Rewriting would change every later hash and break the provenance in the published results and the retro tables; the owner leans toward accepting them and adds the disclosure line to the README.
- The trailer commits are `25c1952`, `fd5ce03`, `9c25a11`, `c7de908`, `84b7dab`, `7939ade`, `cf68104`.
- Owner-voice items are left as marked empty slots, at least one at the end.

### Hygiene pass (S5.T19.1): full checklist

Report first. The pass applies the safe fixes below and leaves every judgement call to the owner. No history rewrite.

1. Working tree clean apart from the ticket's own changes.
2. `go build ./...` clean, `go vet ./...` clean, formatting produces no diff.
3. `go mod tidy` produces no diff.
4. Tests and race-detector tests green.
5. The personal resume directory is added to `.gitignore`. It was never committed, so no history rewrite is needed.
6. The stale audit document moves to the history area with the header "Snapshot as of 2026-09-19, superseded.", and every link to its old location is updated.
7. The Go version in the agent guidance and in the LSP guidance matches the module's. The two agent-guidance symlinks still resolve.
8. LICENSE is present (MIT, kept as is).
9. The progress file has no in-progress entry other than the ticket's own.
10. Placeholder scan over shipped docs: no TODO, FIXME, "Video pending" or unresolved owner slot, unless the owner names one as allowed.
11. Dead-link check over the README and docs. The script lives in the scratchpad and is committed only if the owner approves it.
12. Every number in the README, design-decisions and retrospective is listed with the claim that approves it; a number with no claim is a finding.
13. Secret scan over the tree and all history. As of 2026-10-02 the current tree has no key-shaped strings.
14. Local-path and employer-reference scan over the tree and all history. As of 2026-10-02 the current tree has none.
15. AI-attribution trailer inventory: seven commits as of 2026-10-02 (five naming Claude Opus 4.6, two naming Claude Sonnet 5). Reported only.
16. Commit-history review, reported only: non-conventional subjects, the two author identities (413 commits and 1 commit), commits that mix concerns.
17. Ship-or-prune decisions recorded by the owner for the planning directory, the OpenCode config file, the agent-tooling docs and the history docs. As of 2026-10-02 the owner has decided only that the history docs ship; a scan of the other three found no secrets, absolute local paths or employer references.
18. Commit `8c2b7d4` resolves, and the results document and the provenance record still name it. The 4 h 16 min benchmark reproduction is not re-run; the README states which commit the numbers come from.
19. The README quickstart is executed from a fresh clone: build, run, `make demo-up`, the demo acceptance check passing, then teardown. The benchmark stack and the demo stack share port 8080 and never run together.

### Tag (S5.T19.2)

- Blocked by S5.T19.1 and by S5.T17.2.
- Gate: the README's video slot holds a real link; no owner slot, TODO, FIXME or "Video pending" remains unless the owner names it as allowed; race-detector tests pass at the commit to be tagged; the tree is clean.
- The tag is annotated. Its message (version, date, a one-paragraph summary, the benchmark commit, a pointer to the results) is shown to the owner for confirmation first.
- Creating the tag and pushing it are separate steps, and pushing needs its own explicit confirmation.

### Glossary

- Three entries are added: Matched, Peak, Rig-limited, written to the writing rules. "Nearest-equivalent", "Competitor" and "Hot key" already exist.
- An unreliable or degraded scenario is not made a glossary term, since "degraded" is already used descriptively.

## Testing Decisions

- These tickets produce documents, tracking edits and repository hygiene, not Go logic, so they are exempt from the red-green cycle under the project protocol. If any code were touched, the strict TDD protocol would apply, and none is planned.
- A good check here tests what a reader would observe, not how the text was produced. The checks are: the reader check (undefined terms), the number-to-claim trace, link resolution, rendered diagrams, and the fresh-clone quickstart run.
- The single highest seam is the published documents as a reader meets them. The fresh-clone quickstart is the only end-to-end check, and it runs through the demo's existing acceptance script. There are no new seams.
- Prior art: the benchmark results document already fails generation if a prose figure differs from a generated cell; the demo's acceptance script is the existing end-to-end gate; earlier docs-only tickets (the sprint retros, the architecture document) were accepted by reading and by the tracking-entry format.
- Each document's session-log entry records the reader check: every term that needed a definition and where it is defined.

## Out of Scope

- Relabelling or redefining the "Time to detection" column. It is proposed, not built.
- Re-running the benchmark matrix, the cold-start guard, diagnosing the degraded-scenario leak, fixing the health-probe counting, and fixing the narrative number check's section-boundary bug. All stay proposed.
- The P2C-EWMA latency gauge (S5.T18). It stays proposed.
- Any history rewrite, including the seven attribution trailers and the resume path, which was never committed.
- CI, linters, pre-commit hooks and Dependabot, which the project defers. The missing commit-message hook is named in the retrospective only.
- Rewriting existing ADRs to the new writing style.
- Recording the demo video and deciding where it lives (S5.T17.2 and the local-demo spec's open decision).
- Writing the README's AI-disclosure line and the retrospective's owner-voice items. Both are the owner's.
- Pushing anything, including the tag.

## Further Notes

- Open decisions for the owner: ship or prune the planning directory, the OpenCode config file and the agent-tooling docs; whether to accept the seven attribution trailers; where the demo video lives; whether the tag is pushed.
- Facts the documentation writer must verify before use and not assume: the concurrency primitives (cited to file and line), the mechanism that ejected the backend in C4, the 3500 req/s peak in C4 (inferred from the 1750 req/s rate being half of it, not printed in the result file), and the Little's-law figure for C5 (an explanation only).
- An earlier version of this spec included file paths and a detailed, file-level hygiene breakdown. This rewrite follows the spec template, keeps file paths out except for the owner-named deliverables, and leaves the ticket files unchanged.
