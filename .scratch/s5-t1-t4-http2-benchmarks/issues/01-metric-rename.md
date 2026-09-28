# 01: Metric rename (`active_connections` → `active_requests`)

**What to build:** The `lb_active_connections` gauge metric is renamed to `lb_active_requests` across the entire codebase. The metric tracks per-request lifecycle (increment on request start, decrement on response body close), not TCP connections. With HTTP/2 multiplexing many requests over few TCP connections, the old name is misleading. After this ticket, all metric registrations, method names, callers, Grafana dashboard JSON, and the AGENTS.md locked vocabulary reference `lb_active_requests`. All existing tests pass. No behavioral change — only names change.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] Metric registration changed from `lb_active_connections` to `lb_active_requests` in the metrics package
- [ ] All method names updated: `IncActiveConnections` → `IncActiveRequests`, `DecActiveConnections` → `DecActiveRequests`, `SetActiveConnections` → `SetActiveRequests`, `DeleteActiveConnections` → `DeleteActiveRequests`
- [ ] All callers in the proxy package updated (`reqState.activate`, `reqState.release`, and any others)
- [ ] Grafana dashboard JSON (from S3.T7) references updated
- [ ] AGENTS.md locked vocabulary updated
- [ ] All existing tests pass with the new name
- [ ] `make test-race` passes
- [ ] Commit message documents the rationale: the metric tracks per-request lifecycle, not TCP connections, and the old name is misleading with HTTP/2 multiplexing
