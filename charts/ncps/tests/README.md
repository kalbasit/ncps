# Helm Chart Tests

This directory contains comprehensive unit tests for the ncps Helm chart using [helm-unittest](https://github.com/helm-unittest/helm-unittest).

## Prerequisites

Install the helm-unittest plugin:

```bash
helm plugin install https://github.com/helm-unittest/helm-unittest
```

## Running Tests

**Run all tests:**

```bash
# From repository root
helm unittest charts/ncps

# From chart directory
cd charts/ncps
helm unittest .
```

**Run specific test file:**

```bash
helm unittest charts/ncps -f tests/validation_test.yaml
```

**Run tests with verbose output:**

```bash
helm unittest charts/ncps -3
```

**Run tests in Nix (recommended for CI):**

```bash
nix flake check
```

## Test Structure

All test files are located in `charts/ncps/tests/` and must end with `_test.yaml`.

### Test Files

- **`configmap_test.yaml`** - ConfigMap rendering and configuration tests

  - CDC value formatting (ensures integers, not exponential notation)
  - Analytics, logging, and observability settings
  - Storage backend configuration
  - Database pool settings
  - Lock backend configuration
  - All config.\* values

- **`secret_test.yaml`** - Secret generation and database URL tests

  - PostgreSQL URL generation (with/without password, with special characters)
  - MySQL URL generation (with/without password, with special characters)
  - S3 credentials
  - Redis credentials
  - Netrc file handling
  - ExistingSecret scenarios

- **`validation_test.yaml`** - Chart validation logic tests

  - HA mode requirements (CDC, distributed locks, database compatibility)
  - Storage backend validation
  - Database configuration validation
  - LRU, Redis, and upstream cache validation
  - Migration mode validation for HA
  - All fail scenarios from `_helpers.tpl`

- **`deployment_test.yaml`** - Deployment and StatefulSet rendering tests

  - Mode-based resource creation
  - Replica count configuration
  - Image configuration
  - Security contexts
  - Resource limits and requests
  - Probes configuration
  - Service account, node selector, tolerations, affinity
  - Extra environment variables, volumes, and init containers

## Adding New Tests

Create or edit test files in `charts/ncps/tests/`. Test files must end with `_test.yaml`.

**Example test:**

```yaml
suite: my test suite
templates:
  - configmap.yaml
tests:
  - it: should do something
    set:
      config.hostname: test.example.com
      # ... other values
    asserts:
      - isKind:
          of: ConfigMap
      - equal:
          path: metadata.name
          value: RELEASE-NAME-ncps
```

## Test Coverage

The test suite provides comprehensive coverage of:

✅ All values from `values.yaml`
✅ All validation logic from `_helpers.tpl`
✅ Database URL generation for all database types
✅ Type validation (e.g., CDC values as integers, not exponential)
✅ HA mode requirements and constraints
✅ Storage backend configuration
✅ Secret generation and credential handling
✅ Migration modes and database safety
✅ Security contexts and resource configuration

## Key Test Scenarios

### CDC Formatting

Tests ensure CDC values (min, avg, max) are rendered as integers, not exponential notation:

```yaml
# Should render as: min: 65536
# NOT as: min: 6.5536e+04
```

### Database URL Generation

Tests verify correct URL format for all database types:

- **PostgreSQL with password**: `postgresql://user:pass@host:port/db?sslmode=disable`
- **PostgreSQL without password**: `postgresql://user@host:port/db?sslmode=disable`
- **MySQL with password**: `mysql://user:pass@host:port/db`
- **MySQL without password**: `mysql://user@host:port/db`
- **SQLite**: `sqlite:/path/to/db.sqlite` (no secret created)

Special characters in passwords are URL-encoded (e.g., `p@ss!` → `p%40ss%21`).

### Validation Tests

Tests cover all validation rules:

- ✗ HA without distributed locking → **fails**
- ✗ HA with SQLite → **fails**
- ✗ HA without CDC (no bypass) → **fails**
- ✓ HA with Redis + CDC + PostgreSQL → **passes**
- ✗ PostgreSQL with both password and existingSecret → **fails**
- ✗ S3 without bucket/endpoint/credentials → **fails**

## CI Integration

Tests are automatically run in CI via `nix flake check`, which includes the `helm-unittest-check` in the checks output.

The CI workflow (\`\`.github/workflows/ci.yml`) runs the `build`job, which internally calls`nix flake check\`, ensuring all Helm tests pass before merging.

## Best Practices

1. **Use descriptive test names**: `it: should generate PostgreSQL database-url with password`
1. **Test both positive and negative cases**: Verify expected failures with `failedTemplate`
1. **Base64 encode secret values**: Use `equal` with base64-encoded expected values
1. **Test special characters**: Ensure URL encoding works correctly
1. **Keep tests focused**: One test per scenario
1. **Document expected values**: Add comments showing decoded base64 or expected output

## Troubleshooting

**Plugin not installed:**

```bash
helm plugin install https://github.com/helm-unittest/helm-unittest
```

**Tests failing:**

```bash
# Run with verbose output
helm unittest charts/ncps -3

# Run specific test file
helm unittest charts/ncps -f tests/validation_test.yaml
```

**Nix checks failing:**

```bash
# Run locally
nix flake check

# Check specific output
nix build .#checks.<your-system>.helm-unittest-check
```

## Documentation

- [helm-unittest GitHub](https://github.com/helm-unittest/helm-unittest)
- [helm-unittest Documentation](https://github.com/helm-unittest/helm-unittest/blob/main/DOCUMENT.md)
- [Assertion Types](https://github.com/helm-unittest/helm-unittest/blob/main/DOCUMENT.md#assertion-types)
