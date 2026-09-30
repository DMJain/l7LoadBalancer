#!/usr/bin/env bash
#
# repro.sh — the one-command reproducer (S5.T12, issue 11).
#
# `make bench-repro` takes a stranger from a fresh clone to the full result set:
# preflight, certificates, images, the smoke slice, then the full matrix. It
# stops at the first failure. It is the Sprint 5 exit criterion in executable
# form; the published run (issue 19) is produced by it. It is a benchmark tool,
# not a deployment, so ADR-0019 stands.
#
# The harness (bench/run.sh) is a development tool: it *records* a dirty tree or
# running containers but never refuses them, so uncommitted work can be
# smoke-tested (spec §52). Enforcement lives here, at the publication boundary:
# this script refuses a dirty tree (untracked files included), any running
# container (listing each one, and never stopping any itself), and fewer than
# MIN_VCPUS vCPUs.
#
# The fixed 8-vCPU split (bench/docker-compose.yml) is never scaled from the
# host's core count, so Docker must expose at least MIN_VCPUS or the cpusets
# cannot all be honoured: refuse up front rather than fail obscurely mid-run.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/bench/docker-compose.yml"
COMPOSE=(docker compose -f "$COMPOSE_FILE")

# Fixed 8-vCPU split. Raise this locally to exercise the refusal (the ticket's
# "a vCPU threshold raised locally, uncommitted, to simulate too few").
MIN_VCPUS=8

# fail <message> [fix] prints why the run is refused and how to fix it, then
# stops the reproducer.
fail() {
  printf 'bench-repro: %s\n' "$1" >&2
  if [[ -n "${2:-}" ]]; then
    printf '  fix: %s\n' "$2" >&2
  fi
  exit 1
}

require_cmd() { # <name> <fix>
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required but was not found on PATH" "$2"
}

# preflight refuses any condition that would make the results unreproducible or
# skewed, before anything is built or measured.
preflight() {
  require_cmd docker "install Docker Desktop and put docker on PATH"
  docker compose version >/dev/null 2>&1 \
    || fail "Docker Compose v2 is required" \
            "update Docker Desktop; the v2 plugin provides 'docker compose'"
  require_cmd openssl "install openssl (scripts/generate-cert.sh needs it)"
  require_cmd make "install make"

  local cpus
  cpus=$(docker info --format '{{.NCPU}}' 2>/dev/null || true)
  [[ "$cpus" =~ ^[0-9]+$ ]] \
    || fail "could not read Docker's vCPU count (is the daemon running?)" \
            "start Docker Desktop"
  if (( cpus < MIN_VCPUS )); then
    fail "Docker reports ${cpus} vCPUs, fewer than the required ${MIN_VCPUS}" \
         "assign at least ${MIN_VCPUS} CPUs to Docker Desktop, then retry"
  fi

  local running
  running=$(docker ps --format '  {{.ID}}  {{.Image}}')
  if [[ -n "$running" ]]; then
    printf 'bench-repro: refusing to run while any container is running (this never stops them):\n' >&2
    printf '%s\n' "$running" >&2
    fail "stop them yourself before a published run" \
         "docker stop \$(docker ps -q)"
  fi

  # Literal clean-tree check: a modified tracked file or an untracked file means
  # the results would not trace to a commit, so refuse. Gitignored artifacts
  # (certs/, bench/results/.tmp/) are not untracked and do not count.
  local dirty
  dirty=$(git -C "$ROOT" status --porcelain)
  if [[ -n "$dirty" ]]; then
    printf 'bench-repro: refusing to run on a dirty working tree; commit or stash first:\n' >&2
    printf '%s\n' "$dirty" >&2
    fail "published results must come from a commit" \
         "commit or stash the files above"
  fi
}

main() {
  printf 'bench-repro: preflight\n'
  preflight

  printf '\nbench-repro: generating certificates\n'
  "$ROOT/scripts/generate-cert.sh"

  printf '\nbench-repro: building bench images\n'
  "${COMPOSE[@]}" build

  printf '\nbench-repro: smoke slice\n'
  "$ROOT/bench/run.sh" smoke

  printf '\nbench-repro: full matrix\n'
  "$ROOT/bench/run.sh" all

  printf '\nbench-repro: done. results under bench/results/\n'
}

main
