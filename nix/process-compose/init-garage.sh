#!/usr/bin/env bash
# Bootstraps the local Garage instance:
#   - assigns a single-node layout
#   - creates the test bucket
#   - imports the fixed test access key
#   - grants the key read+write on the bucket
#   - runs a put/get/presign smoke test using awscli2
#   - verifies anonymous access is blocked
#
# Expects env vars set by flake-module.nix or pre-check-garage.sh:
#   NCPS_TEST_S3_ENDPOINT, NCPS_TEST_S3_REGION, NCPS_TEST_S3_BUCKET,
#   NCPS_TEST_S3_ACCESS_KEY_ID, NCPS_TEST_S3_SECRET_ACCESS_KEY
#
# Locates the Garage config by reading the per-UID pointer file written by
# start-garage.sh. Override by exporting GARAGE_CONFIG_FILE before invocation
# (e.g. from pre-check-garage.sh in the Nix build sandbox).
set -euo pipefail

if [ -z "${GARAGE_CONFIG_FILE:-}" ]; then
  POINTER_FILE="${TMPDIR:-/tmp}/ncps-garage-$(id -u).workdir"
  if [ ! -f "$POINTER_FILE" ]; then
    echo "❌ Garage workdir pointer not found at $POINTER_FILE; is start-garage.sh running?" >&2
    exit 1
  fi
  GARAGE_CONFIG_FILE="$(cat "$POINTER_FILE")/garage.toml"
fi

garage_cli() { garage -c "$GARAGE_CONFIG_FILE" "$@"; }

# ---------------------------------------------------
# Wait for the Garage node to be reachable via RPC.
# `garage status` exits non-zero until the server is up.
# ---------------------------------------------------
echo "⏳ Waiting for Garage RPC to come up..."
for _ in $(seq 1 60); do
  if garage_cli status >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done
garage_cli status >/dev/null

# ---------------------------------------------------
# Assign single-node layout (idempotent).
# Look up the node ID, then assign only if not yet in the layout.
# ---------------------------------------------------
NODE_ID=$(garage_cli node id -q | cut -d@ -f1)

if ! garage_cli layout show 2>/dev/null | grep -q "$NODE_ID"; then
  garage_cli layout assign -z dc1 -c 1G "$NODE_ID"
  # `layout apply` requires --version N where N is the new version number;
  # for a fresh cluster the first apply is version 1.
  LAYOUT_VERSION=$(garage_cli layout show 2>/dev/null \
    | awk '/Current cluster layout version:/ { print $5 + 1 }')
  : "${LAYOUT_VERSION:=1}"
  garage_cli layout apply --version "$LAYOUT_VERSION"
fi

# ---------------------------------------------------
# Create test bucket (idempotent).
# ---------------------------------------------------
if ! garage_cli bucket info "$NCPS_TEST_S3_BUCKET" >/dev/null 2>&1; then
  garage_cli bucket create "$NCPS_TEST_S3_BUCKET"
fi

# ---------------------------------------------------
# Import the fixed test access key (idempotent).
# `garage key import` in Garage 1.x takes only the access key id and secret;
# the key is referenced later by its access key id.
# ---------------------------------------------------
if ! garage_cli key info "$NCPS_TEST_S3_ACCESS_KEY_ID" >/dev/null 2>&1; then
  garage_cli key import --yes \
    "$NCPS_TEST_S3_ACCESS_KEY_ID" \
    "$NCPS_TEST_S3_SECRET_ACCESS_KEY"
fi

# Grant the key read+write on the bucket (idempotent — repeated grants are no-ops).
garage_cli bucket allow --read --write --owner \
  "$NCPS_TEST_S3_BUCKET" \
  --key "$NCPS_TEST_S3_ACCESS_KEY_ID"

# ---------------------------------------------------
# Smoke test: put / get via presigned URL using awscli2.
# Tests the same code path ncps uses.
# ---------------------------------------------------
export AWS_ACCESS_KEY_ID="$NCPS_TEST_S3_ACCESS_KEY_ID"
export AWS_SECRET_ACCESS_KEY="$NCPS_TEST_S3_SECRET_ACCESS_KEY"
export AWS_DEFAULT_REGION="$NCPS_TEST_S3_REGION"
export AWS_EC2_METADATA_DISABLED=true

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
echo "This is secret data!" >"$TMP/message.txt"

aws --endpoint-url "$NCPS_TEST_S3_ENDPOINT" s3 cp \
  --quiet "$TMP/message.txt" "s3://$NCPS_TEST_S3_BUCKET/message.txt"

# Check A: signed download via awscli
aws --endpoint-url "$NCPS_TEST_S3_ENDPOINT" s3 cp \
  --quiet "s3://$NCPS_TEST_S3_BUCKET/message.txt" "$TMP/downloaded.txt"
if ! diff -q "$TMP/message.txt" "$TMP/downloaded.txt" >/dev/null; then
  echo "❌ Signed S3 download did not match upload"
  exit 1
fi

# Check B: anonymous (unsigned) GET should be rejected.
HTTP_CODE=$(curl -o /dev/null --silent --head --write-out '%{http_code}' \
  "$NCPS_TEST_S3_ENDPOINT/$NCPS_TEST_S3_BUCKET/message.txt")
if [ "$HTTP_CODE" = "200" ]; then
  echo "❌ Anonymous access to bucket succeeded (HTTP $HTTP_CODE). Bucket is public."
  exit 1
fi

# Check C: presigned URL (1h) should succeed.
SIGNED_URL=$(aws --endpoint-url "$NCPS_TEST_S3_ENDPOINT" s3 presign \
  --expires-in 3600 "s3://$NCPS_TEST_S3_BUCKET/message.txt")
SIGNED_BODY=$(curl --silent --fail "$SIGNED_URL")
if [ "$SIGNED_BODY" != "This is secret data!" ]; then
  echo "❌ Presigned URL fetch returned unexpected body: $SIGNED_BODY"
  exit 1
fi

# ---------------------------------------------------
# Banner
# ---------------------------------------------------
echo
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║           NCPS GARAGE CONFIGURATION                       ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo
echo "  Endpoint:     $NCPS_TEST_S3_ENDPOINT"
echo "  Region:       $NCPS_TEST_S3_REGION"
echo "  Bucket:       $NCPS_TEST_S3_BUCKET"
echo "  Access Key:   $NCPS_TEST_S3_ACCESS_KEY_ID"
echo "  Secret Key:   $NCPS_TEST_S3_SECRET_ACCESS_KEY"
echo
echo "  Garage CLI:   garage -c \$GARAGE_CONFIG_FILE <cmd>"
echo "                (e.g. garage status, garage bucket list)"
echo
