#!/usr/bin/env bash
# Convenience script to run all Helm chart tests
# Run from anywhere in the repository

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${CHART_DIR}/../.." && pwd)"

cd "${REPO_ROOT}"

echo "Running Helm chart tests..."
echo ""

# Check if helm-unittest is installed
if helm plugin list 2>/dev/null | grep -q unittest; then
    echo "=== Running helm-unittest tests ==="
    helm unittest charts/ncps
    echo ""
fi

# Run validation tests
echo "=== Running validation tests ==="
echo ""

PASSED=0
FAILED=0

echo "Positive Tests (should pass):"
tests=(
    "ha-deployment-s3-positive:HA + Deployment + S3"
    "single-replica-rwo-positive:Single replica + RWO"
)

for test in "${tests[@]}"; do
    IFS=':' read -r file desc <<< "$test"
    if helm template test ./charts/ncps --values "charts/ncps/tests/validation/${file}.yaml" > /dev/null 2>&1; then
        echo "  ✓ ${desc}"
        PASSED=$((PASSED+1))
    else
        echo "  ✗ ${desc}"
        FAILED=$((FAILED+1))
    fi
done

echo ""
echo "Negative Tests (should fail):"
tests=(
    "ha-deployment-rwo-negative:HA + Deployment + RWO (no existingClaim)"
    "ha-sqlite-negative:HA + SQLite database"
    "ha-no-redis-negative:HA without Redis"
)

for test in "${tests[@]}"; do
    IFS=':' read -r file desc <<< "$test"
    if helm template test ./charts/ncps --values "charts/ncps/tests/validation/${file}.yaml" > /dev/null 2>&1; then
        echo "  ✗ ${desc} (incorrectly passed)"
        FAILED=$((FAILED+1))
    else
        echo "  ✓ ${desc} (correctly rejected)"
        PASSED=$((PASSED+1))
    fi
done

echo ""
echo "=== Test Summary ==="
echo "Passed: ${PASSED}"
echo "Failed: ${FAILED}"

if [ $FAILED -eq 0 ]; then
    echo ""
    echo "✓ All tests passed!"
    exit 0
else
    echo ""
    echo "✗ Some tests failed!"
    exit 1
fi
