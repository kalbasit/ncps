#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 1 ]]; then
  echo "USAGE: $0 <functions.sql>" >&2
  exit 1
fi

readonly functions_file="$1"

# ---------------------------------------------------
# Check PGUSER connectivity
# ---------------------------------------------------

echo -n "Dev Connection Test... "
if psql -U "$PGUSER" -d "$PGDATABASE" -c "SELECT 1" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

# ---------------------------------------------------
# SETUP: Dev User (Standard)
# ---------------------------------------------------
echo "Creating dev user and database..."
psql -c "CREATE USER \"$PG_DEV_USER\" WITH PASSWORD '$PG_DEV_PASSWORD';"
psql -c "CREATE DATABASE \"$PG_DEV_DB\" OWNER \"$PG_DEV_USER\";"

# ---------------------------------------------------
# SETUP: Migration User (Standard)
# ---------------------------------------------------
echo "Creating migration user and database..."
psql -c "CREATE USER \"$PG_MIGRATION_USER\" WITH PASSWORD '$PG_MIGRATION_PASSWORD' CREATEDB;"
psql -c "CREATE DATABASE \"$PG_MIGRATION_DB\" OWNER \"$PG_MIGRATION_USER\";"

# ---------------------------------------------------
# SETUP: Test User (Restricted via dblink)
# ---------------------------------------------------
echo "Creating test user and constrained functions..."

# 1. Create the user (No CREATEDB permission)
psql -c "CREATE USER \"$PG_TEST_USER\" WITH PASSWORD '$PG_TEST_PASSWORD';"
psql -c "CREATE DATABASE \"$PG_TEST_DB\" OWNER \"$PG_TEST_USER\";"

# 2. Install dblink and create wrapper functions
# Note: We use dblink to bypass the transaction block restriction of CREATE/DROP DATABASE
psql -d "$PG_TEST_DB" -f "$functions_file" \
  -v dbname="$PGDATABASE" \
  -v pghost="$PGHOST" \
  -v pgport="$PGPORT" \
  -v pguser="$PGUSER"

echo "---------------------------------------------------"
echo "🔍 VERIFICATION CHECKS:"

# ---------------------------------------------------
# Check $PG_DEV_USER connectivity and operations
# ---------------------------------------------------

echo -n "Dev Connection Test... "
if psql -U "$PG_DEV_USER" -d "$PG_DEV_DB" -c "SELECT 1" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

echo -n "Dev Table Operations... "
if psql -U "$PG_DEV_USER" -d "$PG_DEV_DB" -c "
  CREATE TABLE IF NOT EXISTS test_table (
    id SERIAL PRIMARY KEY,
    message TEXT NOT NULL
  );
  INSERT INTO test_table (message) VALUES ('Test data');
" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

echo -n "Dev Query Test... "
if RESULT="$(psql -U "$PG_DEV_USER" -d "$PG_DEV_DB" -t -c "SELECT message FROM test_table WHERE message = 'Test data'" 2>/dev/null | xargs)" && [ "$RESULT" = "Test data" ]; then
  echo "✅ Success"
  echo "   Content verified: ✅"
else
  echo "❌ Failed"
  echo "   Expected: 'Test data', Got: '$RESULT'"
fi

echo -n "   Clean up after Dev Table Operations... "
if psql -U "$PG_DEV_USER" -d "$PG_DEV_DB" -c "
  DROP TABLE IF EXISTS test_table;
" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

# ---------------------------------------------------
# Check $PG_MIGRATION_USER connectivity and operations
# ---------------------------------------------------
echo -n "Migration Connection Test... "
if psql -U "$PG_MIGRATION_USER" -d "$PG_MIGRATION_DB" -c "SELECT 1" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

echo -n "Migration Table Operations... "
if psql -U "$PG_MIGRATION_USER" -d "$PG_MIGRATION_DB" -c "
  CREATE TABLE IF NOT EXISTS test_table (
    id SERIAL PRIMARY KEY,
    message TEXT NOT NULL
  );
  INSERT INTO test_table (message) VALUES ('Test data');
" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

echo -n "Migration Query Test... "
if RESULT="$(psql -U "$PG_MIGRATION_USER" -d "$PG_MIGRATION_DB" -t -c "SELECT message FROM test_table WHERE message = 'Test data'" 2>/dev/null | xargs)" && [ "$RESULT" = "Test data" ]; then
  echo "✅ Success"
  echo "   Content verified: ✅"
else
  echo "❌ Failed"
  echo "   Expected: 'Test data', Got: '$RESULT'"
  exit 1
fi

echo -n "   Clean up after Migration Table Operations... "
if psql -U "$PG_MIGRATION_USER" -d "$PG_MIGRATION_DB" -c "
  DROP TABLE IF EXISTS test_table;
" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

# ---------------------------------------------------
# Check $PG_TEST_USER connectivity, database create/drop and table operations
# ---------------------------------------------------

echo -n "Test USER Connection Test... "
if psql -U "$PG_TEST_USER" -d "$PG_TEST_DB" -c "SELECT 1" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

echo -n "Test Database Create Logic... "
if psql -U "$PG_TEST_USER" -d "$PG_TEST_DB" -c "SELECT create_test_db('test-123');" > /dev/null 2>&1; then
    echo "✅ Success"
else
    echo "❌ Failed (Could not run wrapper function)"
    exit 1
fi

echo -n "Test Database Connection Test... "
if psql -U "$PG_TEST_USER" -d test-123 -c "SELECT 1" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

echo -n "Test User Table Operations... "
if psql -U "$PG_TEST_USER" -d test-123 -c "
  CREATE TABLE IF NOT EXISTS test_table (
    id SERIAL PRIMARY KEY,
    message TEXT NOT NULL
  );
  INSERT INTO test_table (message) VALUES ('Test data');
" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

echo -n "Test User Query Test... "
if RESULT="$(psql -U "$PG_TEST_USER" -d test-123 -t -c "SELECT message FROM test_table WHERE message = 'Test data'" 2>/dev/null | xargs)" && [ "$RESULT" = "Test data" ]; then
  echo "✅ Success"
  echo "   Content verified: ✅"
else
  echo "❌ Failed"
  echo "   Expected: 'Test data', Got: '$RESULT'"
fi

echo -n "   Clean up after Test Table Operations... "
if psql -U "$PG_TEST_USER" -d test-123 -c "
  DROP TABLE IF EXISTS test_table;
" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

echo -n "Test Database Drop Logic... "
if psql -U "$PG_TEST_USER" -d "$PG_TEST_DB" -c "SELECT drop_test_db('test-123');" > /dev/null 2>&1; then
    echo "✅ Success"
else
    echo "❌ Failed (Could not run wrapper function)"
    exit 1
fi

echo "---------------------------------------------------"
echo ""
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║             NCPS POSTGRESQL CONFIGURATION                 ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo ""
echo "🗄️  Dev User:"
echo "  URL: postgresql://$PG_DEV_USER:$PG_DEV_PASSWORD@$PGHOST:$PGPORT/$PG_DEV_DB?sslmode=disable"
echo ""
echo "🧪 Test User (For Integration Tests):"
echo "  URL: postgresql://$PG_TEST_USER:$PG_TEST_PASSWORD@$PGHOST:$PGPORT/$PG_TEST_DB?sslmode=disable"
echo "  Capabilities: Can create/drop databases starting with 'test-'"
echo "  Create:   SELECT create_test_db('test-foo');"
echo "  Drop:     SELECT drop_test_db('test-foo');"
echo ""
echo "---------------------------------------------------"
