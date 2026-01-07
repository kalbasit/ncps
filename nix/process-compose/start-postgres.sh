#!/usr/bin/env bash

set -euo pipefail

DATA_DIR=$(mktemp -d)

echo "Storing ephemeral PostgreSQL data in $DATA_DIR"
initdb -D "$DATA_DIR" -U "$PGUSER" --no-locale --encoding=UTF8

echo "host all all 127.0.0.1/32 trust" >> "$DATA_DIR/pg_hba.conf"

{
  echo "listen_addresses = '127.0.0.1'"
  echo "port = $PGPORT"
  echo "unix_socket_directories = '$DATA_DIR'"
}  >> "$DATA_DIR/postgresql.conf"

exec postgres -D "$DATA_DIR" -k "$DATA_DIR"
