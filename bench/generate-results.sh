#!/usr/bin/env bash
#
# generate-results.sh — the results generator (S5.T10.1–T10.2, issues 16–17).
#
# Turns the raw harness output into the published results tables, so no
# published number is typed by hand (spec §64, §271). It writes only inside
# marker regions of one results document:
#
#   <!-- BEGIN GENERATED: <section> -->
#   ...
#   <!-- END GENERATED -->
#
# Everything outside the markers — the hand-written narrative — is preserved
# byte-for-byte. If the document does not exist it is created with its marker
# skeleton. A second run against the same results produces no diff.
#
# Sections generated: methodology, core, protocol (S5.T10.1); failure,
# degraded, hot-key (S5.T10.2).
#
# Percentiles p50/p90/p95/p99/max come from each result's vegeta JSON report
# (committed as the `.json` sibling of the `.txt` summary); p99.9 comes from the
# `.hdr` HDR histogram, because vegeta's JSON report does not print it. The
# comparison label and peak throughput come from the result's metadata header
# (the first line of the `.txt` summary), the failure verdicts and the
# distribution/hot-key blocks from the `.txt` headers, and the methodology facts
# from `provenance.json`.
#
# p99.9 is the first HDR row whose cumulative percentile is >= 0.999 — a
# conservative upper bound on the 99.9th percentile, read straight from the
# histogram rather than interpolated.
#
# It refuses a provenance record with `git_dirty` true: dirty results must not
# be published by accident (spec §69).
#
# Usage:  bench/generate-results.sh [<results-dir>] [<document>]
#   results-dir  defaults to bench/results
#   document     defaults to RESULTS.md at the repo root
#
# The two optional paths are the test seam (spec §272); the committed fixture
# test is bench/generate-results_test.sh.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

RESULTS="${1:-$ROOT/bench/results}"
DOC="${2:-$ROOT/RESULTS.md}"

usage() {
  cat <<'EOF'
bench/generate-results.sh — generate the core/protocol tables and methodology
from the raw benchmark results.

Usage: bench/generate-results.sh [<results-dir>] [<document>]

  results-dir  the harness results directory (default: bench/results)
  document     the results document to fill (default: RESULTS.md)

Only the regions between `<!-- BEGIN GENERATED: <section> -->` and
`<!-- END GENERATED -->` markers are written; everything else is preserved.
Sections generated here: methodology, core, protocol, failure, degraded,
hot-key. A provenance record with `git_dirty` true is refused.
EOF
}

case "${1:-}" in
  -h|--help|help) usage; exit 0 ;;
esac

PROV="$RESULTS/provenance.json"
[[ -f "$PROV" ]] || { printf 'generate-results: no provenance record at %s\n' "$PROV" >&2; exit 1; }
if grep -Eq '"git_dirty"[[:space:]]*:[[:space:]]*true' "$PROV"; then
  printf 'generate-results: refusing results recorded from a dirty tree (provenance git_dirty=true); publish only results that trace to a commit\n' >&2
  exit 1
fi

GEN_DIR="$(mktemp -d)"
trap 'rm -rf "$GEN_DIR"' EXIT

# ---------------------------------------------------------------------------
# Provenance readers. The record is one field per line, so a line-anchored sed
# is enough and avoids a JSON parser dependency.
# ---------------------------------------------------------------------------

prov_str() { # <file> <key>
  sed -n "s/.*\"$2\":[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$1" | head -n1
}

prov_num() { # <file> <key>
  sed -n "s/.*\"$2\":[[:space:]]*\([0-9][0-9]*\).*/\1/p" "$1" | head -n1
}

# epoch_of <iso8601-utc> prints the Unix second. BSD date (macOS) and GNU date
# take different flags, so try BSD first and fall back.
epoch_of() {
  if date -j -f "%Y-%m-%dT%H:%M:%SZ" "$1" +%s >/dev/null 2>&1; then
    date -j -f "%Y-%m-%dT%H:%M:%SZ" "$1" +%s
  else
    date -u -d "$1" +%s
  fi
}

fmt_duration() { # <seconds>
  local s="$1"
  printf '%dh %dm %ds' "$(( s / 3600 ))" "$(( (s % 3600) / 60 ))" "$(( s % 60 ))"
}

bytes_to_gib() { # <bytes>
  awk -v b="$1" 'BEGIN { printf "%.0f", b / 1073741824 }'
}

# ---------------------------------------------------------------------------
# Result readers.
# ---------------------------------------------------------------------------

# meta_value <txt> <key> reads `key=value` from the result's metadata header.
meta_value() { # <file> <key>
  sed -n "s/.* $2=\([^ ]*\).*/\1/p" "$1" | head -n1
}

# json_latency_ms <json> <key> prints the named vegeta latency percentile in
# milliseconds. The JSON report stores latencies in nanoseconds as integer
# fields (`"50th"`,`"90th"`,`"95th"`,`"99th"`,`"max"`); a field grep is enough
# because vegeta emits the report as one minified line (the harness's own
# `json_field` reads it the same way).
json_latency_ms() { # <json> <key>
  local ns
  ns=$(grep -o "\"$2\":[0-9]*" "$1" | head -n1 | cut -d: -f2)
  awk -v n="${ns:-0}" 'BEGIN { printf "%.3f", n / 1e6 }'
}

# latency_cells <json> prints "p50 p90 p95 p99 max" in milliseconds.
latency_cells() { # <json>
  local p50 p90 p95 p99 pmax
  p50=$(json_latency_ms "$1" 50th)
  p90=$(json_latency_ms "$1" 90th)
  p95=$(json_latency_ms "$1" 95th)
  p99=$(json_latency_ms "$1" 99th)
  pmax=$(json_latency_ms "$1" max)
  printf '%s %s %s %s %s' "$p50" "$p90" "$p95" "$p99" "$pmax"
}

# p999 <hdr> prints the 99.9th percentile from the HDR histogram: the value of
# the first row whose cumulative percentile is >= 0.999.
p999() { # <hdr>
  awk '$2 + 0 >= 0.999 { printf "%.3f", $1; exit }' "$1"
}

# verdict_line <txt> prints the reload run's verdict exactly as written — `PASS`
# or `FAIL failed=<criteria>` — from its own `verdict=` line (the failure slice
# writes it as the last line of the measurement block). Empty for backend-kill,
# which carries no verdict.
verdict_line() { # <txt>
  sed -n 's/^verdict=//p' "$1" | head -n1
}

# arrival_share <txt> <backend> prints one backend's `share_pct` from the result
# header's `# arrivals backend=<name> count=<n> share_pct=<p>` lines (S5.T8.1).
arrival_share() { # <txt> <backend>
  sed -n "s/^# arrivals backend=$2 count=[0-9]* share_pct=\([0-9.]*\)\$/\1/p" "$1" | head -n1
}

# snapshot_count <txt> <label> <backend> prints the raw /stats count for one
# backend at one drain-reload event point, from the result header's
# `# snapshot=<label> backend=<name> count=<n>` lines (S5.T9.2).
snapshot_count() { # <txt> <label> <backend>
  sed -n "s/^# snapshot=$2 backend=$3 count=\([0-9]*\)\$/\1/p" "$1" | head -n1
}

# ---------------------------------------------------------------------------
# Sections.
# ---------------------------------------------------------------------------

TABLE_HEADER='| Algorithm | Size | Competitor | Comparison | p50 (ms) | p90 (ms) | p95 (ms) | p99 (ms) | p99.9 (ms) | max (ms) | Peak (req/s) |'
TABLE_RULE='| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |'

# table_row <algo> <size> <competitor> <txt> <json> <hdr> prints one Markdown
# row. The metadata header (comparison, peak throughput) is read from the `.txt`,
# the percentiles from the `.json`, and p99.9 from the `.hdr`.
table_row() { # <algo> <size> <comp> <txt> <json> <hdr>
  local algo="$1" size="$2" comp="$3" txt="$4" json="$5" hdr="$6"
  local cmp peak cells p50 p90 p95 p99 pmax p99_9
  cmp=$(meta_value "$txt" comparison)
  peak=$(meta_value "$txt" peak_rps)
  cells=$(latency_cells "$json")
  read -r p50 p90 p95 p99 pmax <<<"$cells"
  p99_9=$(p999 "$hdr")
  printf '| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n' \
    "$algo" "$size" "$comp" "$cmp" "$p50" "$p90" "$p95" "$p99" "$p99_9" "$pmax" "$peak"
}

# emit_table <outdir> <protocol-token> <algorithm...> prints the table for the
# given algorithms over the fixed size and competitor order. An empty protocol
# token selects the core filenames; a token (http11) selects the protocol ones.
emit_table() { # <outdir> <proto-token> <algorithm...>
  local outdir="$1" proto="$2"
  shift 2
  local algo size comp base txt json hdr
  printf '%s\n%s\n' "$TABLE_HEADER" "$TABLE_RULE"
  for algo in "$@"; do
    for size in 200b 10kb 1mb; do
      for comp in lb nginx; do
        base="$algo-$size"
        [[ -n "$proto" ]] && base="$base-$proto"
        base="$base-$comp-throughput"
        txt="$RESULTS/$outdir/$base.txt"
        json="$RESULTS/$outdir/$base.json"
        hdr="$RESULTS/$outdir/$base.hdr"
        # The committed fixture is deliberately partial, so a missing result is
        # skipped rather than fatal — but never silently: a real run that lost a
        # result set would otherwise publish a quietly incomplete table.
        if [[ -f "$txt" && -f "$json" && -f "$hdr" ]]; then
          table_row "$algo" "$size" "$comp" "$txt" "$json" "$hdr"
        else
          printf 'generate-results: skipping %s/%s (missing .txt/.json/.hdr)\n' "$outdir" "$base" >&2
        fi
      done
    done
  done
}

emit_methodology() {
  local host_os host_cpu go_version nginx_version vegeta_version docker_version compose_version
  local docker_cpus mem_bytes gomaxprocs nginx_workers start finish
  host_os=$(prov_str "$PROV" host_os)
  host_cpu=$(prov_str "$PROV" host_cpu)
  go_version=$(prov_str "$PROV" lb_go_version)
  nginx_version=$(prov_str "$PROV" nginx_version)
  vegeta_version=$(prov_str "$PROV" vegeta_version)
  docker_version=$(prov_str "$PROV" docker_version)
  compose_version=$(prov_str "$PROV" compose_version)
  docker_cpus=$(prov_num "$PROV" docker_cpus)
  mem_bytes=$(prov_num "$PROV" docker_memory_bytes)
  gomaxprocs=$(prov_num "$PROV" lb_gomaxprocs)
  nginx_workers=$(prov_num "$PROV" nginx_workers)
  start=$(prov_str "$PROV" start_time)
  finish=$(prov_str "$PROV" finish_time)

  local wall="unknown"
  if [[ -n "$start" && -n "$finish" ]]; then
    wall=$(fmt_duration "$(( $(epoch_of "$finish") - $(epoch_of "$start") ))")
  fi

  cat <<EOF
**Hardware.** ${host_cpu} (${host_os}), ${docker_cpus} vCPUs and $(bytes_to_gib "$mem_bytes") GiB assigned to Docker.

**Versions.** Load balancer built with ${go_version}; Nginx ${nginx_version}; vegeta ${vegeta_version}; Docker ${docker_version}; Compose ${compose_version}.

**CPU pinning.** LB and Nginx: cores 0–1; backends 1–4: cores 2–5, one each; vegeta: cores 6–7. The split is fixed and never scaled from the host, and the harness verified it at run time: the load balancer reported gomaxprocs=${gomaxprocs}, and Nginx ran ${nginx_workers} workers.

**Backend logging.** Per-request backend logging was off for every run (\`LOG_REQUESTS=false\`), so the backends' stdout was not a throughput ceiling.

**Measured wall-clock.** The run started ${start} and finished ${finish}, a measured duration of ${wall}.
EOF
}

# --- Failure verdicts (S5.T10.2) -------------------------------------------

FAILURE_HEADER='| Run | Event at (s) | p50 (ms) | p99 (ms) | p99 recovery (s) | Errors | Verdict |'
FAILURE_RULE='| --- | --- | --- | --- | --- | --- | --- |'

# failure_row <label> <txt> prints one failure run. The reload runs report their
# errors as the two judged status classes (non-2xx responses plus transport
# errors) and carry a verdict; backend-kill reports its total error count and no
# verdict.
failure_row() { # <label> <txt>
  local label="$1" txt="$2" event p50 p99 rec errors verdict
  event=$(meta_value "$txt" event_at_s)
  p50=$(meta_value "$txt" p50_ms)
  p99=$(meta_value "$txt" p99_ms)
  rec=$(meta_value "$txt" p99_recovery_s)
  if [[ "$label" == backend-kill ]]; then
    errors=$(meta_value "$txt" total_errors)
    verdict='—'
  else
    errors="$(meta_value "$txt" non_2xx) non-2xx, $(meta_value "$txt" transport_errors) transport"
    verdict=$(verdict_line "$txt")
  fi
  printf '| %s | %s | %s | %s | %s | %s | %s |\n' \
    "$label" "$event" "$p50" "$p99" "$rec" "$errors" "$verdict"
}

# snapshot_table <txt> prints the drain reload's three raw arrival-counter
# snapshots as a table, one row per event point and one column per backend.
snapshot_table() { # <txt>
  printf '| Snapshot | backend1 | backend2 | backend3 | backend4 |\n'
  printf '| --- | --- | --- | --- | --- |\n'
  local label row b
  for label in before applied end; do
    row="| $label"
    for b in backend1 backend2 backend3 backend4; do
      row="$row | $(snapshot_count "$1" "$label" "$b")"
    done
    printf '%s |\n' "$row"
  done
}

emit_failure() {
  printf '%s\n%s\n' "$FAILURE_HEADER" "$FAILURE_RULE"
  local file label base txt
  # file label -> the run's published name (the reload runs' filenames are the
  # `sighup-*` harness tokens; the document uses the reload names).
  for file in backend-kill sighup-noop sighup-drain; do
    case "$file" in
      sighup-noop) label='no-op reload' ;;
      sighup-drain) label='drain reload' ;;
      *) label="$file" ;;
    esac
    base="roundrobin-10kb-$file"
    txt="$RESULTS/failure/$base.txt"
    if [[ -f "$txt" ]]; then
      failure_row "$label" "$txt"
    else
      printf 'generate-results: skipping failure/%s (missing .txt)\n' "$base" >&2
    fi
  done
  txt="$RESULTS/failure/roundrobin-10kb-sighup-drain.txt"
  if [[ -f "$txt" ]]; then
    printf '\n'
    snapshot_table "$txt"
  fi
}

# --- Degraded distributions (S5.T10.2) --------------------------------------

DEGRADED_HEADER='| Algorithm | Competitor | p50 (ms) | p99 (ms) | backend1 % | backend2 % | backend3 % | backend4 % |'
DEGRADED_RULE='| --- | --- | --- | --- | --- | --- | --- | --- |'

# degraded_row <algo> <comp> <txt> <json> prints one degraded run: p50/p99 from
# the JSON report, the per-backend shares from the result header.
degraded_row() { # <algo> <comp> <txt> <json>
  local algo="$1" comp="$2" txt="$3" json="$4"
  printf '| %s | %s | %s | %s | %s | %s | %s | %s |\n' \
    "$algo" "$comp" "$(json_latency_ms "$json" 50th)" "$(json_latency_ms "$json" 99th)" \
    "$(arrival_share "$txt" backend1)" "$(arrival_share "$txt" backend2)" \
    "$(arrival_share "$txt" backend3)" "$(arrival_share "$txt" backend4)"
}

emit_degraded() {
  printf '%s\n%s\n' "$DEGRADED_HEADER" "$DEGRADED_RULE"
  local algo comp base txt json
  for algo in roundrobin leastconn consistent-hash p2c-ewma; do
    for comp in lb nginx; do
      base="$algo-10kb-$comp-degraded"
      txt="$RESULTS/degraded/$base.txt"
      json="$RESULTS/degraded/$base.json"
      if [[ -f "$txt" && -f "$json" ]]; then
        degraded_row "$algo" "$comp" "$txt" "$json"
      else
        printf 'generate-results: skipping degraded/%s (missing .txt/.json)\n' "$base" >&2
      fi
    done
  done
}

# --- Hot-key distributions (S5.T10.2) ---------------------------------------

HOTKEY_HEADER='| Source | Size | Competitor | Owner | backend1 % | backend2 % | backend3 % | backend4 % | Spill |'
HOTKEY_RULE='| --- | --- | --- | --- | --- | --- | --- | --- | --- |'

# hotkey_row <source> <size> <comp> <txt> prints one consistent-hash run's
# distribution: the plurality owner and spill flag from the `# hotkey` line and
# the per-backend shares from the `# arrivals` lines.
hotkey_row() { # <source> <size> <comp> <txt>
  local source="$1" size="$2" comp="$3" txt="$4"
  printf '| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n' \
    "$source" "$size" "$comp" "$(meta_value "$txt" owner)" \
    "$(arrival_share "$txt" backend1)" "$(arrival_share "$txt" backend2)" \
    "$(arrival_share "$txt" backend3)" "$(arrival_share "$txt" backend4)" \
    "$(meta_value "$txt" spill)"
}

emit_hotkey() {
  printf '%s\n%s\n' "$HOTKEY_HEADER" "$HOTKEY_RULE"
  local size comp base txt
  # Core: the consistent-hash throughput result per size and competitor is the
  # single-address hot-key scenario.
  for size in 200b 10kb 1mb; do
    for comp in lb nginx; do
      base="consistent-hash-$size-$comp-throughput"
      txt="$RESULTS/core/$base.txt"
      if [[ -f "$txt" ]]; then
        hotkey_row core "$size" "$comp" "$txt"
      else
        printf 'generate-results: skipping core/%s (missing .txt)\n' "$base" >&2
      fi
    done
  done
  # Degraded: the consistent-hash runs carry the same distribution.
  for comp in lb nginx; do
    base="consistent-hash-10kb-$comp-degraded"
    txt="$RESULTS/degraded/$base.txt"
    if [[ -f "$txt" ]]; then
      hotkey_row degraded 10kb "$comp" "$txt"
    else
      printf 'generate-results: skipping degraded/%s (missing .txt)\n' "$base" >&2
    fi
  done
}

# ---------------------------------------------------------------------------
# Write.
# ---------------------------------------------------------------------------

emit_methodology > "$GEN_DIR/methodology.md"
emit_table core "" roundrobin leastconn consistent-hash p2c-ewma > "$GEN_DIR/core.md"
emit_table protocol http11 roundrobin > "$GEN_DIR/protocol.md"
emit_failure > "$GEN_DIR/failure.md"
emit_degraded > "$GEN_DIR/degraded.md"
emit_hotkey > "$GEN_DIR/hot-key.md"

if [[ ! -f "$DOC" ]]; then
  mkdir -p "$(dirname "$DOC")"
  cat > "$DOC" <<'EOF'
# Benchmark results

<!-- BEGIN GENERATED: methodology -->
<!-- END GENERATED -->

<!-- BEGIN GENERATED: core -->
<!-- END GENERATED -->

<!-- BEGIN GENERATED: protocol -->
<!-- END GENERATED -->

<!-- BEGIN GENERATED: failure -->
<!-- END GENERATED -->

<!-- BEGIN GENERATED: degraded -->
<!-- END GENERATED -->

<!-- BEGIN GENERATED: hot-key -->
<!-- END GENERATED -->
EOF
fi

# Replace each known generated region in place, preserving everything else
# byte-for-byte. An unknown `BEGIN GENERATED` section is left untouched so a
# later section added by another ticket survives until it is generated here.
tmp="$DOC.tmp.$$"
awk -v dir="$GEN_DIR" -v secs="methodology core protocol failure degraded hot-key" '
  BEGIN { n = split(secs, s, " "); for (i = 1; i <= n; i++) known[s[i]] = 1 }
  /^<!-- BEGIN GENERATED: / {
    sec = $0
    sub(/^<!-- BEGIN GENERATED: /, "", sec)
    sub(/ -->$/, "", sec)
    print
    if (sec in known) {
      f = dir "/" sec ".md"
      while ((getline line < f) > 0) print line
      close(f)
      skip = 1
    } else {
      skip = 0
    }
    next
  }
  /^<!-- END GENERATED -->$/ { skip = 0; print; next }
  skip { next }
  { print }
' "$DOC" > "$tmp"
mv "$tmp" "$DOC"

printf 'generate-results: wrote methodology, core, protocol, failure, degraded and hot-key into %s\n' "$DOC"
