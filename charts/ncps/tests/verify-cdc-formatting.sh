#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

echo "Verifying CDC config formatting..."

# Render the chart with CDC enabled and large values
# We specifically use values that are powers of 2 (like 1048576) which are susceptible to this if not handled
if ! OUTPUT=$(helm template ncps "${CHART_DIR}" --show-only templates/configmap.yaml \
  --set config.cdc.enabled=true \
  --set config.cdc.min=65536 \
  --set config.cdc.avg=262144 \
  --set config.cdc.max=1048576 \
  --set config.redis.enabled=true \
  --set config.lock.backend=redis \
  --set config.database.type=postgresql \
  --set migration.mode=job \
  --set config.database.postgresql.host=postgres \
  --set replicaCount=2 2>&1); then
    echo "Helm template failed:"
    echo "$OUTPUT"
    exit 1
fi

# Check for exponential notation (e.g., 1.048576e+06)
if MATCHES=$(echo "$OUTPUT" | grep -E -C 3 "[0-9]\.[0-9]+e\+[0-9]+"); then
  echo "❌ Found exponential notation in output!"
  echo "$MATCHES"
  exit 1
fi

echo "✅ No exponential notation found."
exit 0
