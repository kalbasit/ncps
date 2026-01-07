echo "🛑 Stopping PostgreSQL..."
if [ -n "$POSTGRES_PID" ]; then
  kill $POSTGRES_PID 2>/dev/null || true
  # Wait for PostgreSQL to fully shut down
  for i in {1..30}; do
    if ! kill -0 $POSTGRES_PID 2>/dev/null; then
      break
    fi
    sleep 0.5
  done

  # If it's still alive, force kill it
  if kill -0 $POSTGRES_PID 2>/dev/null; then
    echo "PostgreSQL did not shut down gracefully, force killing..."
    kill -9 $POSTGRES_PID 2>/dev/null || true
    sleep 1 # Give a moment for the OS to clean up after SIGKILL
  fi
fi
sleep 1
rm -rf "$POSTGRES_DATA_DIR" 2>/dev/null || true
echo "✅ PostgreSQL stopped and cleaned up"
