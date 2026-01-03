#!/usr/bin/env bash
# Convenience script to run all Helm chart validation tests
# Run from anywhere in the repository
#
# Auto-discovers test files from charts/ncps/tests/validation/:
#   *-positive.yaml - Tests that should pass validation
#   *-negative.yaml - Tests that should fail validation
#
# Test descriptions are parsed from the first comment line in each file.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${CHART_DIR}/../.." && pwd)"
VALIDATION_DIR="${SCRIPT_DIR}/validation"

cd "${REPO_ROOT}"

echo "Running Helm chart validation tests..."
echo ""

# Function to extract test description from YAML file
# Returns the first comment line (without the # prefix)
get_test_description() {
    local file=$1
    # Get first line starting with #, remove the # and trim whitespace
    grep -m 1 '^#' "$file" 2>/dev/null | sed 's/^#\s*//' || basename "$file" .yaml
}

# Function to run positive tests
run_positive_tests() {
    local file desc filename
    local count=0

    for file in "${VALIDATION_DIR}"/*-positive.yaml; do
        # Check if glob matched any files
        [ -e "$file" ] || continue
        count=$((count + 1))

        desc=$(get_test_description "$file")
        filename=$(basename "$file")

        if helm template test ./charts/ncps --values "$file" > /dev/null 2>&1; then
            echo "  ✓ ${desc}"
            PASSED=$((PASSED+1))
        else
            echo "  ✗ ${desc}"
            echo "    File: ${filename}"
            FAILED=$((FAILED+1))
        fi
    done

    if [ $count -eq 0 ]; then
        echo "  No positive tests found"
    fi
}

# Function to run negative tests
run_negative_tests() {
    local file desc filename
    local count=0

    for file in "${VALIDATION_DIR}"/*-negative.yaml; do
        # Check if glob matched any files
        [ -e "$file" ] || continue
        count=$((count + 1))

        desc=$(get_test_description "$file")
        filename=$(basename "$file")

        if helm template test ./charts/ncps --values "$file" > /dev/null 2>&1; then
            echo "  ✗ ${desc} (incorrectly passed)"
            echo "    File: ${filename}"
            FAILED=$((FAILED+1))
        else
            echo "  ✓ ${desc} (correctly rejected)"
            PASSED=$((PASSED+1))
        fi
    done

    if [ $count -eq 0 ]; then
        echo "  No negative tests found"
    fi
}

echo "=== Validation Tests ==="
echo ""

PASSED=0
FAILED=0

echo "Positive Tests (should pass):"
run_positive_tests

echo ""
echo "Negative Tests (should fail):"
run_negative_tests

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
