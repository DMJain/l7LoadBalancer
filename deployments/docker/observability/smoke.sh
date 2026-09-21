#!/usr/bin/env bash
#
# Scripted smoke test for the observability demo stack (S3.T7).
#
# Assumes the load balancer is already running locally (`make run`) and the
# dummy backends are up. Brings up this stack, then verifies:
#   1. the load balancer's /metrics has every seeded gauge series;
#   2. Prometheus is up and scraping the load balancer target successfully;
#   3. Grafana has the dashboard provisioned.
#
# It does NOT send client traffic — see README.md for the traffic and
# chaos-test procedures.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
compose=(docker compose -f "$here/docker-compose.yml")

lb_metrics="${LB_METRICS_URL:-http://127.0.0.1:9090/metrics}"
prom="${PROM_URL:-http://127.0.0.1:9091}"
grafana="${GRAFANA_URL:-http://127.0.0.1:3000}"

fail() { echo "FAIL: $*" >&2; exit 1; }

echo "==> 1. Load balancer metrics endpoint"
curl -fsS "$lb_metrics" >/dev/null || fail "no /metrics at $lb_metrics — is 'make run' running?"
for name in lb_backend_healthy lb_circuit_state lb_active_connections; do
	curl -fsS "$lb_metrics" | grep -q "^$name" || fail "$name has no seeded series yet"
done
curl -fsS "$lb_metrics" | grep -E '^lb_(backend_healthy|circuit_state|active_connections)' | sort -u
echo "    ok"

echo "==> 2. Starting the observability stack"
"${compose[@]}" up -d

echo "    waiting for Prometheus..."
for _ in $(seq 1 60); do
	curl -fsS "$prom/-/ready" >/dev/null 2>&1 && break
	sleep 1
done
curl -fsS "$prom/-/ready" >/dev/null 2>&1 || fail "Prometheus did not become ready at $prom"

# A few scrape intervals may pass before the target reports up.
target_up=0
for _ in $(seq 1 45); do
	if curl -fsS "$prom/api/v1/targets" \
		| python3 -c 'import json,sys; t=json.load(sys.stdin)["data"]["activeTargets"]; sys.exit(0 if any(x["labels"].get("job")=="l7loadbalancer" and x["health"]=="up" for x in t) else 1)'; then
		target_up=1
		break
	fi
	sleep 1
done
[ "$target_up" = 1 ] || fail "Prometheus target l7loadbalancer never came up (is 'make run' listening on :9090?)"
echo "    ok — l7loadbalancer target is up"

echo "==> 3. Grafana dashboard provisioned"
for _ in $(seq 1 30); do
	curl -fsS "$grafana/api/health" >/dev/null 2>&1 && break
	sleep 1
done
curl -fsS "$grafana/api/search?query=l7LoadBalancer" | grep -q "l7LoadBalancer" \
	|| fail "dashboard not found in Grafana"
echo "    ok"

echo
echo "All checks passed."
echo "  Grafana:    $grafana  (dashboard 'l7LoadBalancer')"
echo "  Prometheus: $prom"
echo
echo "Drive traffic:  for i in \$(seq 1 100); do curl -s http://127.0.0.1:8080/ >/dev/null; done"
echo "Chaos test:     docker compose -f deployments/docker/docker-compose.yml stop backend-c"
