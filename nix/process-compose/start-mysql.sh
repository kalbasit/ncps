#!/usr/bin/env bash

set -euo pipefail

DATA_DIR=$(mktemp -d)

echo "Storing ephemeral MariaDB data in $DATA_DIR"
mariadb-install-db --datadir="$DATA_DIR" --auth-root-authentication-method=normal

mariadbd \
  --datadir="$DATA_DIR" \
  --bind-address="$MYSQL_HOST" \
  --port="$MYSQL_TCP_PORT" \
  --socket="$DATA_DIR/mysql.sock" \
  --skip-networking=0
