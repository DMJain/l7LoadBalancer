#!/usr/bin/env bash
#
# generate-results_test.sh — fixture test for the results generator (S5.T10.1).
#
# Runs bench/generate-results.sh against a small committed fixture result set
# (core + protocol .txt/.json/.hdr files and a clean provenance record) and
# asserts:
#   - the document is created with its marker skeleton when absent;
#   - the core and protocol tables carry the expected p50/p90/p95/p99/p99.9/max
#     and peak-throughput cells, with p99.9 taken from the .hdr histograms and
#     the other percentiles from the .json reports (proved by perturbing a JSON
#     percentile and watching the cell follow);
#   - the methodology section is generated from the provenance record;
#   - a second run produces no diff;
#   - narrative outside the markers survives, stale generated content is
#     replaced;
#   - a provenance record with git_dirty true is refused.
#
# The generator is shell-only, so it is TDD-exempt (AGENTS.md); this committed
# script is the test the ticket requires. Run it directly:
#   ./bench/generate-results_test.sh

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GEN="$ROOT/bench/generate-results.sh"
FIXTURE="$ROOT/bench/testdata/results-generator"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

FAILURES=0
pass() { printf 'ok   - %s\n' "$1"; }
fail() { printf 'FAIL - %s\n' "$1" >&2; FAILURES=$(( FAILURES + 1 )); }

assert_contains() { # <file> <needle> <description>
  if grep -qF -- "$2" "$1"; then
    pass "$3"
  else
    fail "$3 (missing: $2)"
  fi
}

assert_not_contains() { # <file> <needle> <description>
  if grep -qF -- "$2" "$1"; then
    fail "$3 (unexpected: $2)"
  else
    pass "$3"
  fi
}

RESULTS="$WORK/results"
cp -R "$FIXTURE" "$RESULTS"
DOC="$WORK/RESULTS.md"

# ---------------------------------------------------------------------------
# Creates the document with its marker skeleton when it does not exist.
# ---------------------------------------------------------------------------
"$GEN" "$RESULTS" "$DOC" >/dev/null

if [[ -f "$DOC" ]]; then
  pass "creates the document when absent"
else
  fail "creates the document when absent"
fi
assert_contains "$DOC" "<!-- BEGIN GENERATED: methodology -->" "methodology marker created"
assert_contains "$DOC" "<!-- BEGIN GENERATED: core -->" "core marker created"
assert_contains "$DOC" "<!-- BEGIN GENERATED: protocol -->" "protocol marker created"
assert_contains "$DOC" "<!-- END GENERATED -->" "end marker created"

# ---------------------------------------------------------------------------
# Core table: algorithm, size, competitor, comparison and the percentile cells.
# p99.9 values (2.500, 7.500, 9.000) appear only in the .hdr fixture, so their
# presence proves the histogram is the source.
# ---------------------------------------------------------------------------
assert_contains "$DOC" "| Algorithm | Size | Competitor | Comparison | p50 (ms) | p90 (ms) | p95 (ms) | p99 (ms) | p99.9 (ms) | max (ms) | Peak (req/s) |" \
  "core table header"
assert_contains "$DOC" "| roundrobin | 200b | lb | matched | 0.277 | 0.452 | 0.610 | 1.240 | 2.500 | 9.810 | 48000 |" \
  "core roundrobin/200b/lb row"
assert_contains "$DOC" "| consistent-hash | 10kb | lb | nearest-equivalent | 0.410 | 0.900 | 1.350 | 4.200 | 7.500 | 22.500 | 44000 |" \
  "core consistent-hash/10kb/lb row"
assert_contains "$DOC" "| p2c-ewma | 10kb | lb | nearest-equivalent | 0.390 | 0.980 | 1.600 | 5.100 | 9.000 | 30.000 | 43000 |" \
  "core p2c-ewma/10kb/lb row"

# ---------------------------------------------------------------------------
# Protocol table.
# ---------------------------------------------------------------------------
assert_contains "$DOC" "| roundrobin | 10kb | lb | matched | 0.700 | 1.600 | 2.400 | 6.000 | 8.000 | 19.000 | 30000 |" \
  "protocol roundrobin/10kb/lb row"
assert_contains "$DOC" "| roundrobin | 1mb | nginx | matched | 1.000 | 2.100 | 3.000 | 7.500 | 10.000 | 25.000 | 28000 |" \
  "protocol roundrobin/1mb/nginx row"

# ---------------------------------------------------------------------------
# The non-p99.9 percentile cells come from the JSON report, not the .txt text
# report. Perturb one JSON percentile, regenerate, and confirm the cell follows;
# restore afterwards. (p99.9 has its own proof above: 2.500 appears only in the
# .hdr fixture.)
# ---------------------------------------------------------------------------
sed 's/"50th":277000/"50th":123000/' "$RESULTS/core/roundrobin-200b-lb-throughput.json" \
  > "$WORK/perturbed.json"
mv "$WORK/perturbed.json" "$RESULTS/core/roundrobin-200b-lb-throughput.json"
"$GEN" "$RESULTS" "$DOC" >/dev/null
assert_contains "$DOC" "| roundrobin | 200b | lb | matched | 0.123 | 0.452 | 0.610 | 1.240 | 2.500 | 9.810 | 48000 |" \
  "percentiles come from the JSON report"
cp "$FIXTURE/core/roundrobin-200b-lb-throughput.json" "$RESULTS/core/roundrobin-200b-lb-throughput.json"
"$GEN" "$RESULTS" "$DOC" >/dev/null

# ---------------------------------------------------------------------------
# Methodology, generated from the provenance record.
# ---------------------------------------------------------------------------
assert_contains "$DOC" "Apple M2 Pro" "methodology hardware CPU"
assert_contains "$DOC" "Darwin 24.6.0 arm64" "methodology host OS"
assert_contains "$DOC" "go1.25.1" "methodology Go version"
assert_contains "$DOC" "1.27.4" "methodology Nginx version"
assert_contains "$DOC" "v12.8.3" "methodology vegeta version"
assert_contains "$DOC" "LB and Nginx: cores 0–1; backends 1–4: cores 2–5, one each; vegeta: cores 6–7." \
  "methodology cpuset sentence"
assert_contains "$DOC" "LOG_REQUESTS" "methodology backend-logging statement"
assert_contains "$DOC" "gomaxprocs=2" "methodology observed LB GOMAXPROCS"
assert_contains "$DOC" "2 workers" "methodology observed Nginx worker count"
assert_contains "$DOC" "2h 13m 40s" "methodology measured wall-clock"

# ---------------------------------------------------------------------------
# Determinism: a second run produces no diff.
# ---------------------------------------------------------------------------
cp "$DOC" "$WORK/first.md"
"$GEN" "$RESULTS" "$DOC" >/dev/null
if diff -q "$WORK/first.md" "$DOC" >/dev/null; then
  pass "second run produces no diff"
else
  fail "second run produces no diff"
  diff "$WORK/first.md" "$DOC" >&2 || true
fi

# ---------------------------------------------------------------------------
# Narrative outside the markers survives; stale generated content is replaced.
# ---------------------------------------------------------------------------
cat > "$DOC" <<'EOF'
# Title before

NARRATIVE-ONE

<!-- BEGIN GENERATED: methodology -->
STALE-METHODOLOGY
<!-- END GENERATED -->

NARRATIVE-TWO

<!-- BEGIN GENERATED: core -->
STALE-CORE
<!-- END GENERATED -->

<!-- BEGIN GENERATED: protocol -->
STALE-PROTOCOL
<!-- END GENERATED -->

NARRATIVE-THREE
EOF
"$GEN" "$RESULTS" "$DOC" >/dev/null
assert_contains "$DOC" "# Title before" "narrative title survives"
assert_contains "$DOC" "NARRATIVE-ONE" "narrative before the first marker survives"
assert_contains "$DOC" "NARRATIVE-TWO" "narrative between markers survives"
assert_contains "$DOC" "NARRATIVE-THREE" "narrative after the last marker survives"
assert_not_contains "$DOC" "STALE-METHODOLOGY" "stale methodology content replaced"
assert_not_contains "$DOC" "STALE-CORE" "stale core content replaced"
assert_not_contains "$DOC" "STALE-PROTOCOL" "stale protocol content replaced"

# ---------------------------------------------------------------------------
# A provenance record with git_dirty true is refused.
# ---------------------------------------------------------------------------
DIRTY="$WORK/dirty-results"
cp -R "$FIXTURE" "$DIRTY"
sed 's/"git_dirty": false/"git_dirty": true/' "$FIXTURE/provenance.json" > "$DIRTY/provenance.json"
if "$GEN" "$DIRTY" "$WORK/dirty.md" >"$WORK/dirty.out" 2>&1; then
  fail "refuses a provenance record with git_dirty true"
else
  pass "refuses a provenance record with git_dirty true"
fi
if grep -qi 'dirty' "$WORK/dirty.out"; then
  pass "names the dirty-tree refusal"
else
  fail "names the dirty-tree refusal"
fi

# ---------------------------------------------------------------------------
printf '\n'
if (( FAILURES > 0 )); then
  printf '%d check(s) failed\n' "$FAILURES" >&2
  exit 1
fi
printf 'all checks passed\n'
