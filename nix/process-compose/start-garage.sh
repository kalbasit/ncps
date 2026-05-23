#!/usr/bin/env bash
# Starts a single-node Garage server for local development and integration tests.
#
# Expects the following env vars (set by start-garage scripts in flake-module.nix
# and pre-check-garage.sh):
#   NCPS_TEST_S3_ENDPOINT       - e.g. http://127.0.0.1:9000
#   NCPS_TEST_S3_PORT           - S3 API port (default 9000)
#   NCPS_TEST_S3_REGION         - e.g. us-east-1
#   GARAGE_RPC_PORT             - RPC port (default 3901)
#   GARAGE_ADMIN_PORT           - admin API port (default 3903)
#   GARAGE_DATA_DIR             - data dir (must exist; will hold blobs)
#   GARAGE_META_DIR             - metadata dir (must exist; will hold sqlite db)
#   GARAGE_CONFIG_FILE          - path to write the rendered Garage config
#   GARAGE_RPC_SECRET           - 64-char hex string for RPC auth
#   GARAGE_ADMIN_TOKEN          - admin API bearer token (any string)
set -euo pipefail

: "${NCPS_TEST_S3_PORT:=9000}"
: "${NCPS_TEST_S3_REGION:=us-east-1}"
: "${GARAGE_RPC_PORT:=3901}"
: "${GARAGE_ADMIN_PORT:=3903}"

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

exec garage -c "$GARAGE_CONFIG_FILE" server
