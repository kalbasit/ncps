#!/usr/bin/env bash

set -euo pipefail

DATA_DIR=$(mktemp -d)

echo "Storing ephemeral MariaDB data in $DATA_DIR"
# --user="$(id -un)": mariadb-install-db otherwise defaults to the 'mysql'
# system user and tries to chown the datadir to it. That chown fails with
# EPERM for an unprivileged user that cannot change file ownership (e.g. the
# `runner` user on GitHub-hosted CI), aborting install so mariadbd never binds
# its port. Targeting the current user makes the chown a no-op and lets the
# install proceed rootless.
mariadb-install-db --datadir="$DATA_DIR" --user="$(id -un)" --auth-root-authentication-method=normal

exec mariadbd \
  --datadir="$DATA_DIR" \
  --bind-address="$MYSQL_HOST" \
  --port="$MYSQL_TCP_PORT" \
  --socket="$DATA_DIR/mysql.sock" \
  --skip-networking=0
