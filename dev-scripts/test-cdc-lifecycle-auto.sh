#!/usr/bin/env bash
set -euo pipefail

# test-cdc-lifecycle-auto.sh — start the FIXED-port dev backends
# (nix run .#deps), run the CDC lifecycle e2e driver, then tear the
# backends down.
#
# Unlike test-auto.sh (which allocates random ports for the Go
# integration suite), the CDC lifecycle driver drives ncps through
# dev-scripts/run.py, which is hardwired to the fixed dev ports that
# `nix run .#deps` provides (S3 :9000, PostgreSQL :5432, MariaDB :3306,
# Redis :6379). So this wrapper must use the fixed-port stack.
#
# Pass-through args go to test-cdc-lifecycle-e2e.py, e.g.:
#   bash dev-scripts/test-cdc-lifecycle-auto.sh --db sqlite --storage local

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# Dedicated process-compose control port (avoids ncps :8501 / pprof :7501).
PC_PORT="${NCPS_CDC_PC_PORT:-8511}"

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

python3 dev-scripts/test-cdc-lifecycle-e2e.py "$@"
