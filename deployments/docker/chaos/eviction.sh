#!/usr/bin/env bash
#
# Scripted smoke test for the S3.T8 eviction/recovery chaos scenario.
#
# Closes MILESTONES.md's literal "`docker stop` a backend -> ejected within
# health threshold ... recovers on restart" wording against the real
# docker-compose backends, asserting through the load balancer's own
# Prometheus exposition endpoint (metrics.listen, default :9090) rather than
# by eyeballing Grafana.
#
# The load balancer runs as a host process (S3.T10's Dockerfile is out of this
# bundle's scope); the backends come from the existing S1.T9 compose. No new
# compose file.
#
# Prereqs: docker (daemon running), the Go toolchain, curl, awk.
#
# End-to-end execution requires a Docker daemon; if none is available the
# script fails fast at the first docker call. Recorded as [MANUAL VERIFICATION
# PENDING] in the session log, matching S3.T7's precedent.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
compose=(docker compose -f "$repo_root/deployments/docker/docker-compose.yml")

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
# contains <label> (a literal label selector such as backend="backend-a").
metric_value() {
	curl -fsS "$metrics_url" \
		| grep -E "^$1\{" \
		| grep -F "$2" \
		| awk '{print $NF}' \
		| head -n1
}

# wait_for_value polls until <metric>{<label>} reads <want>, or fails after
# <timeout> seconds (default 90 — three default probe intervals plus margin).
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

echo "==> 4. Baseline: $backend is healthy"
wait_for_value lb_backend_healthy "backend=\"$backend\"" 1 30

echo "==> 5. docker stop $backend -> ejected"
"${compose[@]}" stop "$backend"
wait_for_value lb_backend_healthy "backend=\"$backend\"" 0 90

echo "==> 6. docker start $backend -> reinstated"
"${compose[@]}" start "$backend"
wait_for_value lb_backend_healthy "backend=\"$backend\"" 1 90

echo
echo "All checks passed."
echo "  Metric query: lb_backend_healthy{backend=\"$backend\"}"
echo "  LB log:       $lb_log"
echo
echo "Tear down backends: ${compose[*]} down"
