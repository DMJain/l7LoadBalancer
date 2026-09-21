#!/usr/bin/env bash
#
# Scripted smoke test for the observability demo stack (S3.T7).
#
# Assumes the load balancer is already running locally (`make run`) and the
# dummy backends are up. Brings up this stack, then verifies:
#   1. the load balancer's /metrics has every seeded gauge series;
#   2. Prometheus is up and scraping the load balancer target successfully;
#   3. Prometheus has scraped the seeded gauge series (i.e. the three gauge
#      panels have data);
#   4. Grafana has the dashboard provisioned.
#
# It does NOT send client traffic, so the two traffic-derived panels (request
# rate, latency) stay empty — see README.md for the traffic and chaos-test
# procedures, which fill them.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
compose=(docker compose -f "$here/docker-compose.yml")

lb_metrics="${LB_METRICS_URL:-http://127.0.0.1:9090/metrics}"
prom_url="${PROM_URL:-http://127.0.0.1:9091}"
grafana_url="${GRAFANA_URL:-http://127.0.0.1:3000}"

fail() { echo "FAIL: $*" >&2; exit 1; }

echo "==> 1. Load balancer metrics endpoint"
metrics_body="$(curl -fsS "$lb_metrics")" || fail "no /metrics at $lb_metrics — is 'make run' running?"
for name in lb_backend_healthy lb_circuit_state lb_active_connections; do
	grep -q "^$name" <<<"$metrics_body" || fail "$name has no seeded series yet"
done
grep -E '^lb_(backend_healthy|circuit_state|active_connections)' <<<"$metrics_body" | sort -u
echo "    ok"

echo "==> 2. Starting the observability stack"
"${compose[@]}" up -d

echo "    waiting for Prometheus..."
for _ in $(seq 1 60); do
	curl -fsS "$prom_url/-/ready" >/dev/null 2>&1 && break
	sleep 1
done
curl -fsS "$prom_url/-/ready" >/dev/null 2>&1 || fail "Prometheus did not become ready at $prom_url"

# A few scrape intervals may pass before the target reports up.
target_up=0
for _ in $(seq 1 45); do
	if curl -fsS "$prom_url/api/v1/targets" \
		| python3 -c 'import json,sys; t=json.load(sys.stdin)["data"]["activeTargets"]; sys.exit(0 if any(x["labels"].get("job")=="l7loadbalancer" and x["health"]=="up" for x in t) else 1)'; then
		target_up=1
		break
	fi
	sleep 1
done
[ "$target_up" = 1 ] || fail "Prometheus target l7loadbalancer never came up (is 'make run' listening on :9090?)"
echo "    ok — l7loadbalancer target is up"

echo "==> 3. Prometheus has the seeded gauge series (panel data present)"
# The three gauge panels read these; querying Prometheus proves the panels have
# data before any client traffic, not just that the dashboard JSON is loaded.
for query in lb_backend_healthy lb_circuit_state lb_active_connections; do
	results="$(curl -fsS --get --data-urlencode "query=$query" "$prom_url/api/v1/query" \
		| python3 -c 'import json,sys; print(len(json.load(sys.stdin)["data"]["result"]))')"
	[ "$results" -gt 0 ] || fail "Prometheus has no series for $query (gauge panel would be blank)"
	echo "    $query: $results series"
done

echo "==> 4. Grafana dashboard provisioned"
for _ in $(seq 1 30); do
	curl -fsS "$grafana_url/api/health" >/dev/null 2>&1 && break
	sleep 1
done
curl -fsS "$grafana_url/api/search?query=l7LoadBalancer" | grep -q "l7LoadBalancer" \
	|| fail "dashboard not found in Grafana"
echo "    ok"

echo
echo "All checks passed."
echo "  Grafana:    $grafana_url  (dashboard 'l7LoadBalancer')"
echo "  Prometheus: $prom_url"
echo
echo "Drive traffic:  for i in \$(seq 1 100); do curl -s http://127.0.0.1:8080/ >/dev/null; done"
echo "Chaos test:     docker compose -f deployments/docker/docker-compose.yml stop backend-c"
