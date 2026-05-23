echo "🛑 Stopping Garage..."
if [ -n "${GARAGE_PID:-}" ]; then
  kill $GARAGE_PID 2>/dev/null || true
  # Wait for Garage to fully shut down
  for i in {1..30}; do
    if ! kill -0 $GARAGE_PID 2>/dev/null; then
      break
    fi
    sleep 0.5
  done

  # If it's still alive, force kill it
  if kill -0 $GARAGE_PID 2>/dev/null; then
    echo "Garage did not shut down gracefully, force killing..."
    kill -9 $GARAGE_PID 2>/dev/null || true
    sleep 1 # Give a moment for the OS to clean up after SIGKILL
  fi
fi
sleep 1
rm -rf "${GARAGE_DATA_DIR:-}" "${GARAGE_META_DIR:-}" 2>/dev/null || true
echo "✅ Garage stopped and cleaned up"
