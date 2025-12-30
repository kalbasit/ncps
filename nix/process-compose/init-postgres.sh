#!/usr/bin/env bash

set -e

# Remove stale marker file from previous runs
rm -f /tmp/ncps-postgres-ready

# Create test user and database
echo "Creating test user and database..."
psql -h 127.0.0.1 -p 5432 -U postgres -d postgres -c "CREATE USER \"test-user\" WITH PASSWORD 'test-password';"
psql -h 127.0.0.1 -p 5432 -U postgres -d postgres -c "CREATE DATABASE \"test-db\" OWNER \"test-user\";"

echo "---------------------------------------------------"
echo "🔍 VERIFICATION CHECKS:"

# Check A: Connection with test credentials
echo -n "1. Connection Test... "
if psql -h 127.0.0.1 -p 5432 -U test-user -d test-db -c "SELECT 1" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

# Check B: Create test table and insert data
echo -n "2. Table Operations... "
if psql -h 127.0.0.1 -p 5432 -U test-user -d test-db -c "
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

# Check C: Query test data
echo -n "3. Query Test... "
RESULT=$(psql -h 127.0.0.1 -p 5432 -U test-user -d test-db -t -c "SELECT message FROM test_table WHERE message = 'Test data'" 2>/dev/null | xargs)
if [ "$RESULT" = "Test data" ]; then
  echo "✅ Success"
  echo "   Content verified: ✅"
else
  echo "❌ Failed"
  echo "   Expected: 'Test data', Got: '$RESULT'"
fi

echo "---------------------------------------------------"
echo ""
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║           NCPS POSTGRESQL CONFIGURATION                   ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo ""
echo "🗄️  PostgreSQL Database Configuration:"
echo ""
echo "  Host:         127.0.0.1"
echo "  Port:         5432"
echo "  Database:     test-db"
echo "  Username:     test-user"
echo "  Password:     test-password"
echo ""
echo "🔗 Connection URL:"
echo "  postgresql://test-user:test-password@127.0.0.1:5432/test-db?sslmode=disable"
echo ""
echo "💡 Environment Variables (for testing):"
echo "  export NCPS_TEST_POSTGRES_URL=\"postgresql://test-user:test-password@127.0.0.1:5432/test-db?sslmode=disable\""
echo ""
echo "---------------------------------------------------"

# Create ready marker file for process-compose health check
touch /tmp/ncps-postgres-ready

sleep infinity
