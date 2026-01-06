echo "🛑 Stopping Redis..."
if [ -n "$REDIS_PID" ]; then
  kill $REDIS_PID 2>/dev/null || true
  # Wait for Redis to fully shut down
  for i in {1..30}; do
    if ! kill -0 $REDIS_PID 2>/dev/null; then
      break
    fi
    sleep 0.5
  done

  # If it's still alive, force kill it
  if kill -0 $REDIS_PID 2>/dev/null; then
    echo "Redis did not shut down gracefully, force killing..."
    kill -9 $REDIS_PID 2>/dev/null || true
    sleep 1 # Give a moment for the OS to clean up after SIGKILL
  fi
fi
sleep 1
rm -rf "$REDIS_DATA_DIR" 2>/dev/null || true
echo "✅ Redis stopped and cleaned up"
