#!/usr/bin/env bash
#
# run.sh — the benchmark execution harness (S5.T4-harness, issue 06).
#
# This script is the single source of truth for the benchmark matrix and its
# parameters. Thresholds, rates, durations, and warmup are constants below, not
# flags: published numbers must be reproducible from a clean checkout with the
# same thresholds, and flags would invite "but I ran it with different
# thresholds" comparisons (spec §28, §32).
#
# Usage:  ./bench/run.sh [core|protocol|failure|smoke|all]   (default: all)
#
#   core      HTTP/2 (TLS+ALPN) matrix — 48 runs
#   protocol  HTTP/1.1 round-robin comparison — 12 runs
#   failure   backend-kill and SIGHUP-under-load — 2 runs
#   smoke     a ~1-minute preflight over every (protocol, algorithm,
#             competitor) combination the matrix uses — 10 attacks, not part
#             of `all` (S5.T5.3)
#   all       the full 62-run matrix (smoke excluded)
#
# Every algorithm runs head-to-head with an Nginx competitor. Each result
# header records `comparison=matched` (roundrobin, leastconn) or
# `comparison=nearest-equivalent` (consistent-hash, p2c-ewma), and every
# nearest-equivalent competitor's header states its gap (S5.T5.2).
#
# Results land as .txt summaries + .hdr HDR histograms under
# bench/results/{core,protocol,failure}/ with parameter-encoded filenames, and
# each slice prints a summary table to stdout (spec §27, §30, §32).
#
# Everything runs in-compose via `docker compose run vegeta`; the host never
# needs a vegeta binary (spec §22). The LB and Nginx are recreated per
# algorithm/protocol by changing the mounted config, because only the backend
# list is SIGHUP-reloadable — algorithm and listener mode are not (ADR-0015).
#
# Warmup: each measured attack runs WARMUP_SECS longer than its measurement
# window, then the first WARMUP_SECS of results are dropped by timestamp. The
# drop is a numeric filter on the CSV's Unix-nanosecond column; at ~1.8e18 ns
# a double loses ~256 ns, far below the 5 s window, so the boundary is exact
# for any practical purpose. (vegeta has no built-in warmup skip.)
#
# Failure-mode steady state is TLS+HTTP/2 at 50% of discovered peak (spec §29).
# `docker compose kill -s HUP lb` is the host-side form of the in-container
# `kill -HUP 1` the ticket names: the distroless image has no shell or kill
# binary, and the LB is PID 1 in its container (ADR-0019).
#
# The smoke slice (S5.T5.3) is the preflight gate: for every (protocol,
# algorithm, competitor) combination the matrix uses, it runs a short low-rate
# attack on the 10 KiB endpoint and requires 100% success, failing with a
# nonzero exit naming the combination otherwise. It catches broken configs in
# about a minute (the class of routing bug that once had TLS upstreams return
# 400) instead of hours into a run. It is deliberately not part of `all`.

set -euo pipefail

# ---------------------------------------------------------------------------
# Parameters — constants, deliberately not flags (spec §28).
# ---------------------------------------------------------------------------

SEED_RATE=1000            # req/s the throughput search seeds at (spec §28)
RATE_GRANULARITY=500      # req/s bisection convergence granularity (spec §28)
MAX_RATE=200000           # req/s safety cap so a never-failing search terminates
P99_CEILING_MS=100        # a rate fails when p99 exceeds this (spec §28)
ERROR_CEILING_PCT=1       # a rate fails when error rate exceeds this percent
WARMUP_SECS=5             # first seconds of every measured attack, discarded
STEP_SECS=10              # measurement window per throughput-search step (10–15s)
LATENCY_RATES_PCT=(30 50 70 90)  # latency-profile rates, as % of discovered peak (spec §21)
FAILURE_RATE_PCT=50       # failure-mode steady state as a percent of peak
FAILURE_SECS=60           # failure-mode total duration (spec §29)
FAILURE_EVENT_AT=30       # seconds into the failure run when the event fires
SETTLE_SECS=10            # settle time after restarting backend3 (two probe intervals)

# Smoke slice (S5.T5.3) — a preflight, not part of `all`. Every (protocol,
# algorithm, competitor) combination the matrix uses must serve 100% in a
# short, low-rate attack on one size.
SMOKE_RATE=50             # req/s per smoke attack (S5.T5.3)
SMOKE_SECS=2              # seconds per smoke attack (S5.T5.3)
SMOKE_SIZE=10kb           # endpoint size for smoke attacks (S5.T5.3)
SMOKE_READY_TRIES=5       # readiness polls before a smoke attack is judged

# Detection time is dominated by the active health checker's cadence. None of
# the bench configs set health.probe_interval, so every run uses the config
# default: config.DefaultProbeInterval = 5s, with 3 consecutive failures before
# ejection (internal/health). Set health.probe_interval in a config to change
# it for a run, and record the value alongside the numbers.

SIZES=(200b 10kb 1mb)
ALGOS=(roundrobin leastconn consistent-hash p2c-ewma)

# ---------------------------------------------------------------------------
# Paths and compose plumbing.
# ---------------------------------------------------------------------------

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/bench/docker-compose.yml"
COMPOSE=(docker compose -f "$COMPOSE_FILE")
RESULTS="$ROOT/bench/results"
TMP="$RESULTS/.tmp"
# shellcheck disable=SC2034 # used by the compose file's ${VAR:-default}
export LB_CONFIG="./configs/http11/roundrobin.yaml"
# shellcheck disable=SC2034
export NGINX_CONF="http11/roundrobin.conf"
# shellcheck disable=SC2034
export BACKEND_TLS_CERT_FILE=""
# shellcheck disable=SC2034
export BACKEND_TLS_KEY_FILE=""

usage() {
  cat <<'EOF'
bench/run.sh — the benchmark execution harness (S5.T4-harness).

Usage: bench/run.sh [core|protocol|failure|smoke|all]   (default: all)

  core      HTTP/2 (TLS+ALPN) matrix — 48 runs
  protocol  HTTP/1.1 round-robin comparison — 12 runs
  failure   backend-kill and SIGHUP-under-load — 2 runs
  smoke     preflight: every (protocol, algorithm, competitor) combination
            the matrix uses, 10 short attacks, 100% required, not part of all
  all       the full 62-run matrix (smoke excluded)

Results land as .txt summaries + .hdr HDR histograms under
bench/results/{core,protocol,failure}/ with parameter-encoded filenames, and
each slice prints a summary table to stdout. All parameters are constants at
the top of this script; see the header comment for the methodology.
EOF
}

log() { printf '%s\n' "$*"; }
warn() { printf 'WARN: %s\n' "$*" >&2; }

require_docker() {
  command -v docker >/dev/null 2>&1 || { warn "docker is required"; exit 1; }
  docker compose version >/dev/null 2>&1 || { warn "docker compose v2 is required"; exit 1; }
  # set_lb relies on `up --wait` (compose >= 2.17); fail up front, not mid-slice.
  docker compose up --help 2>&1 | grep -q -- '--wait' \
    || { warn "docker compose up --wait is required (compose >= 2.17)"; exit 1; }
}

ensure_certs() {
  if [[ ! -f "$ROOT/certs/server.crt" || ! -f "$ROOT/certs/server.key" ]]; then
    log "generating the shared SAN certificate ..."
    "$ROOT/scripts/generate-cert.sh"
  fi
}

# vegeta_run <command> runs a shell command inside a one-off vegeta container
# with the results directory mounted at /results. The exported compose
# variables keep the running lb/nginx/backends on the config this slice
# selected, so `compose run` never recreates them.
vegeta_run() {
  "${COMPOSE[@]}" run --rm -T -v "$RESULTS:/results" --entrypoint sh vegeta -c "$1"
}

# ---------------------------------------------------------------------------
# CPU-pinning verification (S5.T5.7.1). The compose cpusets are the pinning;
# these checks prove at run time that they took effect, so a throughput
# difference between the LB and Nginx cannot be "one of them was starved or
# over-provisioned". Each aborts the slice, naming the check and the value seen.
# ---------------------------------------------------------------------------

# verify_lb_gomaxprocs reads `gomaxprocs` from the LB's startup line (S5.T5.6)
# and aborts unless it is 2 — the LB's cpuset is cores 0–1. The startup line is
# JSON, so the value is a plain integer field.
verify_lb_gomaxprocs() {
  local seen
  "${COMPOSE[@]}" logs --no-color lb > "$TMP/lb.log" 2>/dev/null || true
  seen=$(json_field "$TMP/lb.log" gomaxprocs)
  if [[ "$seen" != "2" ]]; then
    warn "lb cpu-pinning check FAILED: gomaxprocs=${seen:-unknown} (need 2)"
    exit 1
  fi
}

# verify_nginx_workers waits until Nginx serves a 200 through its listener (the
# workers fork slightly after the container starts, so counting immediately can
# see zero), then counts worker processes and aborts unless there are 2 — the
# Nginx cpuset is cores 0–1. wait_target leaves its last response in
# $TMP/ready.json; the extra smoke_ok_200 rejects a 3xx, which wait_target's
# readiness signal (vegeta `success`) would accept but the ticket's "200" would
# not. `[n]ginx` keeps grep from matching its own argv.
verify_nginx_workers() { # <proto>
  local proto="$1" url count
  url=$(target_url "$proto" nginx "$SMOKE_SIZE")
  if ! wait_target "$url" || ! smoke_ok_200 "$TMP/ready.json"; then
    warn "nginx worker check FAILED: listener never served a 200 at $url"
    exit 1
  fi
  count=$("${COMPOSE[@]}" exec -T nginx ps 2>/dev/null \
    | grep -c '[n]ginx: worker process' || true)
  if [[ "$count" != "2" ]]; then
    warn "nginx worker check FAILED: ${count:-unknown} worker process(es) (need 2)"
    exit 1
  fi
}

# set_lb <config-path-relative-to-bench> recreates the lb service on a config
# and verifies its CPU pinning before the slice continues. LB_CONFIG is assigned
# (not prefixed) so the value persists for the subsequent `compose run` calls
# too; otherwise compose would see the old value and recreate lb back onto the
# default config mid-slice.
set_lb() {
  LB_CONFIG="$1"
  "${COMPOSE[@]}" up -d --force-recreate --wait lb
  verify_lb_gomaxprocs
}

# up_h2 brings the four backends up serving TLS and nginx up as TLS+HTTP/2.
up_h2() {
  ensure_certs
  BACKEND_TLS_CERT_FILE=/certs/server.crt
  BACKEND_TLS_KEY_FILE=/certs/server.key
  NGINX_CONF=h2/roundrobin.conf
  "${COMPOSE[@]}" up -d --force-recreate backend1 backend2 backend3 backend4 nginx
  verify_nginx_workers h2
}

# up_http11 brings the four backends up plain and nginx up as plain HTTP/1.1.
up_http11() {
  BACKEND_TLS_CERT_FILE=""
  BACKEND_TLS_KEY_FILE=""
  NGINX_CONF=http11/roundrobin.conf
  "${COMPOSE[@]}" up -d --force-recreate backend1 backend2 backend3 backend4 nginx
  verify_nginx_workers http11
}

target_url() { # <proto> <competitor> <size>
  case "$1:$2" in
    http11:lb) printf 'http://lb:8080/%s' "$3" ;;
    http11:nginx) printf 'http://nginx:80/%s' "$3" ;;
    h2:lb) printf 'https://lb:8080/%s' "$3" ;;
    h2:nginx) printf 'https://nginx:443/%s' "$3" ;;
    *) warn "unknown target $1/$2"; return 1 ;;
  esac
}

# nginx_conf_for <proto> <algorithm> maps a scenario to its Nginx competitor by
# path join: the competitor configs mirror the LB's `<proto>/<algo>` layout
# (S5.T5.1–S5.T5.2). Every algorithm in the matrix now has a competitor, so the
# old solo path is gone.
nginx_conf_for() {
  printf '%s/%s.conf' "$1" "$2"
}

# comparison_for <algorithm> labels whether the Nginx competitor implements the
# LB's algorithm exactly (`matched`) or only approximates it
# (`nearest-equivalent`), per the CONTEXT.md glossary. The nearest-equivalent
# configs state their gap in their header comments.
comparison_for() {
  case "$1" in
    roundrobin|leastconn) printf 'matched' ;;
    consistent-hash|p2c-ewma) printf 'nearest-equivalent' ;;
  esac
}

# set_nginx_conf <conf-file> recreates nginx on a config and verifies its CPU
# pinning before the slice continues (the exported global is updated so later
# `compose run` calls stay consistent). The proto is the config path's leading
# segment, which is also the listener mode the readiness probe needs.
set_nginx_conf() {
  NGINX_CONF="$1"
  "${COMPOSE[@]}" up -d --force-recreate nginx
  verify_nginx_workers "${1%%/*}"
}

# ---------------------------------------------------------------------------
# Metrics parsing (vegeta report --type=json).
# ---------------------------------------------------------------------------

json_field() { # <file> <key>
  grep -o "\"$2\":[0-9.eE+-]*" "$1" 2>/dev/null | head -n1 | cut -d: -f2
}

ns_to_ms() { awk -v n="${1:-0}" 'BEGIN { printf "%.3f", n / 1e6 }'; }

# passes reports whether the metrics from the last attack clear the thresholds.
passes() {
  local p99 success
  p99=$(json_field "$TMP/metrics.json" 99th)
  success=$(json_field "$TMP/metrics.json" success)
  [[ -n "$p99" && -n "$success" ]] || return 1
  (( p99 < P99_CEILING_MS * 1000000 )) || return 1
  awk -v s="$success" -v e="$ERROR_CEILING_PCT" \
    'BEGIN { exit !((100 * (1 - s)) < e) }'
}

# ---------------------------------------------------------------------------
# Attacks.
# ---------------------------------------------------------------------------

# attack_filtered <url> <rate> <measure_secs> runs WARMUP+measure seconds,
# drops the warmup rows, and leaves the measurement's text report, HDR
# histogram, and JSON metrics in $TMP.
attack_filtered() {
  local url="$1" rate="$2" secs="$3"
  local total=$(( WARMUP_SECS + secs ))
  vegeta_run "
set -e
printf 'GET %s\n' '$url' | vegeta attack -rate=${rate}/s -duration=${total}s -insecure > /results/.tmp/attack.gob
vegeta encode -to csv /results/.tmp/attack.gob > /results/.tmp/attack.csv
c=\$(head -n1 /results/.tmp/attack.csv | cut -d, -f1)
c=\$((c + ${WARMUP_SECS} * 1000000000))
awk -F, -v c=\"\$c\" '\$1+0 >= c' /results/.tmp/attack.csv > /results/.tmp/meas.csv
vegeta encode -to gob /results/.tmp/meas.csv > /results/.tmp/meas.gob
vegeta report /results/.tmp/meas.gob > /results/.tmp/report.txt
vegeta report -type=hdrplot /results/.tmp/meas.gob > /results/.tmp/report.hdr
vegeta report -type=json /results/.tmp/meas.gob > /results/.tmp/metrics.json
"
}

# discover_peak <url> binary-searches the highest sustainable rate: seed at
# SEED_RATE, double until a step fails (p99 > ceiling or errors > ceiling),
# then bisect between the last good and first bad rate to RATE_GRANULARITY.
# Echoes the peak rate (0 when even the seed fails).
discover_peak() {
  local url="$1" good=0 bad=0 rate="$SEED_RATE" mid
  while (( rate <= MAX_RATE )); do
    attack_filtered "$url" "$rate" "$STEP_SECS"
    if passes; then
      good=$rate
      rate=$(( rate * 2 ))
    else
      bad=$rate
      break
    fi
  done
  if (( bad == 0 )); then
    bad=$MAX_RATE
  fi
  if (( good == 0 )); then
    printf '0'
    return
  fi
  while (( bad - good > RATE_GRANULARITY )); do
    mid=$(( (good + bad) / 2 / RATE_GRANULARITY * RATE_GRANULARITY ))
    (( mid > good )) || break
    attack_filtered "$url" "$mid" "$STEP_SECS"
    if passes; then good=$mid; else bad=$mid; fi
  done
  printf '%d' "$good"
}

save_result() { # <outdir> <basename> <metadata-line>
  local dir="$RESULTS/$1" base="$2" meta="$3"
  mkdir -p "$dir"
  { printf '# %s\n' "$meta"; cat "$TMP/report.txt"; } > "$dir/$base.txt"
  cp "$TMP/report.hdr" "$dir/$base.hdr"
}

add_summary() { # <algorithm> <size> <competitor> <load> [throughput-override]
  local algo="$1" size="$2" comp="$3" load="$4" tput="${5:-}" p50 p99
  p50=$(ns_to_ms "$(json_field "$TMP/metrics.json" 50th)")
  p99=$(ns_to_ms "$(json_field "$TMP/metrics.json" 99th)")
  if [[ -z "$tput" ]]; then
    tput=$(awk -v t="$(json_field "$TMP/metrics.json" throughput)" 'BEGIN { printf "%.0f", t }')
  fi
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$algo" "$size" "$comp" "$load" "$p50" "$p99" "$tput" >> "$TMP/summary.tsv"
}

# run_scenario discovers peak, writes the throughput result at peak, then a
# latency result sweeping LATENCY_RATES_PCT of peak. proto is a filename token
# ("http11" in the protocol slice, empty in core — spec §30).
run_scenario() { # <outdir> <proto> <algorithm> <size> <competitor> <url>
  local outdir="$1" proto="$2" algo="$3" size="$4" comp="$5" url="$6"
  local peak base latrate pct cmp
  cmp=$(comparison_for "$algo")
  peak=$(discover_peak "$url")
  if (( peak == 0 )); then
    warn "$algo/$size/$comp reached no sustainable rate at seed ${SEED_RATE}/s; skipped"
    return
  fi
  log "  $algo $size $comp: peak=${peak}/s"

  base="${algo}-${size}"
  [[ -n "$proto" ]] && base="${base}-${proto}"
  base="${base}-${comp}"

  attack_filtered "$url" "$peak" "$STEP_SECS"
  save_result "$outdir" "${base}-throughput" \
    "algorithm=$algo size=$size competitor=$comp load=throughput comparison=$cmp peak_rps=$peak"
  add_summary "$algo" "$size" "$comp" throughput "$peak"

  { printf '# algorithm=%s size=%s competitor=%s load=latency comparison=%s peak_rps=%s rates_pct=%s\n' \
      "$algo" "$size" "$comp" "$cmp" "$peak" "${LATENCY_RATES_PCT[*]}"; } \
    > "$RESULTS/$outdir/${base}-latency.txt"
  for pct in "${LATENCY_RATES_PCT[@]}"; do
    latrate=$(( peak * pct / 100 ))
    (( latrate > 0 )) || latrate=1
    attack_filtered "$url" "$latrate" "$STEP_SECS"
    { printf '\n# rate_rps=%s (%s%% of peak %s)\n' "$latrate" "$pct" "$peak"
      cat "$TMP/report.txt"; } >> "$RESULTS/$outdir/${base}-latency.txt"
    cp "$TMP/report.hdr" "$RESULTS/$outdir/${base}-latency-${pct}.hdr"
    add_summary "$algo" "$size" "$comp" "latency@${pct}%" ""
  done
}

# ---------------------------------------------------------------------------
# Slices.
# ---------------------------------------------------------------------------

run_core() {
  log "=== core slice: HTTP/2 (TLS+ALPN), 48 runs ==="
  : > "$TMP/summary.tsv"
  up_h2
  local algo size comp url nginx_conf
  for algo in "${ALGOS[@]}"; do
    set_lb "./configs/h2/$algo.yaml"
    nginx_conf=$(nginx_conf_for h2 "$algo")
    set_nginx_conf "$nginx_conf"
    for size in "${SIZES[@]}"; do
      for comp in lb nginx; do
        url=$(target_url h2 "$comp" "$size")
        run_scenario core "" "$algo" "$size" "$comp" "$url"
      done
    done
  done
}

run_protocol() {
  log "=== protocol slice: HTTP/1.1 round-robin, 12 runs ==="
  : > "$TMP/summary.tsv"
  up_http11
  set_lb "./configs/http11/roundrobin.yaml"
  local size comp url
  for size in "${SIZES[@]}"; do
    for comp in lb nginx; do
      url=$(target_url http11 "$comp" "$size")
      run_scenario protocol http11 roundrobin "$size" "$comp" "$url"
    done
  done
}

# failure_attack runs a raw (no warmup-filtered) attack and additionally keeps
# the live 1 Hz cumulative JSON report as a time series for recovery analysis.
failure_attack() { # <url> <rate> <secs>
  local url="$1" rate="$2" secs="$3"
  vegeta_run "
set -e
printf 'GET %s\n' '$url' | vegeta attack -rate=${rate}/s -duration=${secs}s -insecure \
  | tee /results/.tmp/failure.gob \
  | vegeta report -type=json --every=1s 2>&1 \
  | sed 's/\x1b\[[0-9;]*[A-Za-z]//g' > /results/.tmp/timeseries.jsonl
vegeta report /results/.tmp/failure.gob > /results/.tmp/report.txt
vegeta report -type=hdrplot /results/.tmp/failure.gob > /results/.tmp/report.hdr
vegeta report -type=json /results/.tmp/failure.gob > /results/.tmp/metrics.json
vegeta encode -to csv /results/.tmp/failure.gob > /results/.tmp/failure.csv
"
}

# recovery_second prints the first whole second at/after the event whose
# cumulative p99 is back within 10% of the pre-event second, or "none".
recovery_second() { # <timeseries> <event-second>
  awk -v ev="$2" '
    { if (match($0, /"99th":[0-9]+/)) { n++; v[n] = substr($0, RSTART+7, RLENGTH-7) + 0 } }
    END {
      if (n == 0) { print "n/a"; exit }
      b = (ev > 1 && ev <= n) ? v[ev-1] : v[1]
      rec = "none"
      for (i = ev; i <= n; i++) if (v[i] <= b * 1.1) { rec = i; break }
      print rec
    }' "$1"
}

max_p99_ms() { # <timeseries>
  awk '
    { if (match($0, /"99th":[0-9]+/)) { v = substr($0, RSTART+7, RLENGTH-7) + 0; if (v > m) m = v } }
    END { printf "%.3f", m / 1e6 }' "$1"
}

# failure_event <mode> <url> <rate> fires the event at T+FAILURE_EVENT_AT while
# the attack runs, then writes the failure result and its measurements.
failure_event() { # <kill|sighup> <url> <rate>
  local mode="$1" url="$2" rate="$3" scheduler
  rm -f "$TMP/event_epoch"
  case "$mode" in
    kill)
      ( sleep "$FAILURE_EVENT_AT"; date +%s > "$TMP/event_epoch"
        "${COMPOSE[@]}" stop backend3 >/dev/null 2>&1 ) &
      ;;
    sighup)
      ( sleep "$FAILURE_EVENT_AT"; date +%s > "$TMP/event_epoch"
        "${COMPOSE[@]}" kill -s HUP lb >/dev/null 2>&1 ) &
      ;;
    *) warn "unknown failure mode $mode"; return 1 ;;
  esac
  scheduler=$!

  failure_attack "$url" "$rate" "$FAILURE_SECS"
  wait "$scheduler" || warn "the $mode event command exited non-zero"

  local errors first last p50 p99 rec start_epoch event_epoch event_offset maxp drops detwindow
  read -r errors first last < <(awk -F, '
    NR == 1 { start = $1 }
    ($2 + 0 < 200 || $2 + 0 >= 300) {
      e++; off = ($1 - start) / 1e9
      if (first == "") first = off
      last = off
    }
    END { printf "%d %.3f %.3f\n", e + 0, (first == "" ? 0 : first), (last == "" ? 0 : last) }' \
    "$TMP/failure.csv")

  p50=$(ns_to_ms "$(json_field "$TMP/metrics.json" 50th)")
  p99=$(ns_to_ms "$(json_field "$TMP/metrics.json" 99th)")
  rec=$(recovery_second "$TMP/timeseries.jsonl" "$FAILURE_EVENT_AT")
  start_epoch=$(awk -F, 'NR == 1 { printf "%.0f", $1 / 1e9 }' "$TMP/failure.csv")
  event_epoch=$(cat "$TMP/event_epoch" 2>/dev/null || printf '0')
  event_offset=$(( event_epoch - start_epoch ))
  # time-to-detection: first error to the last error (the point the LB stopped
  # routing to the dead backend). Every error is inside [first, last], so the
  # detection-window count equals the total error count.
  detwindow=$(awk -v a="$first" -v b="$last" 'BEGIN { printf "%.3f", b - a }')

  local base analysis
  if [[ "$mode" == kill ]]; then
    base="roundrobin-10kb-backend-kill"
    analysis=$(printf 'measurements: mode=backend-kill event=docker-compose-stop-backend3 event_at_s=%s total_errors=%s detection_window_errors=%s error_first_s=%s error_last_s=%s time_to_detection_s=%s p50_ms=%s p99_ms=%s p99_recovery_s=%s' \
      "$event_offset" "$errors" "$errors" "$first" "$last" "$detwindow" "$p50" "$p99" "$rec")
  else
    base="roundrobin-10kb-sighup"
    drops=$errors
    maxp=$(max_p99_ms "$TMP/timeseries.jsonl")
    analysis=$(printf 'measurements: mode=sighup-unchanged-config event=docker-compose-kill-s-HUP-lb event_at_s=%s drops=%s p50_ms=%s p99_ms=%s p99_peak_ms=%s p99_recovery_s=%s' \
      "$event_offset" "$drops" "$p50" "$p99" "$maxp" "$rec")
  fi

  mkdir -p "$RESULTS/failure"
  { printf '# algorithm=roundrobin size=10kb competitor=lb load=%s comparison=%s rate_rps=%s\n' \
      "$mode" "$(comparison_for roundrobin)" "$rate"
    cat "$TMP/report.txt"
    printf '\n%s\n' "$analysis"; } > "$RESULTS/failure/$base.txt"
  cp "$TMP/report.hdr" "$RESULTS/failure/$base.hdr"
  { printf '# algorithm=roundrobin size=10kb competitor=lb load=%s comparison=%s rate_rps=%s\n' \
      "$mode" "$(comparison_for roundrobin)" "$rate"
    cat "$TMP/timeseries.jsonl"; } > "$RESULTS/failure/$base-timeseries.txt"
  add_summary roundrobin 10kb lb "$mode" "$rate"
  log "  failure $mode: errors=$errors event_at_s=$event_offset p99=${p99}ms"
}

run_failure() {
  log "=== failure slice: backend-kill + SIGHUP-under-load, 2 runs ==="
  : > "$TMP/summary.tsv"
  up_h2
  set_lb "./configs/h2/roundrobin.yaml"
  local url="https://lb:8080/10kb" peak rate
  peak=$(discover_peak "$url")
  if (( peak == 0 )); then
    warn "failure slice reached no sustainable rate at seed ${SEED_RATE}/s; skipped"
    return
  fi
  rate=$(( peak * FAILURE_RATE_PCT / 100 ))
  (( rate > 0 )) || rate=1
  log "  peak=${peak}/s, steady state=${rate}/s (${FAILURE_RATE_PCT}% of peak)"

  failure_event kill "$url" "$rate"
  log "  restarting backend3 and settling ${SETTLE_SECS}s ..."
  "${COMPOSE[@]}" start backend3 >/dev/null
  sleep "$SETTLE_SECS"
  failure_event sighup "$url" "$rate"
}

# ---------------------------------------------------------------------------
# Smoke slice (S5.T5.3).
# ---------------------------------------------------------------------------

# smoke_fail <combination> <reason> records a failed combination, naming it.
smoke_fail() { # <combination> <reason>
  warn "smoke FAILED: $1 — $2"
  SMOKE_FAILURES=$(( SMOKE_FAILURES + 1 ))
}

# wait_target <url> polls a single request until it succeeds, up to
# SMOKE_READY_TRIES. It separates "a container is still starting" from "the
# routing is broken", so a slow start is not misread as a failure.
wait_target() { # <url>
  local url="$1" i
  for (( i = 0; i < SMOKE_READY_TRIES; i++ )); do
    vegeta_run "printf 'GET %s\n' '$url' | vegeta attack -rate=1/s -duration=1s -insecure | vegeta report -type=json" \
      > "$TMP/ready.json" 2>/dev/null || true
    if awk -v s="$(json_field "$TMP/ready.json" success)" 'BEGIN { exit !(s > 0) }'; then
      return 0
    fi
  done
  return 1
}

# smoke_ok_200 <json> reports whether every request in the report came back 200.
# vegeta's own `success` counts 3xx as a success, but the smoke gate exists to
# prove the target "serves 200s", so a redirect is treated as a failure.
smoke_ok_200() { # <json>
  local requests ok
  requests=$(json_field "$1" requests)
  ok=$(grep -o '"200":[0-9]*' "$1" 2>/dev/null | head -n1 | cut -d: -f2)
  [[ -n "$requests" && "$requests" != 0 && "$ok" == "$requests" ]]
}

# smoke_attack <combination> <url> runs the constant-rate smoke attack and
# requires every request to be a 200, failing (and naming the combination)
# otherwise. The attack is the judge, not a compose exit code.
smoke_attack() { # <combination> <url>
  local combo="$1" url="$2" success pct
  if ! wait_target "$url"; then
    smoke_fail "$combo" "target never served a successful response"
    return
  fi
  vegeta_run "printf 'GET %s\n' '$url' | vegeta attack -rate=${SMOKE_RATE}/s -duration=${SMOKE_SECS}s -insecure | vegeta report -type=json" \
    > "$TMP/smoke.json" 2>/dev/null || true
  if smoke_ok_200 "$TMP/smoke.json"; then
    log "  smoke $combo OK (100%)"
    return
  fi
  success=$(json_field "$TMP/smoke.json" success)
  pct=$(awk -v s="${success:-0}" 'BEGIN { printf "%.2f", 100 * s }')
  smoke_fail "$combo" "success=${pct}% (need 100%)"
}

# smoke_proto <proto> <algorithm...> brings the protocol's backends and nginx
# up and checks both competitors for each algorithm. A set_lb that fails to
# start (compose error) is allowed to fail so the combination is still probed
# and named by smoke_attack; a CPU-pinning mismatch is not — that aborts the
# slice from inside set_lb/up_h2 (S5.T5.7.1).
smoke_proto() { # <proto> <algorithm...>
  local proto="$1"
  shift
  local algo comp url conf combo
  case "$proto" in
    h2) up_h2 ;;
    http11) up_http11 ;;
    *) warn "unknown smoke protocol $proto"; return 1 ;;
  esac
  for algo in "$@"; do
    set_lb "./configs/$proto/$algo.yaml" || true
    conf=$(nginx_conf_for "$proto" "$algo")
    if ! set_nginx_conf "$conf"; then
      smoke_fail "$proto/$algo/nginx" "nginx would not start on $conf"
      continue
    fi
    for comp in lb nginx; do
      combo="$proto/$algo/$comp"
      url=$(target_url "$proto" "$comp" "$SMOKE_SIZE")
      smoke_attack "$combo" "$url"
    done
  done
}

# run_smoke checks every (protocol, algorithm, competitor) combination the
# matrix uses and exits nonzero, naming each failed combination, if any does
# not serve 100% 200s. It is not part of `all`: it is the preflight gate for a
# published run.
run_smoke() {
  log "=== smoke slice: 10 attacks, ${SMOKE_SECS}s @ ${SMOKE_RATE}/s on /${SMOKE_SIZE} ==="
  SMOKE_FAILURES=0
  smoke_proto h2 "${ALGOS[@]}"
  smoke_proto http11 roundrobin
  if (( SMOKE_FAILURES > 0 )); then
    warn "smoke FAILED: ${SMOKE_FAILURES} combination(s) did not reach 100% success"
    exit 1
  fi
  log "smoke PASSED: all 10 combinations served 100%."
}

print_summary() {
  [[ -s "$TMP/summary.tsv" ]] || return 0
  printf '\n%-14s %-6s %-6s %-12s %9s %9s %12s\n' \
    algorithm size comp load "p50(ms)" "p99(ms)" "throughput"
  printf '%.0s-' {1..72}; printf '\n'
  awk -F'\t' '{ printf "%-14s %-6s %-6s %-12s %9s %9s %12s\n", $1, $2, $3, $4, $5, $6, $7 }' \
    "$TMP/summary.tsv"
  : > "$TMP/summary.tsv"
}

# ---------------------------------------------------------------------------
# Main.
# ---------------------------------------------------------------------------

SLICE="${1:-all}"
require_docker
mkdir -p "$TMP" "$RESULTS/core" "$RESULTS/protocol" "$RESULTS/failure"

case "$SLICE" in
  core) run_core; print_summary ;;
  protocol) run_protocol; print_summary ;;
  failure) run_failure; print_summary ;;
  smoke) run_smoke; exit 0 ;;
  all) run_core; print_summary; run_protocol; print_summary; run_failure; print_summary ;;
  -h|--help|help) usage; exit 0 ;;
  *) warn "unknown slice '$SLICE'"; usage; exit 2 ;;
esac

log ""
log "done. results under bench/results/${SLICE}/ (raw .gob/.csv in bench/results/.tmp/)"
log "tear the stack down with: docker compose -f bench/docker-compose.yml down"
