#!/usr/bin/env bash

set -euo pipefail

DATA_DIR=$(mktemp -d)

echo "Storing ephemeral data in $DATA_DIR"
exec minio server "$DATA_DIR" \
  --address ":$MINIO_PORT" \
  --console-address ":$MINIO_CONSOLE_PORT"
