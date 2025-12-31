#!/usr/bin/env bash

set -euo pipefail

# Ensure the script runs in the context of the root directory
readonly root_dir="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

# Colors for output
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly NC='\033[0m' # No Color

# Configuration (matches nix/process-compose/flake-module.nix)
readonly S3_BUCKET="test-bucket"
readonly S3_ENDPOINT="127.0.0.1:9000"
readonly S3_REGION="us-east-1"
readonly S3_ACCESS_KEY="test-access-key"
readonly S3_SECRET_KEY="test-secret-key"
readonly POSTGRES_URL="postgresql://test-user:test-password@127.0.0.1:5432/test-db?sslmode=disable"
readonly REDIS_ADDR="127.0.0.1:6379"

# Number of instances to run
readonly NUM_INSTANCES=3
readonly BASE_PORT=8501

# PIDs of running instances
declare -a INSTANCE_PIDS=()

# Cleanup function
cleanup() {
  echo -e "\n${YELLOW}Shutting down ncps instances...${NC}"

  for pid in "${INSTANCE_PIDS[@]}"; do
    if kill -0 "$pid" 2>/dev/null; then
      echo -e "${BLUE}Stopping instance with PID $pid${NC}"
      kill "$pid" 2>/dev/null || true
    fi
  done

  # Wait for processes to terminate
  sleep 1

  # Force kill if still running
  for pid in "${INSTANCE_PIDS[@]}"; do
    if kill -0 "$pid" 2>/dev/null; then
      echo -e "${RED}Force killing instance with PID $pid${NC}"
      kill -9 "$pid" 2>/dev/null || true
    fi
  done

  echo -e "${GREEN}All instances stopped${NC}"
}

# Set up cleanup trap
trap cleanup EXIT INT TERM

# Display usage information
usage() {
  cat <<EOF
Usage: $0 [OPTIONS]

Run multiple ncps instances in high-availability mode with hot-reload.

This script starts ${NUM_INSTANCES} ncps instances that share:
  - PostgreSQL database (port 5432)
  - S3/MinIO storage (port 9000)
  - Redis for distributed locking (port 6379)

Prerequisites:
  Run 'nix run .#deps' in a separate terminal to start:
    - PostgreSQL
    - MinIO (S3-compatible storage)
    - Redis

Instances will run on ports:
EOF
  for ((i=1; i<=NUM_INSTANCES; i++)); do
    port=$((BASE_PORT + i - 1))
    echo "  - Instance $i: http://localhost:$port"
  done

  cat <<EOF

Options:
  -h, --help    Show this help message

Examples:
  $0            # Start ${NUM_INSTANCES} instances in HA mode

After starting, test with:
EOF
  for ((i=1; i<=NUM_INSTANCES; i++)); do
    port=$((BASE_PORT + i - 1))
    echo "  curl http://localhost:$port/pubkey  # Instance $i"
  done

  exit 1
}

check_dependency() {
  local name="$1"
  shift

  if ! "$@" >/dev/null 2>&1; then
    echo -e "${RED}ERROR: ${name} is not running${NC}"
    echo -e "${YELLOW}Start dependencies with: nix run .#deps${NC}"
    exit 1
  fi
  echo -e "${GREEN}✓ ${name} is ready${NC}"
}

# Parse arguments
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help|help)
      usage
      ;;
    *)
      echo -e "${RED}Unknown option: $1${NC}"
      usage
      ;;
  esac
  shift
done

# Check if dependencies are running
echo -e "${BLUE}Checking dependencies...${NC}"
check_dependency "PostgreSQL" pg_isready -h 127.0.0.1 -p 5432 -U test-user
check_dependency "MinIO" curl -s http://127.0.0.1:9000/minio/health/live
check_dependency "Redis" redis-cli -h 127.0.0.1 -p 6379 ping

echo ""

# Function to start a single instance
start_instance() {
  local instance_num=$1
  local port=$((BASE_PORT + instance_num - 1))
  local args=(
    serve
    --cache-allow-put-verb
    --cache-hostname="cache-ha-${instance_num}.example.com"
    --cache-database-url="'${POSTGRES_URL}'"
    --cache-upstream-url="https://cache.nixos.org"
    --cache-upstream-public-key="cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="
    --cache-storage-s3-bucket="$S3_BUCKET"
    --cache-storage-s3-endpoint="$S3_ENDPOINT"
    --cache-storage-s3-region="$S3_REGION"
    --cache-storage-s3-access-key-id="$S3_ACCESS_KEY"
    --cache-storage-s3-secret-access-key="$S3_SECRET_KEY"
    --cache-storage-s3-use-ssl="false"
    --cache-redis-addrs="${REDIS_ADDR}"
    --cache-lock-download-ttl="5m"
    --cache-lock-lru-ttl="30m"
    --server-addr=":${port}"
  )

  echo -e "${BLUE}Starting instance ${instance_num} on port ${port}...${NC}"

  # Start instance with watchexec for hot-reload
  watchexec -e go -c clear -r go run . \
    "${args[@]}" \
    2>&1 | sed "s/^/[Instance ${instance_num}] /" &

  INSTANCE_PIDS+=($!)
  echo -e "${GREEN}✓ Instance ${instance_num} started (PID: ${INSTANCE_PIDS[-1]}, Port: ${port})${NC}"
}

# Migrating the database
echo -e "${YELLOW}Migrating the database...${NC}"
echo ""
DBMATE_NO_DUMP_SCHEMA=true dbmate --url "${POSTGRES_URL}" up

# Start all instances
echo -e "${YELLOW}Starting ${NUM_INSTANCES} ncps instances in HA mode...${NC}"
echo ""

for ((i=1; i<=NUM_INSTANCES; i++)); do
  start_instance "$i"
  sleep 1  # Stagger startup
done

echo ""
echo -e "${GREEN}All instances started!${NC}"
echo ""
echo -e "${YELLOW}Instances running on:${NC}"
for ((i=1; i<=NUM_INSTANCES; i++)); do
  port=$((BASE_PORT + i - 1))
  echo -e "  ${BLUE}Instance $i:${NC} http://localhost:$port"
done
echo ""
echo -e "${YELLOW}Shared resources:${NC}"
echo -e "  ${BLUE}Database:${NC}    PostgreSQL on 127.0.0.1:5432"
echo -e "  ${BLUE}Storage:${NC}     MinIO (S3) on 127.0.0.1:9000"
echo -e "  ${BLUE}Locks:${NC}       Redis on 127.0.0.1:6379"
echo ""
echo -e "${YELLOW}Test endpoints:${NC}"
for ((i=1; i<=NUM_INSTANCES; i++)); do
  port=$((BASE_PORT + i - 1))
  echo -e "  curl http://localhost:$port/pubkey  # Instance $i"
done
echo ""
echo -e "${YELLOW}Monitor Redis locks:${NC}"
echo -e "  redis-cli --scan --pattern 'ncps:lock:*'"
echo ""
echo -e "${GREEN}Press Ctrl+C to stop all instances${NC}"

# Wait for all instances
wait
