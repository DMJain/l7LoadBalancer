#!/usr/bin/env bash
#
# Latency-isolation acceptance check for the local live demo (S5.T16.5,
# ADR-0023 decision 10).
#
# Proves the demo rig is sound before any UI is built on it: latency injected on
# one backend stays on that backend (phase 1, through the round-robin LB), and
# the p2c-ewma selector moves traffic off it (phase 2). It is the guard against
# the failure mode that sank the published `degraded` slice, where the injected
# delay reached every backend.
#
# Run against a live stack:
#   docker compose -f demo/docker-compose.yml up -d --build
#   demo/acceptance.sh
#
# It drives the generators' control endpoints and the backends' admin listeners
# directly and reads Prometheus's HTTP API; it does not depend on the control
# service (S5.T16.4). Those ports stay on the demo's internal network, so the
# check reaches them by exec'ing `wget` inside the running Prometheus container
# (the only service with busybox) rather than publishing anything — ADR-0023
# decision 2.
#
# Verification hook: INJECT_BACKENDS (default "backend3") selects which
# backend(s) phase 1 injects 200 ms into, so the mandated negative run can put
# latency on a second backend too. The assertion does not follow it — it stays
# the spec's fixed shape (backend3 slow, backends 1/2/4 fast) — so
# `INJECT_BACKENDS="backend2 backend3" demo/acceptance.sh` makes phase 1 fail,
# naming the backend whose latency is not isolated.
#
# Every failure names the assertion, the LB and the measured value. An EXIT trap
# restores every backend's profile and the generators' rate and target, pass or
# fail, so a failed check leaves the stack usable.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
compose=(docker compose -f "$here/docker-compose.yml")

# Fixed check parameters (constants, no flags — the harness style).
readonly SLOW_MS=200            # injected latency
readonly PHASE1_WAIT_S=30       # settle time before phase 1 is measured
readonly PHASE2_CONVERGE_S=30   # EWMA convergence after the selector switch
readonly WINDOW=30s             # Prometheus rate window ("a short window")
readonly CHECK_TOTAL_RATE=400   # demo default; each client takes its Zipf share
readonly SLOW_P50_MIN_MS=150    # phase 1: the slow backend must show the delay
readonly FAST_P50_MAX_MS=20     # phase 1: every other backend must stay fast
readonly SHARE_MAX_PCT=15       # phase 2: backend3's request share ceiling

readonly RR_JOB=lb-roundrobin
readonly P2C_JOB=lb-p2c-ewma
readonly RR_URL="http://lb-roundrobin:8080"
readonly P2C_URL="http://lb-p2c-ewma:8080"

readonly BACKENDS=(backend1 backend2 backend3 backend4)
readonly GENS=(gen1 gen2 gen3 gen4 gen5 gen6 gen7 gen8)
readonly INJECT_BACKENDS="${INJECT_BACKENDS:-backend3}"

tmpdir="$(mktemp -d)"
backend_snapshot="$tmpdir/backends.tsv"
gen_snapshot="$tmpdir/generators.tsv"

fail() { echo "FAIL: $*" >&2; exit 1; }

# json_field <json> <key> prints a top-level field of a JSON object.
json_field() { python3 -c 'import json, sys; print(json.loads(sys.argv[1])[sys.argv[2]])' "$1" "$2"; }

# backend_profile_is_clean <profile-json> succeeds when the profile is the zero
# baseline the check requires.
backend_profile_is_clean() {
	python3 -c 'import json, sys; p = json.loads(sys.argv[1]); sys.exit(0 if p["sleep_ms"] == 0 and p["jitter_ms"] == 0 and p["fail_rate"] == 0 else 1)' "$1"
}

# in_prom runs wget inside the running demo Prometheus container (the only
# shell-capable service on the demo network), so internal-only ports can be
# driven without publishing them. stdin is closed so the exec cannot consume a
# caller's `while read` loop.
in_prom() { "${compose[@]}" exec -T prometheus wget -qO- "$@" </dev/null; }

# prom_query <expr> prints the Prometheus instant-query JSON. Busybox wget has
# no --data-urlencode, so spaces are form-encoded as '+'.
prom_query() {
	local body="query=${1// /+}"
	in_prom --post-data="$body" "http://localhost:9090/api/v1/query"
}

# prom_value <expr> prints the first scalar value, or exits non-zero when the
# query fails or has no result.
prom_value() {
	prom_query "$1" | python3 -c '
import json, sys
r = json.load(sys.stdin)["data"]["result"]
if not r or "value" not in r[0]:
    sys.exit(1)
print(r[0]["value"][1])
'
}

# p50_ms <job> <backend> prints the backend's request-duration p50 in ms for the
# current window, from the LB's per-backend histogram.
p50_ms() {
	local q="histogram_quantile(0.5, sum by (le) (rate(lb_request_duration_seconds_bucket{job=\"$1\",backend=\"$2\"}[$WINDOW])))" v
	v=$(prom_value "$q") || fail "Prometheus has no p50 for $2 on $1 (is it scraped?)"
	python3 -c 'import sys; print(f"{float(sys.argv[1]) * 1000:.1f}")' "$v"
}

# share_pct <job> <backend> prints the backend's share of the LB's requests, %.
share_pct() {
	local q="100 * sum(rate(lb_requests_total{job=\"$1\",backend=\"$2\"}[$WINDOW])) / sum(rate(lb_requests_total{job=\"$1\"}[$WINDOW]))" v
	v=$(prom_value "$q") || fail "Prometheus has no request share for $2 on $1"
	python3 -c 'import sys; print(f"{float(sys.argv[1]):.2f}")' "$v"
}

# num_cmp <a> <op> <b> runs a float comparison; used with && / ||.
num_cmp() {
	python3 -c 'import sys; op = sys.argv[2]; a, b = float(sys.argv[1]), float(sys.argv[3]); sys.exit(0 if {"<": a < b, "<=": a <= b, ">": a > b, ">=": a >= b}[op] else 1)' "$1" "$2" "$3"
}

# wait_metric <expr> <want> <timeout_s> <description> polls until <expr> reads
# exactly <want>, or fails naming the last value seen.
wait_metric() {
	local deadline=$((SECONDS + $3)) v=""
	while [ "$SECONDS" -lt "$deadline" ]; do
		v=$(prom_value "$1" 2>/dev/null || true)
		[ "$v" = "$2" ] && return 0
		sleep 1
	done
	fail "$4 (last ${1}=${v:-<none>})"
}

# admin_get <backend> prints the backend's current profile. The admin listener
# takes POST only, and an empty body keeps every field, so `{}` is a read.
admin_get() { in_prom --post-data='{}' --header "Content-Type: application/json" "http://$1:9091/"; }

# admin_set <backend> <json> writes a profile and prints the profile now in
# effect.
admin_set() { in_prom --post-data="$2" --header "Content-Type: application/json" "http://$1:9091/"; }

# gen_get <gen> prints the generator's control status JSON.
gen_get() { in_prom "http://$1:9090/"; }

# gen_set <gen> <json> updates the generator's total rate and/or target.
gen_set() { in_prom --post-data="$2" --header "Content-Type: application/json" "http://$1:9090/"; }

# restore undoes every change this script made: backends to their snapshotted
# profile, generators to their snapshotted rate and target. It runs on EXIT, so
# a failed check leaves the stack usable.
restore() {
	local status=$? g b body rate target
	trap - EXIT
	echo "==> Restoring backend profiles and generator settings"
	if [ -s "$backend_snapshot" ]; then
		while IFS=$'\t' read -r b body; do
			admin_set "$b" "$body" >/dev/null 2>&1 || true
		done <"$backend_snapshot"
	fi
	if [ -s "$gen_snapshot" ]; then
		while IFS=$'\t' read -r g rate target; do
			gen_set "$g" "{\"total_rate\":$rate,\"target\":\"$target\"}" >/dev/null 2>&1 || true
		done <"$gen_snapshot"
	fi
	rm -rf "$tmpdir"
	exit "$status"
}
trap restore EXIT

command -v docker >/dev/null 2>&1 || fail "docker not found"
command -v python3 >/dev/null 2>&1 || fail "python3 not found (needed to parse Prometheus JSON)"

echo "==> 1. Preconditions"

# Prometheus reachable (the stack is up).
ready=0
for _ in $(seq 1 60); do
	if in_prom "http://localhost:9090/-/ready" >/dev/null 2>&1; then
		ready=1
		break
	fi
	sleep 1
done
[ "$ready" = 1 ] || fail "Prometheus never became ready — is the demo stack up? (make demo-up)"

# Both LBs are healthy Prometheus targets and every backend is selectable.
for job in "$RR_JOB" "$P2C_JOB"; do
	wait_metric "up{job=\"$job\"}" 1 60 "$job is not a healthy Prometheus target"
done
for job in "$RR_JOB" "$P2C_JOB"; do
	for b in "${BACKENDS[@]}"; do
		wait_metric "lb_backend_healthy{job=\"$job\",backend=\"$b\"}" 1 60 "$b is not selectable on $job"
	done
done
echo "    both LBs up, all four backends selectable on each"

# Snapshot the backends' profiles and require the clean baseline.
: >"$backend_snapshot"
for b in "${BACKENDS[@]}"; do
	body=$(admin_get "$b") || fail "$b admin listener did not answer (ADMIN_ENABLED? stack up?)"
	printf '%s\t%s\n' "$b" "$body" >>"$backend_snapshot"
	if ! backend_profile_is_clean "$body"; then
		vals="sleep_ms=$(json_field "$body" sleep_ms) jitter_ms=$(json_field "$body" jitter_ms) fail_rate=$(json_field "$body" fail_rate)"
		fail "precondition: $b is not at sleep_ms=0 jitter_ms=0 fail_rate=0 ($vals); run the check on a fresh up"
	fi
done
echo "    every backend at sleep_ms=0 jitter_ms=0 fail_rate=0"

# Wait for the generators, snapshot their settings, then set the fixed check
# rate and the phase-1 target.
: >"$gen_snapshot"
for g in "${GENS[@]}"; do
	up=0
	for _ in $(seq 1 60); do
		if body=$(gen_get "$g" 2>/dev/null); then
			up=1
			break
		fi
		sleep 1
	done
	[ "$up" = 1 ] || fail "generator $g control endpoint never answered at http://$g:9090/"
	rate=$(json_field "$body" total_rate)
	target=$(json_field "$body" target)
	printf '%s\t%s\t%s\n' "$g" "$rate" "$target" >>"$gen_snapshot"
	gen_set "$g" "{\"total_rate\":$CHECK_TOTAL_RATE,\"target\":\"$RR_URL\"}" >/dev/null \
		|| fail "generator $g rejected the check rate/target update"
done
echo "    all eight generators at ${CHECK_TOTAL_RATE} req/s, target $RR_URL"

echo "==> 2. Phase 1: $INJECT_BACKENDS at ${SLOW_MS} ms, $RR_JOB active"
for b in $INJECT_BACKENDS; do
	admin_set "$b" "{\"sleep_ms\":$SLOW_MS,\"jitter_ms\":0}" >/dev/null \
		|| fail "could not set $b to sleep_ms=$SLOW_MS"
done
echo "    waiting ${PHASE1_WAIT_S}s for the injected latency to show..."
sleep "$PHASE1_WAIT_S"

for b in "${BACKENDS[@]}"; do
	p=$(p50_ms "$RR_JOB" "$b")
	echo "    $b p50=${p} ms"
	if [ "$b" = backend3 ]; then
		num_cmp "$p" ">=" "$SLOW_P50_MIN_MS" \
			|| fail "phase 1 [$RR_JOB]: backend3 p50=${p}ms < ${SLOW_P50_MIN_MS}ms (injected ${SLOW_MS}ms is not visible on backend3)"
	else
		num_cmp "$p" "<=" "$FAST_P50_MAX_MS" \
			|| fail "phase 1 [$RR_JOB]: $b p50=${p}ms > ${FAST_P50_MAX_MS}ms (injected latency is not isolated to backend3)"
	fi
done
echo "    injected latency is isolated to backend3"

echo "==> 3. Phase 2: switch generators to $P2C_JOB; backend3 still at ${SLOW_MS} ms"
for g in "${GENS[@]}"; do
	gen_set "$g" "{\"target\":\"$P2C_URL\"}" >/dev/null \
		|| fail "generator $g rejected the target switch to $P2C_URL"
done
echo "    waiting ${PHASE2_CONVERGE_S}s for EWMA convergence..."
sleep "$PHASE2_CONVERGE_S"

slowest=backend3
share=$(share_pct "$P2C_JOB" "$slowest")
echo "    $slowest share=${share}%"
num_cmp "$share" "<" "$SHARE_MAX_PCT" \
	|| fail "phase 2 [$P2C_JOB]: $slowest request share=${share}% >= ${SHARE_MAX_PCT}% (selector did not move off the slow backend)"

echo
echo "All checks passed."
echo "  Phase 1: ${INJECT_BACKENDS} at ${SLOW_MS} ms stayed isolated on $RR_JOB"
echo "  Phase 2: $P2C_JOB moved traffic off $slowest (share=${share}% < ${SHARE_MAX_PCT}%)"
echo
