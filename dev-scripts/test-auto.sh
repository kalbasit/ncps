#!/usr/bin/env bash
set -euo pipefail

DEPS_PID=""
DEPS_LOG=""

cleanup() {
  if [[ -n "$DEPS_PID" ]]; then
    echo "Stopping backing services (PID $DEPS_PID)..." >&2
    kill "$DEPS_PID" 2>/dev/null || true
    wait "$DEPS_PID" 2>/dev/null || true
    [[ -n "$DEPS_LOG" && -f "$DEPS_LOG" ]] && rm -f "$DEPS_LOG"
  fi
}
trap cleanup EXIT

# Returns 0 if all four backend ports are reachable
ports_ready() {
  nc -z localhost 9000 2>/dev/null \
    && nc -z localhost 5432 2>/dev/null \
    && nc -z localhost 3306 2>/dev/null \
    && nc -z localhost 6379 2>/dev/null
}

if ports_ready; then
  echo "Backing services already running — skipping auto-start." >&2
else
  DEPS_LOG=$(mktemp /tmp/ncps-deps-XXXXXX.log)
  echo "Starting backing services (log: $DEPS_LOG)..." >&2
  nix run .#deps >"$DEPS_LOG" 2>&1 &
  DEPS_PID=$!

  echo "Waiting for all services to be ready (up to 60s)..." >&2
  elapsed=0
  until ports_ready; do
    sleep 2
    elapsed=$((elapsed + 2))
    if [[ $elapsed -ge 60 ]]; then
      echo "ERROR: Services did not become ready within 60s." >&2
      echo "Deps log:" >&2
      cat "$DEPS_LOG" >&2
      exit 1
    fi
  done
  echo "All services ready." >&2
fi

eval "$(enable-integration-tests)"
go test -race ./...
