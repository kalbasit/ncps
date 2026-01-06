echo "🚀 Starting MinIO for S3 integration tests..."

# Create temporary directories for MinIO data and config
export MINIO_DATA_DIR=$(mktemp -d)
export HOME=$(mktemp -d)

# Generate random free ports using python
# We bind to port 0, get the assigned port, and close the socket immediately.
# In a Nix sandbox, the race condition risk (port being stolen between check and use) is negligible.
export MINIO_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')
export CONSOLE_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')

# Export the environment variables required by the init script and the tests
export MINIO_ENDPOINT="http://127.0.0.1:$MINIO_PORT";
export MINIO_CONSOLE="http://127.0.0.1:$CONSOLE_PORT";
export MINIO_REGION="us-east-1";
export MINIO_ROOT_PASSWORD="password";
export MINIO_ROOT_USER="admin";
export MINIO_TEST_S3_ACCESS_KEY_ID="test-access-key";
export MINIO_TEST_S3_BUCKET="test-bucket";
export MINIO_TEST_S3_SECRET_ACCESS_KEY="test-secret-key";

# Start MinIO server in background
minio server "$MINIO_DATA_DIR" \
  --address "127.0.0.1:$MINIO_PORT" \
  --console-address "127.0.0.1:$CONSOLE_PORT" &
export MINIO_PID=$!

# Wait for MinIO to be ready
echo "⏳ Waiting for MinIO to be ready..."
for i in {1..30}; do
  if curl -sf "$MINIO_ENDPOINT/minio/health/live"; then
    echo "✅ MinIO is ready"
    break
  fi
  if [ $i -eq 30 ]; then
    echo "❌ MinIO failed to start"
    kill $MINIO_PID 2>/dev/null || true
    exit 1
  fi
  sleep 1
done

# Call the init script
bash $src/nix/process-compose/init-minio.sh

# Export S3 test environment variables
export NCPS_TEST_S3_BUCKET="$MINIO_TEST_S3_BUCKET"
export NCPS_TEST_S3_ENDPOINT="$MINIO_ENDPOINT"
export NCPS_TEST_S3_REGION="$MINIO_REGION"
export NCPS_TEST_S3_ACCESS_KEY_ID="$MINIO_TEST_S3_ACCESS_KEY_ID"
export NCPS_TEST_S3_SECRET_ACCESS_KEY="$MINIO_TEST_S3_SECRET_ACCESS_KEY"

echo "✅ MinIO configured for S3 integration tests"
