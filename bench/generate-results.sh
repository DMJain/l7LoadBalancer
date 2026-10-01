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
# Narrative number check (S5.T10.3): after generating, every latency or
# throughput figure quoted outside the marker regions (`ms`, `µs`, `req/s`,
# optionally prefixed `≈` or `~`) must match a generated cell. Percentages,
# core counts, sizes, ADR numbers and percentile names carry no such unit and
# are never matched. A mismatch exits nonzero naming every unmatched token, so
# the hand-written narrative cannot drift from the data (spec §67, §285).
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

A narrative `ms`, `µs` or `req/s` figure outside the markers (an optional
`≈`/`~` prefix is allowed) must match a generated cell or the run fails.
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

KILL_HEADER='| Run | Event at (s) | Total errors | First error (s) | Last error (s) | Time to detection (s) | p50 (ms) | p99 (ms) | p99 recovery (s) |'
KILL_RULE='| --- | --- | --- | --- | --- | --- | --- | --- | --- |'

# kill_row <txt> prints the backend-kill run's measurements: when the backend
# died, the error window, how long detection took, and the latency/recovery
# figures.
kill_row() { # <txt>
  local txt="$1"
  printf '| backend-kill | %s | %s | %s | %s | %s | %s | %s | %s |\n' \
    "$(meta_value "$txt" event_at_s)" "$(meta_value "$txt" total_errors)" \
    "$(meta_value "$txt" error_first_s)" "$(meta_value "$txt" error_last_s)" \
    "$(meta_value "$txt" time_to_detection_s)" "$(meta_value "$txt" p50_ms)" \
    "$(meta_value "$txt" p99_ms)" "$(meta_value "$txt" p99_recovery_s)"
}

RELOAD_HEADER='| Run | Event at (s) | Errors | pre-p99 (ms) | post-p99 (ms) | p99 factor | p50 (ms) | p99 (ms) | p99 recovery (s) | Verdict |'
RELOAD_RULE='| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |'

# reload_row <label> <txt> prints one reload run: the errors split into the two
# judged status classes, both p99 windows the verdict compares, and the verdict
# itself (PASS, or FAIL naming each unmet criterion).
reload_row() { # <label> <txt>
  local label="$1" txt="$2"
  printf '| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n' \
    "$label" "$(meta_value "$txt" event_at_s)" \
    "$(meta_value "$txt" non_2xx) non-2xx, $(meta_value "$txt" transport_errors) transport" \
    "$(meta_value "$txt" pre_p99_ms)" "$(meta_value "$txt" post_p99_ms)" \
    "$(meta_value "$txt" p99_factor)" "$(meta_value "$txt" p50_ms)" \
    "$(meta_value "$txt" p99_ms)" "$(meta_value "$txt" p99_recovery_s)" \
    "$(verdict_line "$txt")"
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
  local txt
  txt="$RESULTS/failure/roundrobin-10kb-backend-kill.txt"
  printf '%s\n%s\n' "$KILL_HEADER" "$KILL_RULE"
  if [[ -f "$txt" ]]; then
    kill_row "$txt"
  else
    printf 'generate-results: skipping failure/roundrobin-10kb-backend-kill (missing .txt)\n' >&2
  fi

  printf '\n%s\n%s\n' "$RELOAD_HEADER" "$RELOAD_RULE"
  local file label base
  # file label -> the run's published name (the reload runs' filenames are the
  # `sighup-*` harness tokens; the document uses the reload names).
  for file in sighup-noop sighup-drain; do
    case "$file" in
      sighup-noop) label='no-op reload' ;;
      sighup-drain) label='drain reload' ;;
    esac
    base="roundrobin-10kb-$file"
    txt="$RESULTS/failure/$base.txt"
    if [[ -f "$txt" ]]; then
      reload_row "$label" "$txt"
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

HOTKEY_HEADER='| Source | Size | Competitor | Load | Owner | backend1 % | backend2 % | backend3 % | backend4 % | Spill |'
HOTKEY_RULE='| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |'

# hotkey_row <source> <size> <comp> <load> <txt> prints one consistent-hash
# run's distribution: the plurality owner and spill flag from the `# hotkey`
# line and the per-backend shares from the `# arrivals` lines.
hotkey_row() { # <source> <size> <comp> <load> <txt>
  local source="$1" size="$2" comp="$3" load="$4" txt="$5"
  printf '| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n' \
    "$source" "$size" "$comp" "$load" "$(meta_value "$txt" owner)" \
    "$(arrival_share "$txt" backend1)" "$(arrival_share "$txt" backend2)" \
    "$(arrival_share "$txt" backend3)" "$(arrival_share "$txt" backend4)" \
    "$(meta_value "$txt" spill)"
}

# hotkey_latency_rows <source> <size> <comp> <txt> prints one row per latency
# rate block of a consistent-hash latency result. Its file holds one
# `# rate_rps=<n> (<pct>% of peak <peak>)` block per rate, each followed by its
# own `# arrivals` and `# hotkey` lines, so the shares are block-scoped and the
# pct comes from the rate line. The sweep shows the hot key's spill change with
# load.
hotkey_latency_rows() { # <source> <size> <comp> <txt>
  awk -v source="$1" -v size="$2" -v comp="$3" '
    function flush() {
      if (pct != "") {
        printf "| %s | %s | %s | latency@%s%% | %s | %s | %s | %s | %s | %s |\n", \
          source, size, comp, pct, owner, s1, s2, s3, s4, spill
      }
      pct = ""; owner = ""; spill = ""; s1 = s2 = s3 = s4 = ""
    }
    /^# rate_rps=/ { flush(); pct = $0; sub(/.*\(/, "", pct); sub(/%.*/, "", pct); next }
    /^# arrivals backend=/ {
      b = $3; sub(/^backend=/, "", b)
      sh = $5; sub(/^share_pct=/, "", sh)
      if (b == "backend1") s1 = sh
      else if (b == "backend2") s2 = sh
      else if (b == "backend3") s3 = sh
      else if (b == "backend4") s4 = sh
      next
    }
    /^# hotkey / { owner = $3; sub(/^owner=/, "", owner); spill = $4; sub(/^spill=/, "", spill); next }
    END { flush() }
  ' "$4"
}

emit_hotkey() {
  printf '%s\n%s\n' "$HOTKEY_HEADER" "$HOTKEY_RULE"
  local size comp base txt
  # Core: the throughput result per size and competitor is the single-address hot
  # key at peak; the latency result carries one distribution per rate (S5.T8.1).
  for size in 200b 10kb 1mb; do
    for comp in lb nginx; do
      base="consistent-hash-$size-$comp-throughput"
      txt="$RESULTS/core/$base.txt"
      if [[ -f "$txt" ]]; then
        hotkey_row core "$size" "$comp" throughput "$txt"
      else
        printf 'generate-results: skipping core/%s (missing .txt)\n' "$base" >&2
      fi
      base="consistent-hash-$size-$comp-latency"
      txt="$RESULTS/core/$base.txt"
      if [[ -f "$txt" ]]; then
        hotkey_latency_rows core "$size" "$comp" "$txt"
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
      hotkey_row degraded 10kb "$comp" degraded "$txt"
    else
      printf 'generate-results: skipping degraded/%s (missing .txt)\n' "$base" >&2
    fi
  done
}

# ---------------------------------------------------------------------------
# Narrative number check (S5.T10.3).
# ---------------------------------------------------------------------------

# allowed_cells <doc> prints `unit<TAB>value` for every numeric cell inside the
# generated regions whose table column is headed with a latency/throughput unit.
# Only cells under a `(ms)`, `(µs)` or `(req/s)` header are emitted, so a
# percentage or a raw count can never match a narrative figure.
allowed_cells() { # <doc>
  awk '
    function trim(s) { sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s); return s }
    function unit_of(h) {
      if (index(h, "(ms)") > 0) return "ms"
      if (index(h, "(µs)") > 0) return "µs"
      if (index(h, "(req/s)") > 0) return "req/s"
      return ""
    }
    /^<!-- BEGIN GENERATED: / { ing = 1; next }
    /^<!-- END GENERATED -->$/ { ing = 0; next }
    !ing { next }
    /^\|/ {
      if ($0 ~ /^\|[ \t]*-/) {
        for (k in unit) delete unit[k]
        n = split(hdr, cells, "|")
        for (i = 2; i < n; i++) unit[i] = unit_of(trim(cells[i]))
        is_table = 1
        next
      }
      if (is_table) {
        n = split($0, cells, "|")
        for (i = 2; i < n; i++) {
          v = trim(cells[i])
          if (unit[i] != "" && v ~ /^[0-9]/) print unit[i] "\t" v
        }
        next
      }
      hdr = $0
      next
    }
    { is_table = 0; hdr = "" }
  ' "$1"
}

# narrative_tokens <doc> prints `unit<TAB>number<TAB>token` for every benchmark
# figure outside the generated regions. A figure must start on a non-word
# boundary, so a percentile name (`p99.9`) or an identifier (`ADR-0020`) is
# never matched. The approximate prefix and any commas or spaces are stripped
# before the numeric comparison.
narrative_tokens() { # <doc>
  awk '
    /^<!-- BEGIN GENERATED: / { skip = 1; next }
    /^<!-- END GENERATED -->$/ { skip = 0; next }
    skip { next }
    {
      rest = " " $0
      while (match(rest, /[^A-Za-z0-9._][≈~]?[0-9][0-9,]*(\.[0-9]+)?[ \t]*(ms|µs|req\/s)/)) {
        tok = substr(rest, RSTART + 1, RLENGTH - 1)
        unit = ""
        if (tok ~ /req\/s$/) unit = "req/s"
        else if (tok ~ /ms$/) unit = "ms"
        else if (tok ~ /µs$/) unit = "µs"
        num = tok
        sub(/(ms|µs|req\/s)$/, "", num)
        gsub(/,/, "", num)
        gsub(/[ \t≈~]/, "", num)
        # The reported name must not carry a tab, or it would split the TSV
        # record the comparison pass reads.
        gsub(/\t/, " ", tok)
        printf "%s\t%s\t%s\n", unit, num, tok
        rest = substr(rest, RSTART + RLENGTH)
      }
    }
  ' "$1"
}

# check_narrative <doc> fails when a narrative figure matches no generated cell
# with the same unit, naming every unmatched token.
check_narrative() { # <doc>
  local doc="$1" unmatched
  allowed_cells "$doc" > "$GEN_DIR/allowed.tsv"
  narrative_tokens "$doc" > "$GEN_DIR/tokens.tsv"
  unmatched=$(awk -F'\t' '
    NR == FNR { gsub(/,/, "", $2); vals[$1] = vals[$1] " " $2; next }
    {
      found = 0
      n = split(vals[$1], arr, " ")
      for (i = 1; i <= n; i++) {
        if (arr[i] != "" && arr[i] + 0 == $2 + 0) { found = 1; break }
      }
      if (!found) print $3
    }
  ' "$GEN_DIR/allowed.tsv" "$GEN_DIR/tokens.tsv")
  if [[ -n "$unmatched" ]]; then
    printf 'generate-results: narrative figures with no matching generated cell:\n' >&2
    while IFS= read -r tok; do
      printf '  %s\n' "$tok" >&2
    done <<<"$unmatched"
    return 1
  fi
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

# Validate the candidate document before publishing it, so a narrative figure
# with no matching cell leaves the existing document untouched.
if ! check_narrative "$tmp"; then
  rm -f "$tmp"
  printf 'generate-results: fix the narrative figures above before publishing\n' >&2
  exit 1
fi

mv "$tmp" "$DOC"

printf 'generate-results: wrote methodology, core, protocol, failure, degraded and hot-key into %s\n' "$DOC"
