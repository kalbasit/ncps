#!/usr/bin/env bash
set -euo pipefail

# test-inflight-staging-contention-auto.sh — start the FIXED-port dev backends
# (nix run .#deps), run the in-flight staging contention e2e driver, then tear
# the backends down.
#
# Like test-cdc-lifecycle-auto.sh (and unlike test-auto.sh, which allocates
# random ports for the Go integration suite), this wrapper uses the fixed dev
# ports because the driver drives ncps through dev-scripts/run.py, which is
# hardwired to them (S3 :9000, PostgreSQL :5432, MariaDB :3306, Redis :6379).
# The staging feature additionally requires Redis (the distributed locker).
#
# Pass-through args go to test-inflight-staging-contention-e2e.py, e.g.:
#   bash dev-scripts/test-inflight-staging-contention-auto.sh --storage s3 --window download

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# Bootstrap direnv so `nix`/`python3` see the devshell environment when this
# script is run standalone in a non-interactive shell (see .claude/rules/
# env-execution.md). A no-op when invoked via `task` inside the dev shell.
direnv status | grep -q "Found RC allowed 0" || direnv allow .
unset DIRENV_DIR DIRENV_FILE DIRENV_WATCHES DIRENV_DIFF && eval "$(direnv export bash)"

# Dedicated process-compose control port (avoids ncps :8501 / pprof :7501 and
# the CDC driver's :8511).
PC_PORT="${NCPS_STAGING_PC_PORT:-8512}"

cleanup() {
  echo "Stopping backing services (PC port: ${PC_PORT})..." >&2
  if command -v process-compose >/dev/null 2>&1; then
    process-compose down -p "${PC_PORT}" 2>/dev/null || true
  else
    nix run .#deps -- down -p "${PC_PORT}" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "Starting fixed-port backing services..." >&2
nix run .#deps -- up --detached --tui=false -p "${PC_PORT}"

ports_ready() {
  (echo > /dev/tcp/127.0.0.1/9000) 2>/dev/null \
    && (echo > /dev/tcp/127.0.0.1/5432) 2>/dev/null \
    && (echo > /dev/tcp/127.0.0.1/3306) 2>/dev/null \
    && (echo > /dev/tcp/127.0.0.1/6379) 2>/dev/null
}

echo "Waiting for all services to be ready (up to 120s)..." >&2
elapsed=0
until ports_ready; do
  sleep 2
  elapsed=$((elapsed + 2))
  if [[ ${elapsed} -ge 120 ]]; then
    echo "ERROR: Services did not become ready within 120s." >&2
    exit 1
  fi
done
echo "All services ready." >&2

python3 dev-scripts/test-inflight-staging-contention-e2e.py "$@"
