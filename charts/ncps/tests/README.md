# Helm Chart Tests

This directory contains tests for the ncps Helm chart.

## Test Types

### 1. Validation Tests (Shell Script)

Located in `validation/`, these tests use `helm template` to verify that the chart's validation logic works correctly.

**Run validation tests:**
```bash
./validation/test-validation.sh
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

### 2. Helm Unittest Tests

Located in `validation_test.yaml`, these are proper unit tests using the [helm-unittest](https://github.com/helm-unittest/helm-unittest) plugin.

**Install helm-unittest:**
```bash
helm plugin install https://github.com/helm-unittest/helm-unittest
```

**Run unit tests:**
```bash
# From the repository root
helm unittest charts/ncps

# With verbose output
helm unittest -v charts/ncps

# Run specific test file
helm unittest -f 'tests/validation_test.yaml' charts/ncps
```

**Test coverage:**
The unit tests cover the same scenarios as the shell script tests but use the proper helm-unittest framework for better integration with CI/CD pipelines.

## Adding New Tests

### For validation tests (shell script):
1. Create a new YAML file in `validation/` directory:
   - `*-positive.yaml` for tests that should pass
   - `*-negative.yaml` for tests that should fail
2. Add the test case to `test-validation.sh`

### For helm-unittest:
1. Edit `validation_test.yaml`
2. Add a new test case following the existing pattern
3. Use `failedTemplate` assertion for negative tests
4. Use standard assertions (`isKind`, `equal`, etc.) for positive tests

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
    - name: Install helm-unittest plugin
      run: helm plugin install https://github.com/helm-unittest/helm-unittest
    - name: Run Helm validation tests
      run: ./charts/ncps/tests/run-tests.sh
```

The `run-tests.sh` script:
- Automatically detects and runs helm-unittest tests if the plugin is installed
- Runs all validation tests (positive and negative cases)
- Exits with code 0 on success and 1 on failure
- Can be run from anywhere in the repository

**Manual CI testing:**
You can run the same tests locally that CI runs:
```bash
# Install helm-unittest plugin (optional, tests run without it too)
helm plugin install https://github.com/helm-unittest/helm-unittest

# Run all tests
./charts/ncps/tests/run-tests.sh
```
