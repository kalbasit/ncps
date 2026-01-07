#!/usr/bin/env bash

set -euo pipefail

# Ensure the script runs in the context of the root directory
readonly root_dir="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

# Colors for output
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly NC='\033[0m' # No Color

# Database URLs constructed from source files
readonly POSTGRES_URL="postgresql://dev-user:dev-password@127.0.0.1:5432/dev-db?sslmode=disable"
readonly MYSQL_URL="mysql://dev-user:dev-password@127.0.0.1:3306/dev-db"
readonly SQLITE_DIR="$(mktemp -d)"
readonly SQLITE_URL="sqlite:${SQLITE_DIR}/db.sqlite"

trap "rm -rf $SQLITE_DIR" EXIT

check_dependency() {
  local name="$1"
  shift

  # Special handling for SQLite (file check only)
  if [[ "$name" == "SQLite" ]]; then
    mkdir -p "$SQLITE_DIR"
    echo -e "${GREEN}✓ ${name} directory ready${NC}"
    return 0
  fi

  if ! "$@" >/dev/null 2>&1; then
    echo -e "${RED}ERROR: ${name} is not running or accessible${NC}"
    echo -e "${YELLOW}Skipping migration for ${name}...${NC}"
    return 1
  fi
  echo -e "${GREEN}✓ ${name} is ready${NC}"
}

echo -e "${BLUE}Starting migrations for all detected database configurations...${NC}"
echo ""

# --- PostgreSQL ---
echo -e "${YELLOW}1. Migrating PostgreSQL...${NC}"
if check_dependency "PostgreSQL" pg_isready -h 127.0.0.1 -p 5432 -U test-user; then
  dbmate --url "${POSTGRES_URL}" up
  echo -e "${GREEN}PostgreSQL migration complete.${NC}"
else
  echo -e "${RED}Skipped PostgreSQL.${NC}"
fi
echo ""

# --- MySQL ---
echo -e "${YELLOW}2. Migrating MySQL...${NC}"
# Note: mysqladmin ping often requires password if set, depending on local config.
# Using the credentials from the URL for the check to be safe.
if check_dependency "MySQL" mysqladmin -h 127.0.0.1 -P 3306 -u dev-user --password=dev-password ping; then
  dbmate --url "${MYSQL_URL}" up
  echo -e "${GREEN}MySQL migration complete.${NC}"
else
  echo -e "${RED}Skipped MySQL.${NC}"
fi
echo ""

# --- SQLite ---
echo -e "${YELLOW}3. Migrating SQLite...${NC}"
check_dependency "SQLite"
dbmate --url "${SQLITE_URL}" up
echo -e "${GREEN}SQLite migration complete.${NC}"

echo ""
echo -e "${GREEN}All requested migrations finished.${NC}"
