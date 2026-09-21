#!/usr/bin/env bash
#
# Scripted smoke test for the S3.T9 circuit-breaker chaos scenario.
#
# Closes MILESTONES.md's literal "injecting 500s on one backend eventually
# opens its circuit" wording against the real docker-compose backends,
# asserting through the load balancer's own Prometheus exposition endpoint
# (metrics.listen, default :9090) rather than by eyeballing Grafana.
#
# The load balancer runs as a host process (S3.T10's Dockerfile is out of this
# bundle's scope); the backends come from the existing S1.T9 compose. No new
# compose file.
#
# Failure injection flips one backend's FAIL_RATE to 1 by recreating that
# service with a per-backend env override (the compose file reads
# ${BACKEND_A_FAIL_RATE:-0.05}), then restores it to 0. A `docker exec` cannot
# change a running process's environment, so the service is recreated.
#
# Prereqs: docker (daemon running), the Go toolchain, curl, awk, grep.
#
# End-to-end execution requires a Docker daemon; if none is available the
# script fails fast at the first docker call. Recorded as [MANUAL VERIFICATION
# PENDING] in the session log, matching S3.T7's precedent.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
compose=(docker compose -f "$repo_root/deployments/docker/docker-compose.yml")

lb_url="${LB_URL:-http://127.0.0.1:8080/}"
metrics_url="${LB_METRICS_URL:-http://127.0.0.1:9090/metrics}"
backend="backend-a"
bin="$repo_root/bin/l7-chaos-lb"
lb_log="$(mktemp -t l7-chaos-lb.XXXXXX.log)"

lb_pid=""

fail() { echo "FAIL: $*" >&2; exit 1; }

cleanup() {
	if [ -n "$lb_pid" ] && kill -0 "$lb_pid" 2>/dev/null; then
		kill "$lb_pid" 2>/dev/null || true
	fi
}
trap cleanup EXIT

# metric_value prints the value of the first <metric> series whose line
# contains <label> (a literal label selector such as
# backend="backend-a",state="open").
metric_value() {
	curl -fsS "$metrics_url" \
		| grep -E "^$1\{" \
		| grep -F "$2" \
		| awk '{print $NF}' \
		| head -n1
}

# blast sends <n> requests to the LB, ignoring their status: the goal is to
# route traffic to the backend under test, not to assert on one response.
blast() {
	local n="${1:-6}"
	for _ in $(seq 1 "$n"); do
		curl -sS -o /dev/null "$lb_url" || true
	done
}

# wait_for_value polls until <metric>{<label>} reads <want>, or fails after
# <timeout> seconds.
wait_for_value() {
	local metric="$1" label="$2" want="$3" timeout="${4:-90}"
	local deadline=$((SECONDS + timeout))
	local got=""
	while [ "$SECONDS" -lt "$deadline" ]; do
		got="$(metric_value "$metric" "$label" || true)"
		if [ "$got" = "$want" ]; then
			echo "    $metric{$label} = $want"
			return 0
		fi
		sleep 1
	done
	fail "$metric{$label} did not reach $want within ${timeout}s (last: ${got:-<none>})"
}

# wait_for_value_while_blasting polls <metric>{<label>} to reach <want> while
# repeatedly sending traffic. Needed for the circuit gate: a half-open trial
# only fires when a real request selects the recovered backend (round-robin
# round-robins onto it), not on a timer — ADR-0011 decision 6.
wait_for_value_while_blasting() {
	local metric="$1" label="$2" want="$3" timeout="${4:-120}"
	local deadline=$((SECONDS + timeout))
	local got=""
	while [ "$SECONDS" -lt "$deadline" ]; do
		blast 6
		got="$(metric_value "$metric" "$label" || true)"
		if [ "$got" = "$want" ]; then
			echo "    $metric{$label} = $want"
			return 0
		fi
		sleep 1
	done
	fail "$metric{$label} did not reach $want within ${timeout}s (last: ${got:-<none>})"
}

command -v docker >/dev/null 2>&1 || fail "docker not found"
command -v go >/dev/null 2>&1 || fail "go toolchain not found"

echo "==> 1. Build the load balancer"
(cd "$repo_root" && go build -o "$bin" ./cmd/l7LoadBalancer)

echo "==> 2. Start the dummy backends"
"${compose[@]}" up -d --build

echo "==> 3. Start the load balancer (host process)"
"$bin" -config "$repo_root/configs/example.yaml" >"$lb_log" 2>&1 &
lb_pid=$!

for _ in $(seq 1 40); do
	curl -fsS "$metrics_url" >/dev/null 2>&1 && break
	sleep 0.5
done
curl -fsS "$metrics_url" >/dev/null 2>&1 \
	|| fail "no /metrics at $metrics_url — see $lb_log"
echo "    metrics endpoint up"

echo "==> 4. Baseline: $backend circuit closed"
wait_for_value lb_circuit_state "backend=\"$backend\",state=\"closed\"" 1 30

echo "==> 5. Inject 5xx on $backend (FAIL_RATE=1) and blast until the circuit opens"
BACKEND_A_FAIL_RATE=1 "${compose[@]}" up -d --force-recreate "$backend"
wait_for_value_while_blasting lb_circuit_state "backend=\"$backend\",state=\"open\"" 1 90

echo "==> 6. Restore $backend (FAIL_RATE=0) and blast until the circuit closes"
BACKEND_A_FAIL_RATE=0 "${compose[@]}" up -d --force-recreate "$backend"
wait_for_value_while_blasting lb_circuit_state "backend=\"$backend\",state=\"closed\"" 1 120

echo
echo "All checks passed."
echo "  Metric query: lb_circuit_state{backend=\"$backend\",state=...}"
echo "  LB log:       $lb_log"
echo
echo "Tear down backends: ${compose[*]} down"
