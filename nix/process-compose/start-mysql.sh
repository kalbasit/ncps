#!/usr/bin/env bash

set -euo pipefail

DATA_DIR=$(mktemp -d)

echo "Storing ephemeral MariaDB data in $DATA_DIR"
# --no-defaults: ignore the system option files (/etc/mysql/my.cnf etc.). A
# GitHub-hosted runner ships a pre-installed MySQL whose my.cnf sets
# `user=mysql` and the MySQL-only `mysqlx-bind-address`. MariaDB reads those by
# default, so it both tries to chown the datadir to the (unwritable) `mysql`
# user and then aborts on the unknown `mysqlx-bind-address` variable, leaving
# mariadbd unable to bind its port. Ignoring the system config lets the install
# and server run rootless as the current user; every option we rely on is
# passed explicitly here.
mariadb-install-db --no-defaults --datadir="$DATA_DIR" --auth-root-authentication-method=normal

exec mariadbd \
  --no-defaults \
  --datadir="$DATA_DIR" \
  --bind-address="$MYSQL_HOST" \
  --port="$MYSQL_TCP_PORT" \
  --socket="$DATA_DIR/mysql.sock" \
  --skip-networking=0
