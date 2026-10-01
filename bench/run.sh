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
# Usage:  ./bench/run.sh [core|protocol|failure|degraded|smoke|all]   (default: all)
#
#   core      HTTP/2 (TLS+ALPN) matrix — 48 runs
#   protocol  HTTP/1.1 round-robin comparison — 12 runs
#   failure   backend-kill, no-op reload, drain reload — 3 runs
#   degraded  one backend +50 ms, per-backend distribution — 8 runs
#   smoke     a ~1-minute preflight over every (protocol, algorithm,
#             competitor) combination the matrix uses — 10 attacks, not part
#             of `all` (S5.T5.3)
#   all       the full 71-run matrix (smoke excluded)
#
# Every algorithm runs head-to-head with an Nginx competitor. Each result
# header records `comparison=matched` (roundrobin, leastconn) or
# `comparison=nearest-equivalent` (consistent-hash, p2c-ewma), and every
# nearest-equivalent competitor's header states its gap (S5.T5.2).
#
# Results land as .txt summaries + .hdr HDR histograms + .json reports under
# bench/results/{core,protocol,failure,degraded}/ with parameter-encoded filenames, and
# each slice prints a summary table to stdout (spec §27, §30, §32). Every
# invocation also writes bench/results/provenance.json — the commit, dirty
# state, host, tool versions, cpusets and start/finish times a reader needs to
# interpret the numbers (S5.T5.7.3).
#
# Progress (S5.T5.7.2) goes to stderr before every run, so the stdout summary
# tables stay clean for piping.
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
# No-op reload (S5.T9.1): the reload run re-reads an unchanged config and is
# judged PASS/FAIL against criteria fixed as constants before any run — zero
# non-2xx/transport errors over the whole run, and post-event p99 within
# RELOAD_P99_FACTOR of the same run's pre-event p99 (warmup to the event). The
# result file carries both windows' p99, the error counts and a verdict line
# naming any failed criterion, so the writeup reports a verdict, not a judgment
# call (spec §36, §37, §38, §44).
#
# Drain reload (S5.T9.2): the LB mounts a working copy of the committed
# round-robin h2 config in the results scratch area and rewrites it in place
# (cp over the same inode, never a rename) to drop backend4 at T+30s, then
# SIGHUPs. The committed configs are never modified. It passes only if the
# no-op criteria hold and backend4's own arrival counter does not move between
# "reload applied" and the end of the run — the "zero drops while draining /
# never selected again" claim, verified from the backend's side (spec §35, §39,
# §40, §41). The counter is sampled just before the signal, once the LB logs
# config_reloaded, and at the end; the first interval is recorded, only the
# second is judged. After the run the LB is recreated on the committed config.
#
# The smoke slice (S5.T5.3) is the preflight gate: for every (protocol,
# algorithm, competitor) combination the matrix uses, it runs a short low-rate
# attack on the 10 KiB endpoint and requires 100% success, failing with a
# nonzero exit naming the combination otherwise. It catches broken configs in
# about a minute (the class of routing bug that once had TLS upstreams return
# 400) instead of hours into a run. It is deliberately not part of `all`.
#
# Hot key (S5.T8.1): the load generator has one address, so every consistent-hash
# run is a hot-key scenario — one hash key. A result therefore records where the
# traffic actually went: the per-backend arrival-counter deltas as counts and
# shares, the plurality owner and whether spill occurred. The snapshots bracket
# each measured attack, so they add no load during measurement; the reads go
# straight to the backends and /stats is not counted (S5.T5.5.2).
#
# Degraded (S5.T8.2): backend3 is recreated 50 ms slow for the whole slice and
# restored on exit, including on failure. Every one of the eight runs (four
# algorithms × two competitors, h2, 10 KiB) is bracketed by arrival snapshots,
# so the headline result is the per-backend share — which algorithms route around
# a slow backend and which do not. All eight run at one absolute rate, half the
# h2/round-robin/10 KiB LB peak: reused from the core slice when the same
# invocation ran it, discovered here (with the backends still fast) otherwise.
# The active health checker probes each backend's own URL, so backend3's probe
# also waits 50 ms, but that is far below the checker's 2 s probe timeout — the
# delay alone never ejects it.

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
RELOAD_P99_FACTOR=2       # no-op reload: post-event p99 must be <= factor x pre-event p99 (S5.T9.1)

# Drain reload (S5.T9.2): backend4 is removed under load; its own arrival counter
# must not move once the reload is applied. The applied signal is the LB's
# config_reloaded log line, polled after the SIGHUP until it is seen.
DRAIN_BACKEND=backend4    # the backend the drain reload removes
DRAIN_POLL_TRIES=100      # config_reloaded polls after SIGHUP
DRAIN_POLL_SLEEP=0.2      # seconds between polls

# Hot-key spill (S5.T8.1). The active health checker's probes are counted
# arrivals — the probe target is the backend's own URL — and they land on every
# backend, so a bare "more than one backend received traffic" would read yes on
# every run and show nothing. A non-owner backend counts as spill only above
# this share floor; the probe background (a few arrivals per window) stays far
# below it at benchmark rates, while a real bounded-loads spill is far above.
SPILL_MIN_SHARE_PCT=1

# Degraded slice (S5.T8.2): one backend is 50 ms slow for the whole slice and
# the headline result is where the traffic went, not only the aggregate latency.
# All eight runs use one absolute rate — half the h2/round-robin/10 KiB LB peak —
# so their distributions and latencies are directly comparable.
DEGRADED_BACKEND=backend3  # the backend recreated with the injected delay
DEGRADED_SLEEP_MS=50       # its injected delay, in milliseconds
DEGRADED_SECS=30           # measured window per run (plus the standard warmup)
DEGRADED_RATE_PCT=50       # absolute rate, as % of the h2/roundrobin/10kb LB peak
DEGRADED_SIZE=10kb         # the one size the degraded slice measures

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

# The dummy backends expose a per-process arrival counter at /stats (S5.T5.5.2).
# The harness reads them directly for the hot-key distribution (S5.T8.1); the
# names must match the compose service/`-name` values in bench/docker-compose.yml.
BACKENDS=(backend1 backend2 backend3 backend4)
BACKEND_PORT=8080

# Runs per slice, for the progress line's total (S5.T5.7.2). One "run" is one
# result set: a scenario emits a peak-search run (peak discovery plus the
# throughput measurement at peak) and a latency run (the whole rate sweep).
# Keep these in step with the slice functions — a run added there is a run
# added here.
CORE_RUNS=$(( ${#ALGOS[@]} * ${#SIZES[@]} * 2 * 2 ))   # algos × sizes × competitors × {peak, latency}
PROTOCOL_RUNS=$(( 1 * ${#SIZES[@]} * 2 * 2 ))
FAILURE_RUNS=3
DEGRADED_RUNS=$(( ${#ALGOS[@]} * 2 ))    # algos × competitors, one measured run each
SMOKE_RUNS=$(( ${#ALGOS[@]} * 2 + 2 ))   # h2 × algos × competitors + http11/roundrobin × competitors

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
# The degraded slice raises SLEEP_MS to 50 before force-recreating backend3 only
# (S5.T8.2); the compose anchor reads it. Reset to 0 everywhere else.
# shellcheck disable=SC2034
export SLEEP_MS=0

# The h2/round-robin/10 KiB LB peak, captured by the core slice so the degraded
# slice can reuse the same invocation's measurement (S5.T8.2). Empty when the
# core slice did not run, which makes the degraded slice discover its own.
CORE_RR_10KB_LB_PEAK=""
DEGRADED_BACKEND_SLOW=0

usage() {
  cat <<'EOF'
bench/run.sh — the benchmark execution harness (S5.T4-harness).

Usage: bench/run.sh [core|protocol|failure|degraded|smoke|all]   (default: all)

  core      HTTP/2 (TLS+ALPN) matrix — 48 runs
  protocol  HTTP/1.1 round-robin comparison — 12 runs
  failure   backend-kill, no-op reload and drain reload — 3 runs
  degraded  one backend +50 ms, per-backend distribution — 8 runs
  smoke     preflight: every (protocol, algorithm, competitor) combination
            the matrix uses, 10 short attacks, 100% required, not part of all
  all       the full 71-run matrix (smoke excluded)

Results land as .txt summaries + .hdr HDR histograms + .json reports under
bench/results/{core,protocol,failure,degraded}/ with parameter-encoded filenames, and
each slice prints a summary table to stdout. All parameters are constants at
the top of this script; see the header comment for the methodology.
EOF
}

log() { printf '%s\n' "$*"; }
warn() { printf 'WARN: %s\n' "$*" >&2; }

# Progress (S5.T5.7.2): one stderr line before every run, so a multi-hour run is
# not mistaken for a hang. stdout stays clean for piping (the summary tables).
# The total is the sum of the selected slices, so it is right for a single
# slice, smoke, and all. The ETA is deliberately naive — elapsed × total /
# completed — and shown as `--` until the first run finishes.
RUN_TOTAL=0
RUN_DONE=0
RUN_START=0

# run_total <slice> prints the number of runs the slice will perform.
run_total() {
  case "$1" in
    core)     printf '%d' "$CORE_RUNS" ;;
    protocol) printf '%d' "$PROTOCOL_RUNS" ;;
    failure)  printf '%d' "$FAILURE_RUNS" ;;
    degraded) printf '%d' "$DEGRADED_RUNS" ;;
    smoke)    printf '%d' "$SMOKE_RUNS" ;;
    all)      printf '%d' "$(( CORE_RUNS + PROTOCOL_RUNS + FAILURE_RUNS + DEGRADED_RUNS ))" ;;
    *)        printf '0' ;;
  esac
}

fmt_hms() { # <seconds> -> HH:MM:SS
  local s="${1:-0}"
  printf '%02d:%02d:%02d' "$(( s / 3600 ))" "$(( (s % 3600) / 60 ))" "$(( s % 60 ))"
}

# progress <proto> <algorithm> <size> <competitor> <load-type> prints the
# ordinal, scenario, elapsed and ETA to stderr, then advances the counter.
progress() {
  local n elapsed eta
  n=$(( RUN_DONE + 1 ))
  RUN_DONE=$n
  elapsed=$(( $(date +%s) - RUN_START ))
  if (( n == 1 )); then
    eta='--'
  else
    eta=$(fmt_hms "$(( elapsed * RUN_TOTAL / (n - 1) ))")
  fi
  printf '[run %d/%d] %s/%s/%s/%s %s  elapsed %s  eta %s\n' \
    "$n" "$RUN_TOTAL" "$1" "$2" "$3" "$4" "$5" "$(fmt_hms "$elapsed")" "$eta" >&2
}

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

# compose_sh <script> runs a script in a one-off vegeta container on the compose
# network with `sh` as the entrypoint. The vegeta image is alpine, so busybox
# wget is available for the arrival-counter reads. The exported compose
# variables keep the running lb/nginx/backends on the config this slice
# selected, so `compose run` never recreates them.
compose_sh() {
  "${COMPOSE[@]}" run --rm -T --entrypoint sh vegeta -c "$1"
}

# vegeta_run <command> runs a shell command inside a one-off vegeta container
# with the results directory mounted at /results.
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
# Arrival-counter distribution (S5.T8.1). The dummy backends expose a
# per-process arrival counter at /stats (S5.T5.5.2). A single-address load
# generator is one hash key, so every consistent-hash run is a hot-key
# scenario: an unbounded ring pins all traffic to one backend, while bounded
# loads spills the excess (CONTEXT.md Hot key). Bracketing each measured attack
# with /stats reads records where the traffic actually went, without touching
# the measured window: the reads go straight to the backends (never through the
# load balancer) and /stats is not counted.
# ---------------------------------------------------------------------------

# backend_scheme <proto> prints the scheme the backends serve on for a slice:
# the h2 slice runs TLS backends, everything else plain (S5.T4-infra).
backend_scheme() {
  case "$1" in
    h2) printf 'https' ;;
    *)  printf 'http' ;;
  esac
}

# read_arrivals <scheme> <outfile> reads every backend's /stats in a single
# one-off vegeta container and writes "<name>\t<count>" lines. A read that comes
# back empty is an error, and because the caller runs under `set -e` the run
# aborts rather than feed a partial snapshot to the diff.
read_arrivals() {
  local scheme="$1" out="$2" b cmd='set -e; '
  for b in "${BACKENDS[@]}"; do
    cmd+="v=\$(wget -qO- --no-check-certificate '${scheme}://${b}:${BACKEND_PORT}/stats' | sed -n 's/.*\"requests\":\([0-9]*\).*/\1/p'); "
    cmd+="[ -n \"\$v\" ] || { echo 'arrival read failed: ${b}' >&2; exit 1; }; "
    cmd+="printf '${b}\t%s\n' \"\$v\"; "
  done
  compose_sh "$cmd" > "$out"
}

# arrival_deltas <before> <after> prints "<name>\t<delta>\t<share_pct>" per
# backend: arrivals during the run and each backend's share of the total. This
# is the reusable differ: the degraded slice (S5.T8.2) and the drain reload
# (S5.T9.2) feed it their own snapshots.
arrival_deltas() {
  awk -F'\t' '
    FNR == NR { before[$1] = $2 + 0; next }
    {
      d = $2 - before[$1]; if (d < 0) d = 0
      delta[$1] = d; total += d; names[++n] = $1
    }
    END {
      for (i = 1; i <= n; i++) {
        name = names[i]
        share = (total > 0) ? 100 * delta[name] / total : 0
        printf "%s\t%d\t%.2f\n", name, delta[name], share
      }
    }
  ' "$1" "$2"
}

# distribution_block <deltas> prints the per-backend distribution as
# result-header comment lines: "# arrivals backend=<name> count=<n>
# share_pct=<p>", one per backend. It reads arrival_deltas output.
distribution_block() {
  local name count share
  while IFS=$'\t' read -r name count share; do
    printf '# arrivals backend=%s count=%s share_pct=%s\n' "$name" "$count" "$share"
  done < "$1"
}

# hotkey_block <deltas> prints one "# hotkey owner=<name> spill=<yes|no>" line:
# the plurality backend is the owner, and spill means a non-owner backend
# received at least SPILL_MIN_SHARE_PCT of the traffic (bounded loads working).
# The raw counts stay in the result too, so the exact distribution is never
# hidden behind the flag. It reads arrival_deltas output.
hotkey_block() {
  awk -F'\t' -v floor="$SPILL_MIN_SHARE_PCT" '
    { name[NR] = $1; share[NR] = $3 + 0
      if ($2 + 0 > max) { max = $2 + 0; owner = $1 } }
    END {
      spill = "no"
      for (i = 1; i <= NR; i++) if (name[i] != owner && share[i] >= floor) spill = "yes"
      printf "# hotkey owner=%s spill=%s\n", (owner == "" ? "none" : owner), spill
    }' "$1"
}

# snapshot_block <label> <snapshot> prints the raw (not differenced) per-backend
# /stats counts as result-header comment lines: "# snapshot=<label>
# backend=<name> count=<n>". The drain reload emits its three snapshots this way
# so the generator reads the arrivals at each event point, not just the deltas.
snapshot_block() { # <label> <snapshot-file>
  local label="$1" name count
  while IFS=$'\t' read -r name count; do
    printf '# snapshot=%s backend=%s count=%s\n' "$label" "$name" "$count"
  done < "$2"
}

# measured_attack <scheme> <url> <rate> <secs> [hotkey] runs one measured attack,
# bracketed by arrival-counter snapshots when the scheme is non-empty. The
# distribution lands in $TMP/distribution.txt, empty when the scheme is empty so
# a non-bracketed algorithm's result is unchanged. `hotkey` defaults to yes (the
# core slice brackets only consistent-hash, which is always a hot key); the
# degraded slice brackets every algorithm but asks for the hot-key line only on
# consistent-hash. The deltas are computed once, in the same file-based style
# attack_filtered uses.
measured_attack() {
  local scheme="$1" url="$2" rate="$3" secs="$4" hotkey="${5:-yes}"
  : > "$TMP/distribution.txt"
  [[ -n "$scheme" ]] && read_arrivals "$scheme" "$TMP/arrivals-before.tsv"
  attack_filtered "$url" "$rate" "$secs"
  if [[ -n "$scheme" ]]; then
    read_arrivals "$scheme" "$TMP/arrivals-after.tsv"
    arrival_deltas "$TMP/arrivals-before.tsv" "$TMP/arrivals-after.tsv" > "$TMP/arrivals-delta.tsv"
    distribution_block "$TMP/arrivals-delta.tsv" > "$TMP/distribution.txt"
    if [[ "$hotkey" == yes ]]; then
      hotkey_block "$TMP/arrivals-delta.tsv" >> "$TMP/distribution.txt"
    fi
  fi
}

# ---------------------------------------------------------------------------
# Metrics parsing (vegeta report --type=json).
# ---------------------------------------------------------------------------

json_field() { # <file> <key>
  grep -o "\"$2\":[0-9.eE+-]*" "$1" 2>/dev/null | head -n1 | cut -d: -f2
}

ns_to_ms() { awk -v n="${1:-0}" 'BEGIN { printf "%.3f", n / 1e6 }'; }

# result_peak <throughput-txt> prints the peak_rps= value from a result header
# (empty when the file is absent or carries none).
result_peak() {
  sed -n 's/.* peak_rps=\([0-9][0-9]*\).*/\1/p' "$1" 2>/dev/null | head -n1 || true
}

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
# Provenance record (S5.T5.7.3). Every invocation writes
# bench/results/provenance.json when it starts and updates it when it finishes
# (an EXIT trap, so a slice that aborts still leaves a record). It is a
# development tool: it *records* a dirty tree or running containers, it never
# refuses them — enforcement lives at the publication boundary (the reproducer
# S5.T12 and the results generator S5.T10), which refuse a dirty `git_dirty`.
# ---------------------------------------------------------------------------

PROVENANCE="$RESULTS/provenance.json"
PROV_GIT_SHA=""
PROV_GIT_DIRTY="false"
PROV_SLICES="[]"
PROV_START_TS=""
PROV_FINISH_TS=""
PROV_DOCKER_CPUS=""
PROV_DOCKER_MEM=""
PROV_LB_GO_VERSION=""
PROV_LB_GOMAXPROCS=""
PROV_NGINX_VERSION=""
PROV_NGINX_WORKERS=""
PROV_VEGETA_VERSION=""

iso_now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

# json_escape <string> escapes backslashes, quotes and newlines for a JSON
# string value (host CPU models can contain characters that need it).
json_escape() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/ }"
  s="${s//$'\r'/}"
  printf '%s' "$s"
}

json_string_or_null() { # <string>
  if [[ -n "$1" ]]; then printf '"%s"' "$(json_escape "$1")"; else printf 'null'; fi
}

json_int_or_null() { # <value>
  if [[ "$1" =~ ^[0-9]+$ ]]; then printf '%s' "$1"; else printf 'null'; fi
}

# json_str_field <file> <key> extracts a JSON *string* field's value. json_field
# (above) only matches numerics, so the string fields need their own reader.
json_str_field() {
  grep -o "\"$2\":\"[^\"]*\"" "$1" 2>/dev/null | head -n1 | cut -d: -f2- | tr -d '"'
}

host_os() { uname -srm; }

# host_cpu reads the CPU model on the host (the harness runs there): macOS via
# sysctl, Linux via /proc/cpuinfo, falling back to the architecture.
host_cpu() {
  local cpu=""
  if [[ "$(uname -s)" == "Darwin" ]]; then
    cpu=$(sysctl -n machdep.cpu.brand_string 2>/dev/null || true)
  fi
  if [[ -z "$cpu" && -r /proc/cpuinfo ]]; then
    cpu=$(awk -F': ' '/model name/ { print $2; exit }' /proc/cpuinfo)
  fi
  printf '%s' "${cpu:-$(uname -m)}"
}

docker_version() {
  docker version --format '{{.Server.Version}}' 2>/dev/null \
    || docker version --format '{{.Client.Version}}' 2>/dev/null \
    || true
}

compose_version() { docker compose version --short 2>/dev/null || true; }

# cpusets_json renders the per-service cpuset split from the resolved compose
# config, so the record states what the rig is actually pinned to.
cpusets_json() {
  local raw
  raw=$("${COMPOSE[@]}" config 2>/dev/null | awk '
    /^  [A-Za-z0-9_.-]+:/ && $0 !~ /^    / { svc=$1; sub(/:$/, "", svc) }
    /^    cpuset:/ { v=$2; gsub(/"/, "", v); if (n++) printf ", "; printf "\"%s\": \"%s\"", svc, v }
  ')
  printf '{%s}' "$raw"
}

# vegeta_version reads the pinned source tag the vegeta image is built from.
# The `go install`-built binary carries no version metadata (`vegeta -version`
# prints an empty Version), so the Dockerfile pin is the authoritative version.
vegeta_version() {
  grep -o 'vegeta/v12@v[0-9][^ ]*' "$ROOT/bench/vegeta/Dockerfile" 2>/dev/null \
    | head -n1 | sed 's/.*@//'
}

provenance_slices() { # <slice> — the slices actually run (`all` expands).
  case "$1" in
    all) printf '["core", "protocol", "failure", "degraded"]' ;;
    *)   printf '["%s"]' "$1" ;;
  esac
}

# gather_runtime_facts fills the fields only knowable once the containers exist.
# Best-effort: a container that is not up leaves its field null. Called at start
# and again when the record is finalized.
gather_runtime_facts() {
  PROV_DOCKER_CPUS=$(docker info --format '{{.NCPU}}' 2>/dev/null || true)
  PROV_DOCKER_MEM=$(docker info --format '{{.MemTotal}}' 2>/dev/null || true)
  "${COMPOSE[@]}" logs --no-color lb > "$TMP/lb.log" 2>/dev/null || true
  PROV_LB_GO_VERSION=$(json_str_field "$TMP/lb.log" go_version || true)
  PROV_LB_GOMAXPROCS=$(json_field "$TMP/lb.log" gomaxprocs || true)
  if "${COMPOSE[@]}" ps -q nginx 2>/dev/null | grep -q .; then
    PROV_NGINX_VERSION=$("${COMPOSE[@]}" exec -T nginx nginx -v 2>&1 \
      | sed -n 's#.*nginx/##p' | tr -d '\r' | head -n1 || true)
    PROV_NGINX_WORKERS=$("${COMPOSE[@]}" exec -T nginx ps 2>/dev/null \
      | grep -c '[n]ginx: worker process' || true)
  fi
}

# write_provenance renders the record in place. Called once at start (runtime
# fields null) and once from the EXIT trap (populated).
write_provenance() {
  local finish_json
  if [[ -n "$PROV_FINISH_TS" ]]; then finish_json=$(json_string_or_null "$PROV_FINISH_TS"); else finish_json='null'; fi
  mkdir -p "$RESULTS"
  cat > "$PROVENANCE" <<JSON
{
  "git_sha": $(json_string_or_null "$PROV_GIT_SHA"),
  "git_dirty": $PROV_GIT_DIRTY,
  "slices": $PROV_SLICES,
  "start_time": $(json_string_or_null "$PROV_START_TS"),
  "finish_time": $finish_json,
  "host_os": $(json_string_or_null "$(host_os)"),
  "host_cpu": $(json_string_or_null "$(host_cpu)"),
  "docker_version": $(json_string_or_null "$(docker_version)"),
  "compose_version": $(json_string_or_null "$(compose_version)"),
  "docker_cpus": $(json_int_or_null "$PROV_DOCKER_CPUS"),
  "docker_memory_bytes": $(json_int_or_null "$PROV_DOCKER_MEM"),
  "cpusets": $(cpusets_json),
  "lb_go_version": $(json_string_or_null "$PROV_LB_GO_VERSION"),
  "lb_gomaxprocs": $(json_int_or_null "$PROV_LB_GOMAXPROCS"),
  "nginx_version": $(json_string_or_null "$PROV_NGINX_VERSION"),
  "nginx_workers": $(json_int_or_null "$PROV_NGINX_WORKERS"),
  "vegeta_version": $(json_string_or_null "$PROV_VEGETA_VERSION")
}
JSON
}

# provenance_start captures the immutable facts before any result is written
# (git state, start time, the slices, the vegeta pin), records the initial file,
# and arms the EXIT trap that finalizes it.
provenance_start() {
  PROV_GIT_SHA=$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || printf 'unknown')
  # bench/results/ is the harness's own output, not source: exclude it so a
  # repeated invocation does not see the previous run's record/results as a
  # dirty tree. `git_dirty` is about the code the numbers came from. The
  # published run starts from a fresh clone, so this changes nothing there.
  if [[ -n "$(git -C "$ROOT" status --porcelain -- ':!bench/results' 2>/dev/null)" ]]; then
    PROV_GIT_DIRTY=true
  else
    PROV_GIT_DIRTY=false
  fi
  PROV_SLICES=$(provenance_slices "$SLICE")
  PROV_START_TS=$(iso_now)
  PROV_VEGETA_VERSION=$(vegeta_version)
  gather_runtime_facts
  write_provenance
  trap provenance_finish EXIT
}

provenance_finish() {
  PROV_FINISH_TS=$(iso_now)
  gather_runtime_facts
  write_provenance 2>/dev/null || true
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
  # The JSON report is the machine-readable source for the percentiles in the
  # published tables (S5.T10.1); the text report above is its human-readable
  # sibling and the `.hdr` carries the p99.9 the JSON report does not.
  cp "$TMP/metrics.json" "$dir/$base.json"
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
# ("http11" in the protocol slice, empty in core — spec §30). Consistent-hash
# runs are hot-key scenarios: every measured attack is bracketed by
# arrival-counter snapshots and the distribution rides in the result file
# (S5.T8.1).
run_scenario() { # <outdir> <proto> <algorithm> <size> <competitor> <url>
  local outdir="$1" proto="$2" algo="$3" size="$4" comp="$5" url="$6"
  local peak base latrate pct cmp proto_label="${proto:-h2}" dist_scheme="" meta
  cmp=$(comparison_for "$algo")
  if [[ "$algo" == consistent-hash ]]; then
    dist_scheme=$(backend_scheme "$proto_label")
  fi
  progress "$proto_label" "$algo" "$size" "$comp" peak-search
  peak=$(discover_peak "$url")
  if (( peak == 0 )); then
    warn "$algo/$size/$comp reached no sustainable rate at seed ${SEED_RATE}/s; skipped"
    return
  fi
  log "  $algo $size $comp: peak=${peak}/s"

  base="${algo}-${size}"
  [[ -n "$proto" ]] && base="${base}-${proto}"
  base="${base}-${comp}"

  measured_attack "$dist_scheme" "$url" "$peak" "$STEP_SECS"
  meta="algorithm=$algo size=$size competitor=$comp load=throughput comparison=$cmp peak_rps=$peak"
  [[ -s "$TMP/distribution.txt" ]] && meta="$meta
$(cat "$TMP/distribution.txt")"
  save_result "$outdir" "${base}-throughput" "$meta"
  add_summary "$algo" "$size" "$comp" throughput "$peak"

  { printf '# algorithm=%s size=%s competitor=%s load=latency comparison=%s peak_rps=%s rates_pct=%s\n' \
      "$algo" "$size" "$comp" "$cmp" "$peak" "${LATENCY_RATES_PCT[*]}"; } \
    > "$RESULTS/$outdir/${base}-latency.txt"
  progress "$proto_label" "$algo" "$size" "$comp" latency
  for pct in "${LATENCY_RATES_PCT[@]}"; do
    latrate=$(( peak * pct / 100 ))
    (( latrate > 0 )) || latrate=1
    measured_attack "$dist_scheme" "$url" "$latrate" "$STEP_SECS"
    { printf '\n# rate_rps=%s (%s%% of peak %s)\n' "$latrate" "$pct" "$peak"
      [[ -s "$TMP/distribution.txt" ]] && cat "$TMP/distribution.txt"
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
  # Capture the h2/round-robin/10 KiB LB peak for the degraded slice. Set here,
  # from the result just written, so it is only non-empty when the core slice ran
  # in this invocation (S5.T8.2); a stale file from a previous run is ignored.
  CORE_RR_10KB_LB_PEAK=$(result_peak "$RESULTS/core/roundrobin-10kb-lb-throughput.txt")
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

# Reload runs (S5.T9.1). The no-op reload re-reads an unchanged config, so it
# measures the reload path's own cost. Its verdict is computed here from fixed
# thresholds, never left to the writeup.

# inode_of <file> prints the file's inode. The drain reload must rewrite the
# mounted config in place (cp over the same inode); a rename would swap the inode
# under the bind mount and the LB would keep reading the old file, so the run
# would pass while testing nothing (S5.T9.2). Darwin and Linux stat differ.
inode_of() { # <file>
  case "$(uname -s)" in
    Darwin) stat -f %i "$1" ;;
    *)      stat -c %i "$1" ;;
  esac
}

# reload_seen_count refreshes the LB's log file and prints how many successful
# reload lines it contains. config_reloaded is emitted once per applied SIGHUP
# (internal/logger), so a count that grows across a signal means the drain
# reload took effect. grep -c prints 0 and exits nonzero when there is no match.
reload_seen_count() {
  "${COMPOSE[@]}" logs --no-color lb > "$TMP/lb.log" 2>/dev/null || true
  grep -c 'config_reloaded' "$TMP/lb.log" 2>/dev/null || true
}

# reload_window_reports <warmup-secs> <event-secs> splits the run's CSV into the
# pre-event window (the end of warmup to the event) and the post-event window
# (the event to the end) and writes each window's vegeta JSON report to
# $TMP/pre.json and $TMP/post.json. The percentiles come from vegeta, so they
# are HDR-consistent with every other p99 in the results.
reload_window_reports() { # <warmup-secs> <event-secs>
  local warmup="$1" event="$2"
  vegeta_run "
set -e
start=\$(head -n1 /results/.tmp/failure.csv | cut -d, -f1)
pre=\$(( start + ${warmup} * 1000000000 ))
post=\$(( start + ${event} * 1000000000 ))
awk -F, -v a=\"\$pre\" -v b=\"\$post\" '\$1+0 >= a && \$1+0 < b' /results/.tmp/failure.csv > /results/.tmp/pre.csv
awk -F, -v b=\"\$post\" '\$1+0 >= b' /results/.tmp/failure.csv > /results/.tmp/post.csv
vegeta encode -to gob /results/.tmp/pre.csv | vegeta report -type=json > /results/.tmp/pre.json
vegeta encode -to gob /results/.tmp/post.csv | vegeta report -type=json > /results/.tmp/post.json
"
}

# reload_verdict <errors> <transport-errors> <pre-p99-ms> <post-p99-ms>
# [extra-failed] prints PASS, or FAIL naming each unmet criterion. The criteria
# and the p99 factor are the constants fixed before any run (S5.T9.1): zero
# non-2xx responses, zero transport errors, and post-event p99 within
# RELOAD_P99_FACTOR of the pre-event p99. The drain reload (S5.T9.2) passes its
# own extra criterion name here so one verdict line names every failure.
reload_verdict() { # <errors> <transport-errors> <pre-p99-ms> <post-p99-ms> [extra-failed]
  local errors="$1" transport="$2" pre="$3" post="$4" extra="${5:-}" failed=""
  if (( errors > 0 )); then failed="non_2xx"; fi
  if (( transport > 0 )); then failed="${failed:+$failed,}transport_errors"; fi
  if ! awk -v post="$post" -v pre="$pre" -v f="$RELOAD_P99_FACTOR" \
       'BEGIN { exit !((post + 0) <= f * (pre + 0)) }'; then
    failed="${failed:+$failed,}p99_factor"
  fi
  if [[ -n "$extra" ]]; then failed="${failed:+$failed,}$extra"; fi
  if [[ -z "$failed" ]]; then printf 'PASS'; else printf 'FAIL failed=%s' "$failed"; fi
}

# failure_event <mode> <url> <rate> fires the event at T+FAILURE_EVENT_AT while
# the attack runs, then writes the failure result and its measurements. `kill`
# is unchanged; `sighup` is the no-op reload (`sighup-noop`) and `drain` is the
# drain reload (`sighup-drain`), each judged against the fixed reload criteria
# (S5.T9.1, S5.T9.2).
failure_event() { # <kill|sighup|drain> <url> <rate>
  local mode="$1" url="$2" rate="$3" scheduler label
  case "$mode" in
    sighup) label=sighup-noop ;;
    drain)  label=sighup-drain ;;
    *)      label="$mode" ;;
  esac
  progress h2 roundrobin 10kb lb "$label"
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
    drain)
      # In-place rewrite then SIGHUP, with the three arrival snapshots. A rename
      # would leave the bind mount on the old inode, so the inode is asserted
      # unchanged and the run aborts otherwise (spec §41).
      ( sleep "$FAILURE_EVENT_AT"; date +%s > "$TMP/event_epoch"
        before=$(inode_of "$TMP/reload-config.yaml")
        cp "$TMP/drain-config.yaml" "$TMP/reload-config.yaml"
        after=$(inode_of "$TMP/reload-config.yaml")
        if [[ "$before" != "$after" ]]; then
          echo "drain reload: config inode changed ($before -> $after); the rewrite must be in place (cp), not a rename" >&2
          exit 1
        fi
        read_arrivals "$(backend_scheme h2)" "$TMP/drain-snap1.tsv"
        rm -f "$TMP/drain-applied"
        seen=$(reload_seen_count)
        "${COMPOSE[@]}" kill -s HUP lb >/dev/null 2>&1
        now=$seen
        for ((i = 0; i < DRAIN_POLL_TRIES; i++)); do
          now=$(reload_seen_count)
          if (( now > seen )); then break; fi
          sleep "$DRAIN_POLL_SLEEP"
        done
        if (( now > seen )); then
          : > "$TMP/drain-applied"
        else
          warn "drain reload: config_reloaded not seen after SIGHUP; the run fails its applied criterion"
        fi
        read_arrivals "$(backend_scheme h2)" "$TMP/drain-snap2.tsv" ) &
      ;;
    *) warn "unknown failure mode $mode"; return 1 ;;
  esac
  scheduler=$!

  failure_attack "$url" "$rate" "$FAILURE_SECS"
  if ! wait "$scheduler"; then
    warn "the $mode event command exited non-zero"
    if [[ "$mode" == drain ]]; then exit 1; fi
  fi
  # Snapshot 3: the end of the run.
  if [[ "$mode" == drain ]]; then read_arrivals "$(backend_scheme h2)" "$TMP/drain-snap3.tsv"; fi

  local errors transport first last p50 p99 rec start_epoch event_epoch event_offset maxp drops detwindow
  read -r errors transport first last < <(awk -F, '
    NR == 1 { start = $1 }
    { code = $2 + 0
      if (code == 0) t++
      if (code < 200 || code >= 300) {
        e++; off = ($1 - start) / 1e9
        if (first == "") first = off
        last = off
      } }
    END { printf "%d %d %.3f %.3f\n", e + 0, t + 0, (first == "" ? 0 : first), (last == "" ? 0 : last) }' \
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

  local base analysis verdict verdict_label non_2xx pre_p99 post_p99 before_applied applied_end extra
  case "$mode" in
    kill)
      base="roundrobin-10kb-backend-kill"
      verdict=""
      verdict_label=""
      analysis=$(printf 'measurements: mode=backend-kill event=docker-compose-stop-backend3 event_at_s=%s total_errors=%s detection_window_errors=%s error_first_s=%s error_last_s=%s time_to_detection_s=%s p50_ms=%s p99_ms=%s p99_recovery_s=%s' \
        "$event_offset" "$errors" "$errors" "$first" "$last" "$detwindow" "$p50" "$p99" "$rec")
      ;;
    sighup|drain)
      drops=$errors
      # `errors` counts every failed request, transport errors included (they are
      # status 0). The two judged criteria are disjoint status classes, so peel
      # transport out: non-2xx are real HTTP responses outside 2xx (spec §37).
      non_2xx=$(( errors - transport ))
      maxp=$(max_p99_ms "$TMP/timeseries.jsonl")
      reload_window_reports "$WARMUP_SECS" "$FAILURE_EVENT_AT"
      pre_p99=$(ns_to_ms "$(json_field "$TMP/pre.json" 99th)")
      post_p99=$(ns_to_ms "$(json_field "$TMP/post.json" 99th)")
      extra=""
      if [[ "$mode" == drain ]]; then
        base="roundrobin-10kb-sighup-drain"
        # Snapshot 2 - snapshot 1 is recorded but not judged (legitimate
        # pre-swap traffic); only snapshot 3 - snapshot 2 must be zero, the
        # removed backend receiving nothing after the reload is applied.
        before_applied=$(arrival_deltas "$TMP/drain-snap1.tsv" "$TMP/drain-snap2.tsv" \
          | awk -F'\t' -v b="$DRAIN_BACKEND" '$1==b{print $2}')
        applied_end=$(arrival_deltas "$TMP/drain-snap2.tsv" "$TMP/drain-snap3.tsv" \
          | awk -F'\t' -v b="$DRAIN_BACKEND" '$1==b{print $2}')
        (( ${applied_end:-1} == 0 )) || extra="drain_isolation"
        # A missed config_reloaded means the reload never applied, so neither
        # the isolation window nor "never selected again" is meaningful.
        [[ -f "$TMP/drain-applied" ]] || extra="${extra:+$extra,}reload_not_applied"
      else
        base="roundrobin-10kb-sighup-noop"
      fi
      verdict=$(reload_verdict "$non_2xx" "$transport" "$pre_p99" "$post_p99" "$extra")
      if [[ "$mode" == drain ]]; then
        analysis=$(printf 'measurements: mode=sighup-drain event=docker-compose-kill-s-HUP-lb event_at_s=%s non_2xx=%s transport_errors=%s drops=%s pre_p99_ms=%s post_p99_ms=%s p99_factor=%s p50_ms=%s p99_ms=%s p99_peak_ms=%s p99_recovery_s=%s drain_before_applied_backend4=%s drain_applied_end_backend4=%s\nverdict=%s' \
          "$event_offset" "$non_2xx" "$transport" "$drops" "$pre_p99" "$post_p99" "$RELOAD_P99_FACTOR" "$p50" "$p99" "$maxp" "$rec" "${before_applied:-0}" "${applied_end:-0}" "$verdict")
      else
        analysis=$(printf 'measurements: mode=sighup-noop event=docker-compose-kill-s-HUP-lb event_at_s=%s non_2xx=%s transport_errors=%s drops=%s pre_p99_ms=%s post_p99_ms=%s p99_factor=%s p50_ms=%s p99_ms=%s p99_peak_ms=%s p99_recovery_s=%s\nverdict=%s' \
          "$event_offset" "$non_2xx" "$transport" "$drops" "$pre_p99" "$post_p99" "$RELOAD_P99_FACTOR" "$p50" "$p99" "$maxp" "$rec" "$verdict")
      fi
      verdict_label="${verdict%% *}"
      ;;
  esac

  mkdir -p "$RESULTS/failure"
  { printf '# algorithm=roundrobin size=10kb competitor=lb load=%s comparison=%s rate_rps=%s\n' \
      "$label" "$(comparison_for roundrobin)" "$rate"
    if [[ "$mode" == drain ]]; then
      snapshot_block before "$TMP/drain-snap1.tsv"
      snapshot_block applied "$TMP/drain-snap2.tsv"
      snapshot_block end "$TMP/drain-snap3.tsv"
    fi
    cat "$TMP/report.txt"
    printf '\n%s\n' "$analysis"; } > "$RESULTS/failure/$base.txt"
  cp "$TMP/report.hdr" "$RESULTS/failure/$base.hdr"
  { printf '# algorithm=roundrobin size=10kb competitor=lb load=%s comparison=%s rate_rps=%s\n' \
      "$label" "$(comparison_for roundrobin)" "$rate"
    cat "$TMP/timeseries.jsonl"; } > "$RESULTS/failure/$base-timeseries.txt"
  add_summary roundrobin 10kb lb "${label}${verdict_label:+:$verdict_label}" "$rate"
  log "  failure $label: errors=$errors event_at_s=$event_offset p99=${p99}ms"
}

run_failure() {
  log "=== failure slice: backend-kill, no-op reload, drain reload — 3 runs ==="
  : > "$TMP/summary.tsv"
  up_h2
  # The reload runs mount a working copy in the results scratch area so the
  # committed configs are never modified. The drain config (backend4 dropped) is
  # derived here at run time, not committed as a ninth config (S5.T9.2).
  cp "$ROOT/bench/configs/h2/roundrobin.yaml" "$TMP/reload-config.yaml"
  grep -v "$DRAIN_BACKEND" "$TMP/reload-config.yaml" > "$TMP/drain-config.yaml"

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

  # Mount the working copy for both reload runs; the drain run rewrites it in
  # place so the bind mount really changes (S5.T9.2).
  set_lb "./results/.tmp/reload-config.yaml"
  failure_event sighup "$url" "$rate"
  failure_event drain "$url" "$rate"

  # Recreate on the committed config so a later slice/run starts clean.
  set_lb "./configs/h2/roundrobin.yaml"
}

# ---------------------------------------------------------------------------
# Degraded slice (S5.T8.2). backend3 is recreated 50 ms slow for the whole
# slice; every other backend stays fast. All eight runs (four algorithms × two
# competitors, h2, 10 KiB) use one absolute rate — half the h2/round-robin/
# 10 KiB LB peak — so their distributions and latencies are comparable, and
# every run is bracketed by arrival snapshots so the headline result is the
# per-backend share. The slow backend is restored on exit, including on failure.
# ---------------------------------------------------------------------------

# restore_degraded recreates backend3 with no delay. The flag gates it, so the
# EXIT trap can call it whether or not the slice ever injected the delay, and a
# second call is a no-op. A failed restore is warned about (it runs in an EXIT
# trap, so it must not itself abort) because it would silently leave a later
# slice running against a slow backend.
restore_degraded() {
  [[ "${DEGRADED_BACKEND_SLOW:-0}" == 1 ]] || return 0
  SLEEP_MS=0
  if ! "${COMPOSE[@]}" up -d --force-recreate "$DEGRADED_BACKEND" >/dev/null 2>&1; then
    warn "restore_degraded: could not recreate $DEGRADED_BACKEND at 0ms; it may still be slow"
  fi
  DEGRADED_BACKEND_SLOW=0
}

# verify_backend_sleep <ms> reads the target backend's startup line and aborts
# unless it reports the wanted delay, so the injection is proven to have taken
# effect rather than assumed from the constant — the same run-time-proof shape
# as the CPU-pinning checks. The last sleep_ms wins, since a recreated backend
# logs a fresh startup line.
verify_backend_sleep() { # <ms>
  local want="$1" seen
  seen=$("${COMPOSE[@]}" logs --no-color "$DEGRADED_BACKEND" 2>/dev/null \
    | grep -o '"sleep_ms":[0-9]*' | tail -n1 | cut -d: -f2 || true)
  if [[ "$seen" != "$want" ]]; then
    warn "degraded check FAILED: $DEGRADED_BACKEND sleep_ms=${seen:-unknown} (need $want)"
    exit 1
  fi
}

run_degraded() {
  log "=== degraded slice: ${DEGRADED_BACKEND} +${DEGRADED_SLEEP_MS}ms, ${DEGRADED_RUNS} runs ==="
  : > "$TMP/degraded_summary.tsv"
  local peak rate
  SLEEP_MS=0
  up_h2
  if [[ -n "$CORE_RR_10KB_LB_PEAK" ]]; then
    peak=$CORE_RR_10KB_LB_PEAK
    log "  rate from this invocation's core result: peak=${peak}/s"
  else
    log "  no core result in this invocation; discovering the h2/roundrobin/10kb/lb peak ..."
    set_lb "./configs/h2/roundrobin.yaml"
    peak=$(discover_peak "$(target_url h2 lb "$DEGRADED_SIZE")")
    log "  discovered peak=${peak}/s"
  fi
  if (( peak == 0 )); then
    warn "degraded slice reached no sustainable rate at seed ${SEED_RATE}/s; skipped"
    return
  fi
  rate=$(( peak * DEGRADED_RATE_PCT / 100 ))
  (( rate > 0 )) || rate=1
  log "  absolute rate=${rate}/s (${DEGRADED_RATE_PCT}% of ${peak}/s for all 8 runs)"

  # Inject the delay. Arm the trap and set the flag *before* recreating, so a
  # failed or interrupted recreate is still restored by the EXIT trap, then
  # prove the new container actually came up with the delay.
  SLEEP_MS=$DEGRADED_SLEEP_MS
  DEGRADED_BACKEND_SLOW=1
  trap 'restore_degraded; provenance_finish' EXIT
  "${COMPOSE[@]}" up -d --force-recreate "$DEGRADED_BACKEND"
  verify_backend_sleep "$DEGRADED_SLEEP_MS"

  local scheme algo comp url hotkey base meta p50 p99 shares
  scheme=$(backend_scheme h2)
  for algo in "${ALGOS[@]}"; do
    set_lb "./configs/h2/$algo.yaml"
    set_nginx_conf "$(nginx_conf_for h2 "$algo")"
    for comp in lb nginx; do
      url=$(target_url h2 "$comp" "$DEGRADED_SIZE")
      hotkey=no
      if [[ "$algo" == consistent-hash ]]; then hotkey=yes; fi
      progress h2 "$algo" "$DEGRADED_SIZE" "$comp" degraded
      measured_attack "$scheme" "$url" "$rate" "$DEGRADED_SECS" "$hotkey"
      p50=$(ns_to_ms "$(json_field "$TMP/metrics.json" 50th)")
      p99=$(ns_to_ms "$(json_field "$TMP/metrics.json" 99th)")
      shares=$(awk -F'\t' '{ printf "%s\t", $3 }' "$TMP/arrivals-delta.tsv")
      printf '%s\t%s\t%s\t%s\t%s\n' "$algo" "$comp" "$p50" "$p99" "$shares" \
        >> "$TMP/degraded_summary.tsv"
      base="${algo}-${DEGRADED_SIZE}-${comp}-degraded"
      meta="algorithm=$algo size=$DEGRADED_SIZE competitor=$comp load=degraded comparison=$(comparison_for "$algo") rate_rps=$rate backend3_sleep_ms=$DEGRADED_SLEEP_MS
$(cat "$TMP/distribution.txt")"
      save_result degraded "$base" "$meta"
    done
  done

  restore_degraded
}

# print_degraded_summary prints the slice's table — algorithm, competitor, p50,
# p99, and each backend's share of the arrivals — different columns from the
# peak/latency summary, so it has its own reader.
print_degraded_summary() {
  [[ -s "$TMP/degraded_summary.tsv" ]] || return 0
  printf '\n%-14s %-6s %9s %9s %10s %10s %10s %10s\n' \
    algorithm comp "p50(ms)" "p99(ms)" "backend1%" "backend2%" "backend3%" "backend4%"
  printf '%.0s-' {1..84}; printf '\n'
  awk -F'\t' '{ printf "%-14s %-6s %9s %9s %10s %10s %10s %10s\n", $1, $2, $3, $4, $5, $6, $7, $8 }' \
    "$TMP/degraded_summary.tsv"
  : > "$TMP/degraded_summary.tsv"
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
      progress "$proto" "$algo" "$SMOKE_SIZE" "$comp" smoke
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
case "$SLICE" in
  -h|--help|help) usage; exit 0 ;;
  core|protocol|failure|degraded|smoke|all) ;;
  *) warn "unknown slice '$SLICE'"; usage; exit 2 ;;
esac
mkdir -p "$TMP" "$RESULTS/core" "$RESULTS/protocol" "$RESULTS/failure" "$RESULTS/degraded"
provenance_start

RUN_TOTAL=$(run_total "$SLICE")
RUN_START=$(date +%s)

case "$SLICE" in
  core) run_core; print_summary ;;
  protocol) run_protocol; print_summary ;;
  failure) run_failure; print_summary ;;
  degraded) run_degraded; print_degraded_summary ;;
  smoke) run_smoke; exit 0 ;;
  all) run_core; print_summary; run_protocol; print_summary; run_failure; print_summary; run_degraded; print_degraded_summary ;;
esac

log ""
log "done. results under bench/results/${SLICE}/ (raw .gob/.csv in bench/results/.tmp/)"
log "tear the stack down with: docker compose -f bench/docker-compose.yml down"
