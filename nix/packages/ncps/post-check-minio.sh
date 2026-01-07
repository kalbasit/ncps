echo "🛑 Stopping MinIO..."
if [ -n "$MINIO_PID" ]; then
  kill $MINIO_PID 2>/dev/null || true
  # Wait for MinIO to fully shut down
  for i in {1..30}; do
    if ! kill -0 $MINIO_PID 2>/dev/null; then
      break
    fi
    sleep 0.5
  done

  # If it's still alive, force kill it
  if kill -0 $MINIO_PID 2>/dev/null; then
    echo "MinIO did not shut down gracefully, force killing..."
    kill -9 $MINIO_PID 2>/dev/null || true
    sleep 1 # Give a moment for the OS to clean up after SIGKILL
  fi
fi
sleep 1
rm -rf "$MINIO_DATA_DIR" 2>/dev/null || true
echo "✅ MinIO stopped and cleaned up"
