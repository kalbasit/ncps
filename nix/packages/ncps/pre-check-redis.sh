echo "🚀 Starting Redis for integration tests..."

# Create temporary directory for Redis data
export REDIS_DATA_DIR=$(mktemp -d)

# Generate random free port using python
export REDIS_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')

export REDIS_HOST="127.0.0.1"

# Start Redis server in background
bash $src/nix/process-compose/start-redis.sh > "$NIX_BUILD_TOP/redis.stdout" 2> "$NIX_BUILD_TOP/redis.stderr" &
export REDIS_PID=$!

# Wait for Redis to be ready
echo "⏳ Waiting for Redis to be ready..."
for i in {1..30}; do
  if redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" ping >/dev/null 2>&1; then
    echo "✅ Redis is ready on port $REDIS_PORT"
    break
  fi
  if [ $i -eq 30 ]; then
    echo "❌ Redis failed to start"
    kill $REDIS_PID 2>/dev/null || true
    exit 1
  fi
  sleep 1
done

# Export Redis test environment variables
export NCPS_ENABLE_REDIS_TESTS=1
export NCPS_TEST_REDIS_ADDRS="$REDIS_HOST:$REDIS_PORT"

echo "✅ Redis configured for integration tests"
