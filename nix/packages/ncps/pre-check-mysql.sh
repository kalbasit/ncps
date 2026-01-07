echo "🚀 Starting MariaDB for integration tests..."

# Create temporary directory for MariaDB data
export MYSQL_DATA_DIR=$(mktemp -d)

# Generate random free port using python
export MYSQL_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')

# Export the environment variables required by the init script and the tests
export MYSQL_TCP_PORT="$MYSQL_PORT";
export MYSQL_HOST="127.0.0.1";
export MYSQL_USER="root";
export MYSQL_DEV_DB="dev-db";
export MYSQL_DEV_USER="dev-user";
export MYSQL_DEV_PASSWORD="dev-password";
export MYSQL_TEST_DB="test-db";
export MYSQL_TEST_USER="test-user";
export MYSQL_TEST_PASSWORD="test-password";

# Start MariaDB server in background
bash $src/nix/process-compose/start-mysql.sh &
export MYSQL_PID=$!

# Wait for MariaDB to be ready
echo "⏳ Waiting for MariaDB to be ready..."
for i in {1..30}; do
  if mariadb-admin -h "$MYSQL_HOST" -P "$MYSQL_PORT" ping >/dev/null 2>&1; then
    echo "✅ MariaDB is ready"
    break
  fi
  if [ $i -eq 30 ]; then
    echo "❌ MariaDB failed to start"
    kill $MYSQL_PID 2>/dev/null || true
    exit 1
  fi
  sleep 1
done

# Call the init script
bash $src/nix/process-compose/init-mysql.sh

# Export MySQL test environment variable
export NCPS_TEST_ADMIN_MYSQL_URL="mysql://$MYSQL_TEST_USER:$MYSQL_TEST_PASSWORD@$MYSQL_HOST:$MYSQL_PORT/$MYSQL_TEST_DB"

echo "✅ MariaDB configured for integration tests"

