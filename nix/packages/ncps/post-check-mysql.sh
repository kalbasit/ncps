echo "🛑 Stopping MariaDB..."
if [ -n "$MYSQL_PID" ]; then
  kill $MYSQL_PID 2>/dev/null || true
  # Wait for MariaDB to fully shut down
  for i in {1..30}; do
    if ! kill -0 $MYSQL_PID 2>/dev/null; then
      break
    fi
    sleep 0.5
  done

  # If it's still alive, force kill it
  if kill -0 $MYSQL_PID 2>/dev/null; then
    echo "MariaDB did not shut down gracefully, force killing..."
    kill -9 $MYSQL_PID 2>/dev/null || true
    sleep 1 # Give a moment for the OS to clean up after SIGKILL
  fi
fi
# Give it an extra moment to release file handles
sleep 1
rm -rf "$MYSQL_DATA_DIR" 2>/dev/null || true
echo "✅ MariaDB stopped and cleaned up"
