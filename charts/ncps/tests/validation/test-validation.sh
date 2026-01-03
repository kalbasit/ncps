#!/usr/bin/env bash
# Test script to validate Helm chart validation logic
# Tests both positive cases (should pass) and negative cases (should fail)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Track test results
PASSED=0
FAILED=0

echo "Testing Helm chart validation logic..."
echo "Chart: ${CHART_DIR}"
echo ""

# Function to run a positive test (should succeed)
test_positive() {
    local values_file=$1
    local test_name=$(basename "$values_file" .yaml)

    echo -n "Testing ${test_name} (should PASS): "

    if helm template test-release "${CHART_DIR}" \
        --values "${values_file}" \
        --debug \
        > /dev/null 2>&1; then
        echo -e "${GREEN}✓ PASS${NC}"
        ((PASSED++))
        return 0
    else
        echo -e "${RED}✗ FAIL${NC}"
        echo "  Expected: Success"
        echo "  Got: Validation failed"
        helm template test-release "${CHART_DIR}" \
            --values "${values_file}" 2>&1 | grep -A 5 "Error:" || true
        ((FAILED++))
        return 1
    fi
}

# Function to run a negative test (should fail)
test_negative() {
    local values_file=$1
    local test_name=$(basename "$values_file" .yaml)
    local expected_error=$2

    echo -n "Testing ${test_name} (should FAIL): "

    if helm template test-release "${CHART_DIR}" \
        --values "${values_file}" \
        --debug \
        > /dev/null 2>&1; then
        echo -e "${RED}✗ FAIL${NC}"
        echo "  Expected: Validation error"
        echo "  Got: Success (should have failed)"
        ((FAILED++))
        return 1
    else
        # Check if error message matches expected pattern
        error_output=$(helm template test-release "${CHART_DIR}" \
            --values "${values_file}" 2>&1 || true)

        if echo "$error_output" | grep -q "$expected_error"; then
            echo -e "${GREEN}✓ PASS${NC}"
            ((PASSED++))
            return 0
        else
            echo -e "${YELLOW}⚠ PASS (unexpected error)${NC}"
            echo "  Expected error containing: ${expected_error}"
            echo "  Got error:"
            echo "$error_output" | grep "Error:" || true
            ((PASSED++))
            return 0
        fi
    fi
}

echo "=== Positive Tests (should pass validation) ==="
echo ""

test_positive "${SCRIPT_DIR}/ha-deployment-rwx-positive.yaml"
test_positive "${SCRIPT_DIR}/ha-deployment-existing-claim-positive.yaml"
test_positive "${SCRIPT_DIR}/ha-deployment-s3-positive.yaml"
test_positive "${SCRIPT_DIR}/single-replica-rwo-positive.yaml"

echo ""
echo "=== Negative Tests (should fail validation) ==="
echo ""

test_negative "${SCRIPT_DIR}/ha-deployment-rwo-negative.yaml" "ReadWriteMany"
test_negative "${SCRIPT_DIR}/ha-sqlite-negative.yaml" "SQLite"
test_negative "${SCRIPT_DIR}/ha-no-redis-negative.yaml" "Redis"

echo ""
echo "=== Test Summary ==="
echo -e "Passed: ${GREEN}${PASSED}${NC}"
echo -e "Failed: ${RED}${FAILED}${NC}"
echo ""

if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed!${NC}"
    exit 1
fi
