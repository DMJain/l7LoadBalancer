# ADR-0019: Deployment target: the Docker container as the packaging artifact

**Status:** Accepted

## Context

ADR-0005 left the deployment target (bare binary, Docker or Kubernetes) open until the reload and connection-lifecycle work existed. The choice must follow from the properties of the built system:

1. **Three listeners in one process:** the client proxy on `listen` (default `:8080`), metrics on `metrics.listen` (`:9090`) and the health endpoint on `health_endpoint.listen` (`:8081`). The health endpoint is a separate server, never a path on the proxy (ADR-0014).
2. **Reload is in process, started by SIGHUP** (ADR-0015). There is no second process and no socket handoff. `main` handles SIGHUP apart from the SIGINT/SIGTERM shutdown context.
3. **The binary probes itself.** The `probe` subcommand lets a shell-less image run `HEALTHCHECK` as `/l7lb probe http://127.0.0.1:8081/livez`.
4. **No external state.** Configuration is one YAML file. There is no database, cache or shared store. Backend state (health, circuit, EWMA, active requests) lives in the process and resets on restart.
5. **No clustering.** One instance serves one configured backend set. There is no peer discovery, no shared state between instances and no scale-out requirement.

The artifacts exist. The image is `gcr.io/distroless/static-debian12:nonroot`, pinned by digest. It holds the static binary at `/l7lb` and `configs/docker.yaml` at `/etc/l7lb/config.yaml`. It exposes all three ports and carries OCI labels. The repo-root `docker-compose.yml` runs the image with the dummy backends, Prometheus and Grafana.

## Decision

**The deployment target is the Docker container. The digest-pinned distroless image is the packaged artifact. Kubernetes is not the target. The bare binary is the supported primitive under the container, not the packaged form.**

A deployment runs the image with `-config` pointing at a config file (the baked one, or one bind-mounted over `/etc/l7lb/config.yaml`) and publishes the client port. The health and metrics ports can stay on an internal network.

| Operation | How |
|---|---|
| Change the backends or the algorithm | Edit the mounted config, then run `docker kill -s HUP <container>`. The binary is PID 1 and gets the signal directly. The reload drops nothing (ADR-0015, ADR-0016). |
| Health | The image's `HEALTHCHECK` runs the `probe` subcommand against `/livez`. No shell or `curl` is needed. |
| Graceful shutdown | `docker stop` sends SIGTERM to PID 1. In-flight requests drain and all three listeners shut down. |
| Change any other config section | Restart the container. Reload rejects these changes (ADR-0015). |

`docker restart` or `docker compose restart` is not a reload. It sends SIGTERM and starts a new process, so in-flight requests drop. Only SIGHUP is the zero-drop path.

Why containers fit:

- The image is the reproducible unit. The three ports, baked config, OCI labels and self-probe `HEALTHCHECK` already form a full container contract.
- One stateless process with no external state is a standard container workload.
- SIGHUP reload works unchanged in a container, because the binary is PID 1. The bind-mounted config lets a deployment change the backends without a rebuild.

A binary change (a new image) needs a process replacement, and without a socket handoff that drops in-flight requests. SO_REUSEPORT handoff (start the new process on the same port, drain the old one) would make image rollouts zero-downtime. It is a possible future extension and is not built.

## Consequences

- The target is the artifact that already exists and is tested.
- Backend and algorithm changes deploy with no rebuild and no restart.
- The health paths (`/livez`, `/readyz`, `/startupz`) suit any container runtime or orchestrator.
- An image change or a change to another config section needs a restart that drops in-flight requests.
- The container adds a runtime dependency. The bare binary still runs for development (`make run`) and the host-process chaos flow.
- The health endpoint has Kubernetes names, so the system can run on Kubernetes. That is a capability, not a target. If clustering or external state ever appears, a new ADR revisits the target.
- Rejected alternatives:
  - The bare binary as the packaged target (loses reproducible packaging for no gain).
  - Kubernetes (its scheduling, discovery and scaling solve problems this single stateless instance does not have, and imply operational claims ADR-0005 disclaims).
  - Kubernetes manifests now (unused surface).
  - SO_REUSEPORT now (more moving parts than needed).
