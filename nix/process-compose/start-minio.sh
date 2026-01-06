#!/usr/bin/env bash

set -euo pipefail

DATA_DIR=$(mktemp -d)

echo "Storing ephemeral data in $DATA_DIR"
minio server "$DATA_DIR" --console-address ":$MINIO_CONSOLE_PORT"
