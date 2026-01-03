# Helm Chart Tests

This directory contains tests for the ncps Helm chart.

## Validation Tests

The tests use `helm template` to verify that the chart's validation logic works correctly. All tests are located in the `validation/` directory.

**Run all tests:**

```bash
# From anywhere in the repository
./charts/ncps/tests/run-tests.sh
```

**Run individual validation script:**

```bash
./charts/ncps/tests/validation/test-validation.sh
```

**Test cases:**

- **Positive tests** (should succeed):

  - HA + Deployment + ReadWriteMany
  - HA + Deployment + existingClaim
  - HA + Deployment + S3
  - Single replica + ReadWriteOnce

- **Negative tests** (should fail):

  - HA + Deployment + ReadWriteOnce (without existingClaim)
  - HA + SQLite database
  - HA without Redis

## Adding New Tests

To add a new validation test:

1. Create a new YAML values file in `validation/` directory:

   - `*-positive.yaml` for tests that should pass validation
   - `*-negative.yaml` for tests that should fail validation

1. Add the test case to `run-tests.sh`:

   - Add to the `tests` array in the appropriate section (positive or negative)
   - Format: `"filename:Description of test"`

1. Test your new test case:

   ```bash
   # Test positive case
   helm template test ./charts/ncps -f charts/ncps/tests/validation/your-test-positive.yaml

   # Test negative case (should fail)
   helm template test ./charts/ncps -f charts/ncps/tests/validation/your-test-negative.yaml
   ```

Example:

```bash
# Add to run-tests.sh in the positive tests array:
tests=(
    "ha-deployment-rwx-positive:HA + Deployment + ReadWriteMany"
    "your-new-test-positive:Your new test description"  # Add here
)
```

## CI/CD Integration

The tests are automatically run in CI via the `helm-tests` job in `.github/workflows/ci.yml`:

```yaml
helm-tests:
  runs-on: ubuntu-24.04
  steps:
    - uses: actions/checkout@v6
    - name: Install Helm
      uses: azure/setup-helm@v4
      with:
        version: 'v3.16.3'
    - name: Run Helm validation tests
      run: ./charts/ncps/tests/run-tests.sh
```

The `run-tests.sh` script:

- Runs all validation tests (positive and negative cases) using `helm template`
- Exits with code 0 on success and 1 on failure
- Can be run from anywhere in the repository
- Provides comprehensive coverage of chart validation logic

**Running tests locally:**

```bash
# Run all tests (same as CI)
./charts/ncps/tests/run-tests.sh

# Or run the validation script directly
./charts/ncps/tests/validation/test-validation.sh
```
