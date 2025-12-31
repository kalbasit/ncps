#!/usr/bin/env bash

set -euo pipefail

# Ensure the script runs in the context of the root directory
readonly root_dir="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

# S3/MinIO configuration (matches the settings from nix/process-compose/flake-module.nix)
S3_BUCKET="test-bucket"
S3_ENDPOINT="127.0.0.1:9000"
S3_REGION="us-east-1"
S3_ACCESS_KEY="test-access-key"
S3_SECRET_KEY="test-secret-key"

# Display usage information
usage() {
  cat <<EOF
Usage: $0 [STORAGE_BACKEND]

Run ncps development server with hot-reload.

STORAGE_BACKEND:
  local   Use local filesystem storage (default)
  s3|minio Use S3-compatible storage (MinIO)
          Requires running 'nix run .#deps' first to start MinIO

Examples:
  $0              # Run with local storage
  $0 local        # Run with local storage
  $0 s3           # Run with S3 storage (requires MinIO)

Note: When using S3 storage, make sure to start MinIO first:
  nix run .#deps  # In a separate terminal
EOF
  exit 1
}

# Parse storage backend argument
STORAGE_BACKEND="local"
if [[ $# -gt 0 ]]; then
  case "$1" in
    local|s3|minio)
      STORAGE_BACKEND="$1"
      shift
      ;;
    -h|--help|help)
      usage
      ;;
    *)
      # If the first argument is not a flag, treat it as an invalid backend.
      if [[ "$1" != -* ]]; then
        echo "Error: Invalid storage backend: '$1'" >&2
        echo "Run '$0 --help' for usage." >&2
        exit 1
      fi
      ;;
  esac
fi

case "$STORAGE_BACKEND" in
  local)
    echo "🗂️  Using local filesystem storage"
    ;;
  s3|minio)
    echo "☁️  Using S3-compatible storage (MinIO)"
    echo "⚠️  Make sure MinIO is running: nix run .#deps"
    STORAGE_BACKEND="s3"
    ;;
esac

# Create temporary data directory
ncps_datadir="$(mktemp -d)"
trap "rm -rf $ncps_datadir" EXIT

mkdir -p "$ncps_datadir/var/ncps/db"

# Initialize database
dbmate --url "sqlite:$ncps_datadir/var/ncps/db/db.sqlite" up

# Common arguments for both backends
common_args=(
  serve
  --cache-allow-put-verb
  --cache-hostname localhost
  --cache-database-url "sqlite:$ncps_datadir/var/ncps/db/db.sqlite"
  --cache-upstream-url https://cache.nixos.org
  --cache-upstream-url https://nix-community.cachix.org
  --cache-upstream-public-key cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
  --cache-upstream-public-key nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs=
)

# Storage-specific arguments
if [ "$STORAGE_BACKEND" = "local" ]; then
  storage_args=(
    --cache-storage-local "$ncps_datadir"
  )
else
  # S3/MinIO configuration (matches the settings from nix/process-compose/flake-module.nix)
  storage_args=(
    --cache-storage-s3-bucket "$S3_BUCKET"
    --cache-storage-s3-endpoint "$S3_ENDPOINT"
    --cache-storage-s3-region "$S3_REGION"
    --cache-storage-s3-access-key-id "$S3_ACCESS_KEY"
    --cache-storage-s3-secret-access-key "$S3_SECRET_KEY"
    --cache-storage-s3-use-ssl=false
  )
fi

# Run with watchexec for hot-reload
watchexec -e go -c clear -r go run . \
  "${common_args[@]}" \
  "${storage_args[@]}" \
  "$@"
