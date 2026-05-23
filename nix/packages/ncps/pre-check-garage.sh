echo "🚀 Starting Garage for S3 integration tests..."

# Create temporary directories for Garage state and config.
export GARAGE_META_DIR=$(mktemp -d)
export GARAGE_DATA_DIR=$(mktemp -d)
export GARAGE_CONFIG_FILE="$(mktemp -d)/garage.toml"
export HOME=$(mktemp -d)

# Generate random free ports using python.
# We bind to port 0, get the assigned port, and close the socket immediately.
# In a Nix sandbox, the race condition risk is negligible.
export NCPS_TEST_S3_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')
export GARAGE_RPC_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')
export GARAGE_ADMIN_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')

# Backend-neutral env vars consumed by Go tests.
export NCPS_TEST_S3_ENDPOINT="http://127.0.0.1:$NCPS_TEST_S3_PORT"
export NCPS_TEST_S3_REGION="us-east-1"
export NCPS_TEST_S3_BUCKET="test-bucket"
export NCPS_TEST_S3_ACCESS_KEY_ID="GK1234567890abcdef12345678"
export NCPS_TEST_S3_SECRET_ACCESS_KEY="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

# Garage-internal secrets — required by the config but not by tests.
export GARAGE_RPC_SECRET="0000000000000000000000000000000000000000000000000000000000000000"
export GARAGE_ADMIN_TOKEN="ncps-build-admin-token"

# Start Garage server in background.
bash $src/nix/process-compose/start-garage.sh >"$NIX_BUILD_TOP/garage.log" 2>&1 &
export GARAGE_PID=$!

# Wait for Garage to be ready (admin /health probe).
echo "⏳ Waiting for Garage to be ready..."
for i in {1..60}; do
  if curl -sf "http://127.0.0.1:$GARAGE_ADMIN_PORT/health" >/dev/null 2>&1; then
    echo "✅ Garage is ready"
    break
  fi
  if [ $i -eq 60 ]; then
    echo "❌ Garage failed to start"
    cat "$NIX_BUILD_TOP/garage.log" || true
    kill $GARAGE_PID 2>/dev/null || true
    exit 1
  fi
  sleep 1
done

# Bootstrap layout, bucket, key, and run the smoke test.
bash $src/nix/process-compose/init-garage.sh

echo "✅ Garage configured for S3 integration tests"
