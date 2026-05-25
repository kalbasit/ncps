#!/usr/bin/env bash
set -euo pipefail

# Run with --start-only to allocate ports, start services, and return
# without running tests or tearing down (used by task test:deps:start).
START_ONLY=false
if [[ "${1:-}" == "--start-only" ]]; then
  START_ONLY=true
fi

STATE_FILE="${TMPDIR:-/tmp}/ncps-test-deps.env"

# Allocate 7 distinct free ports simultaneously.
# Binding all sockets before closing any ensures each port is unique
# and avoids races between the bind and the service startup.
ports_list=$(python3 -c "
import socket
ss = [socket.socket() for _ in range(7)]
for s in ss:
    s.bind(('', 0))
ports = [str(s.getsockname()[1]) for s in ss]
for s in ss:
    s.close()
print(' '.join(ports))
")
read -r NCPS_TEST_S3_PORT GARAGE_RPC_PORT GARAGE_ADMIN_PORT PGPORT MYSQL_TCP_PORT REDIS_PORT TEST_PC_PORT <<< "$ports_list"

export NCPS_TEST_S3_PORT GARAGE_RPC_PORT GARAGE_ADMIN_PORT PGPORT MYSQL_TCP_PORT REDIS_PORT TEST_PC_PORT
export NCPS_TEST_S3_ENDPOINT="http://127.0.0.1:${NCPS_TEST_S3_PORT}"

# Write state file so test:deps:stop can tear down this instance.
{
  echo "NCPS_TEST_S3_PORT=${NCPS_TEST_S3_PORT}"
  echo "GARAGE_RPC_PORT=${GARAGE_RPC_PORT}"
  echo "GARAGE_ADMIN_PORT=${GARAGE_ADMIN_PORT}"
  echo "PGPORT=${PGPORT}"
  echo "MYSQL_TCP_PORT=${MYSQL_TCP_PORT}"
  echo "REDIS_PORT=${REDIS_PORT}"
  echo "TEST_PC_PORT=${TEST_PC_PORT}"
} > "${STATE_FILE}"

# Registered before service startup so failures during startup or readiness
# polling are also caught and services are cleaned up.
cleanup() {
  echo "Stopping backing services (PC port: ${TEST_PC_PORT})..." >&2
  if command -v process-compose >/dev/null 2>&1; then
    process-compose down -p "${TEST_PC_PORT}" 2>/dev/null || true
  else
    nix run .#test-deps -- down -p "${TEST_PC_PORT}" 2>/dev/null || true
  fi
  rm -f "${STATE_FILE}"
}
trap cleanup EXIT

echo "Starting backing services on random ports..." >&2
echo "  S3/Garage: ${NCPS_TEST_S3_PORT}  PG: ${PGPORT}  MySQL: ${MYSQL_TCP_PORT}  Redis: ${REDIS_PORT}" >&2
echo "  (PC control port: ${TEST_PC_PORT}, state file: ${STATE_FILE})" >&2

nix run .#test-deps -- up --detached --tui=false -p "${TEST_PC_PORT}"

# Returns 0 if all four test-facing service ports are reachable.
ports_ready() {
  (echo > /dev/tcp/127.0.0.1/"${NCPS_TEST_S3_PORT}") 2>/dev/null \
    && (echo > /dev/tcp/127.0.0.1/"${PGPORT}") 2>/dev/null \
    && (echo > /dev/tcp/127.0.0.1/"${MYSQL_TCP_PORT}") 2>/dev/null \
    && (echo > /dev/tcp/127.0.0.1/"${REDIS_PORT}") 2>/dev/null
}

echo "Waiting for all services to be ready (up to 120s)..." >&2
elapsed=0
until ports_ready; do
  sleep 2
  elapsed=$((elapsed + 2))
  if [[ $elapsed -ge 120 ]]; then
    echo "ERROR: Services did not become ready within 120s." >&2
    exit 1
  fi
done
echo "All services ready." >&2

if [[ "$START_ONLY" == "true" ]]; then
  trap - EXIT
  exit 0
fi

eval "$(enable-integration-tests)"
go test -race ./...
