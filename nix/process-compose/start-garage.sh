#!/usr/bin/env bash
# Starts a single-node Garage server for local development and integration tests.
#
# Expects the following env vars (set by flake-module.nix or pre-check-garage.sh):
#   NCPS_TEST_S3_ENDPOINT       - e.g. http://127.0.0.1:9000
#   NCPS_TEST_S3_PORT           - S3 API port (default 9000)
#   NCPS_TEST_S3_REGION         - e.g. us-east-1
#   GARAGE_RPC_PORT             - RPC port (default 3901)
#   GARAGE_ADMIN_PORT           - admin API port (default 3903)
#   GARAGE_RPC_SECRET           - 64-char hex string for RPC auth
#   GARAGE_ADMIN_TOKEN          - admin API bearer token (any string)
#
# Storage is ephemeral: a fresh `mktemp -d` workdir per run (matching the
# pattern used by start-postgres.sh / start-mysql.sh / start-redis.sh) is
# created and removed on shutdown. The workdir path is written to a per-UID
# pointer file so init-garage.sh can find the same config file the server is
# using; the pointer file is removed on exit too.
set -euo pipefail

: "${NCPS_TEST_S3_PORT:=9000}"
: "${NCPS_TEST_S3_REGION:=us-east-1}"
: "${GARAGE_RPC_PORT:=3901}"
: "${GARAGE_ADMIN_PORT:=3903}"

WORKDIR=$(mktemp -d -t "ncps-garage-$(id -u)-XXXXXXXX")
GARAGE_DATA_DIR="$WORKDIR/data"
GARAGE_META_DIR="$WORKDIR/meta"
GARAGE_CONFIG_FILE="$WORKDIR/garage.toml"
mkdir -p "$GARAGE_DATA_DIR" "$GARAGE_META_DIR"
echo "Storing ephemeral Garage data in $WORKDIR"

POINTER_DIR="${TMPDIR:-/tmp}"
POINTER_FILE="$POINTER_DIR/ncps-garage-$(id -u).workdir"
echo "$WORKDIR" > "$POINTER_FILE"

cleanup() {
  trap - EXIT
  if [ -n "${GARAGE_PID:-}" ] && kill -0 "$GARAGE_PID" 2>/dev/null; then
    kill -TERM "$GARAGE_PID" 2>/dev/null || true
    wait "$GARAGE_PID" 2>/dev/null || true
  fi
  rm -rf "$WORKDIR"
  rm -f "$POINTER_FILE"
}
trap cleanup EXIT INT TERM

cat >"$GARAGE_CONFIG_FILE" <<EOF
metadata_dir = "$GARAGE_META_DIR"
data_dir = "$GARAGE_DATA_DIR"

db_engine = "sqlite"

replication_factor = 1

rpc_bind_addr = "127.0.0.1:$GARAGE_RPC_PORT"
rpc_public_addr = "127.0.0.1:$GARAGE_RPC_PORT"
rpc_secret = "$GARAGE_RPC_SECRET"

[s3_api]
s3_region = "$NCPS_TEST_S3_REGION"
api_bind_addr = "127.0.0.1:$NCPS_TEST_S3_PORT"
root_domain = ".s3.local"

[s3_web]
bind_addr = "127.0.0.1:0"
root_domain = ".web.local"
index = "index.html"

[admin]
api_bind_addr = "127.0.0.1:$GARAGE_ADMIN_PORT"
admin_token = "$GARAGE_ADMIN_TOKEN"
metrics_token = "$GARAGE_ADMIN_TOKEN"
EOF

garage -c "$GARAGE_CONFIG_FILE" server &
GARAGE_PID=$!
wait "$GARAGE_PID"
