# Helm Chart Tests

This directory contains tests for the ncps Helm chart.

## Validation Tests

The tests use `helm template` to verify that the chart's validation logic works correctly. All tests are located in the `validation/` directory.

**Run all tests:**

```bash
# From anywhere in the repository
./charts/ncps/tests/run-tests.sh
```

**Test cases:**

- **Positive tests** (should succeed):

  - HA + Deployment + ReadWriteMany
  - HA + Deployment + existingClaim
  - HA + Deployment + S3
  - HA + Deployment + multiple access modes with ReadWriteMany
  - Single replica + ReadWriteOnce

- **Negative tests** (should fail):

  - HA + Deployment + ReadWriteOnce (without existingClaim)
  - HA + SQLite database
  - HA without Redis

## Adding New Tests

Adding a new validation test is simple - just create a new YAML file! The test runner automatically discovers all tests based on file naming convention.

**Steps:**

1. Create a new YAML values file in the `validation/` directory:

   - `*-positive.yaml` for tests that should **pass** validation
   - `*-negative.yaml` for tests that should **fail** validation

1. Add a descriptive comment as the **first line** of the file:

   ```yaml
   # Your test description here
   mode: deployment
   replicaCount: 3
   # ... rest of your values
   ```

1. Run the tests to verify:

   ```bash
   # Test your specific case
   helm template test ./charts/ncps -f charts/ncps/tests/validation/your-test-positive.yaml

   # Run all tests
   ./charts/ncps/tests/run-tests.sh
   ```

That's it! The test runner will automatically discover and run your new test.

**Example:**

Create `validation/ha-deployment-custom-positive.yaml`:

```yaml
# Positive test: HA + Deployment + custom configuration
mode: deployment
replicaCount: 3
config:
  hostname: "test.example.com"
  storage:
    type: s3
    s3:
      bucket: custom-bucket
      endpoint: https://custom.s3.endpoint.com
  # ... rest of config
```

The test will automatically appear in the test output with the description "Positive test: HA + Deployment + custom configuration".

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
```
