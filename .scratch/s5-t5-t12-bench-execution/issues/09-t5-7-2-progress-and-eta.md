# 09: Progress and ETA lines (S5.T5.7.2)

**What to build:** During a multi-hour run, the harness shows where it is, how long it has taken and roughly how long is left, so that nobody kills a healthy run at minute 40 thinking it has hung. The output goes to stderr so the summary tables on stdout stay clean for piping. TDD-exempt.

**Blocked by:** 08

**Status:** ready-for-agent

Spec: `../spec.md` (Provenance → progress)

- [ ] Before every run, one stderr line of the form `[run 14/71] h2/consistent-hash/10kb/lb peak-search  elapsed 00:42:10  eta 01:10:00`: position, scenario (protocol/algorithm/size/competitor), load type, elapsed and ETA.
- [ ] The total is computed from the selected slices, so the number is correct for `smoke`, a single slice and `all`.
- [ ] The ETA is naive: elapsed × total / completed. It is omitted or shown as `--` before the first run completes.
- [ ] stdout output is byte-for-byte unchanged.
- [ ] Verified: `shellcheck -S style` and `bash -n` pass; `./bench/run.sh smoke 2>/dev/null` shows only stdout content, and `./bench/run.sh smoke 1>/dev/null` shows the progress lines with correct totals. Both are recorded in the session log.
