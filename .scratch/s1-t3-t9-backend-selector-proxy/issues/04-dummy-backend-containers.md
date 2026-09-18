# 04: Dummy Backend Containers

**What to build:** Three lightweight, independently-chaos-configurable backend services via docker-compose, so anyone can `docker compose up` and immediately have something real for the load balancer to route to — without writing a second config file or standing up real upstreams.

**Blocked by:** None (only needs S1.T2, already done — runs fully in parallel with tickets 01–03, 05–07)

**Status:** ready-for-agent

- [ ] `docker-compose.yml` defines exactly 3 services (no LB service — that runs locally via `make run`)
- [ ] Ports mapped to host `9001`/`9002`/`9003` so the existing `configs/example.yaml` works unmodified
- [ ] Each service is a Go stdlib-only binary responding to `GET /` with a body identifying itself (e.g. `{"backend":"backend-a"}`)
- [ ] Two env vars per service: `SLEEP_MS` (int, artificial latency, default 0) and `FAIL_RATE` (float 0.0–1.0, fraction of requests returning HTTP 500, default 0), documented in a README
- [ ] The 3 services ship with distinct, non-zero defaults so `docker compose up` visibly demonstrates uneven latency/failure without manual overrides
- [ ] `docker compose up -d` brings up all 3 healthy; manual smoke test documented in the session log (no Go unit tests — infra-only)
