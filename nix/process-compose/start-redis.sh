#!/usr/bin/env bash

set -euo pipefail

DATA_DIR=$(mktemp -d)

echo "Storing ephemeral Redis data in $DATA_DIR"
redis-server \
  --dir "$DATA_DIR" \
  --bind "$REDIS_HOST" \
  --port "$REDIS_PORT" \
  --save "" \
  --appendonly no
