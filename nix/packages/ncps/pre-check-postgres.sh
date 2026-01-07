echo "🚀 Starting PostgreSQL for integration tests..."

# Create temporary directory for PostgreSQL data
export POSTGRES_DATA_DIR=$(mktemp -d)

# Generate random free port using python
export POSTGRES_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')

# Export the environment variables required by the init script and the tests
export PGHOST="127.0.0.1";
export PGPORT="$POSTGRES_PORT";
export PGUSER="postgres";
export PGDATABASE="postgres";
export PG_DEV_DB="dev-db";
export PG_DEV_USER="dev-user";
export PG_DEV_PASSWORD="dev-password";
export PG_TEST_DB="test-db";
export PG_TEST_USER="test-user";
export PG_TEST_PASSWORD="test-password";

# Start PostgreSQL server in background
bash $src/nix/process-compose/start-postgres.sh &
export POSTGRES_PID=$!

# Wait for PostgreSQL to be ready
echo "⏳ Waiting for PostgreSQL to be ready..."
for i in {1..30}; do
  if pg_isready >/dev/null 2>&1; then
    echo "✅ PostgreSQL is ready"
    break
  fi
  if [ $i -eq 30 ]; then
    echo "❌ PostgreSQL failed to start"
    kill $POSTGRES_PID 2>/dev/null || true
    exit 1
  fi
  sleep 1
done

# Call the init script
bash $src/nix/process-compose/init-postgres.sh $src/nix/process-compose/postgres-dblink-create-drop-functions.sql

# Export PostgreSQL test environment variable
export NCPS_TEST_ADMIN_POSTGRES_URL="postgresql://$PG_TEST_DB:$PG_TEST_PASSWORD@$PGHOST:$PGPORT/$PG_TEST_DB?sslmode=disable"

echo "✅ PostgreSQL configured for integration tests"

