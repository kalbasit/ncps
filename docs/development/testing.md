[Home](../../README.md) > [Documentation](../README.md) > [Development](README.md) > Testing

# Testing Guide

Testing procedures and integration test setup.

## Running Tests

### Basic Tests

```bash
# Run all tests with race detector
go test -race ./...

# Run specific package
go test -race ./pkg/cache/...

# Run specific test
go test -race -run TestCacheFetch ./pkg/cache/...
```

### Integration Tests

Integration tests are disabled by default. Enable using helper commands:

```bash
# Start dependencies
nix run .#deps

# Enable all integration tests
eval "$(enable-integration-tests)"

# Run tests
go test -race ./...

# Disable when done
eval "$(disable-integration-tests)"
```

**Available helpers:**

- `enable-s3-tests` - S3/MinIO tests
- `enable-postgres-tests` - PostgreSQL tests
- `enable-mysql-tests` - MySQL tests
- `enable-redis-tests` - Redis lock tests
- `enable-integration-tests` - All integration tests
- `disable-integration-tests` - Disable all

### CI/CD Testing

In Nix builds and CI, all integration tests run automatically:

```bash
nix flake check
```

This automatically:

1. Starts all dependencies (MinIO, PostgreSQL, MariaDB, Redis)
1. Runs all tests including integration tests
1. Stops all services

## Test Structure

Tests use testify for assertions:

```go
import (
    "testing"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestExample(t *testing.T) {
    result := SomeFunction()
    assert.Equal(t, expected, result)
}
```

### Test Packages

Tests use `_test` package suffix (testpackage linter):

```go
package cache_test  // Not package cache

import (
    "testing"
    "github.com/kalbasit/ncps/pkg/cache"
)
```

### Parallel Tests

Use `t.Parallel()` where possible:

```go
func TestParallel(t *testing.T) {
    t.Parallel()
    // Test code...
}
```

## Linting

```bash
# Run all linters
golangci-lint run

# Auto-fix issues
golangci-lint run --fix

# Check specific file
golangci-lint run path/to/file.go
```

**Key linters:**

- err113 - Error handling
- exhaustive - Switch exhaustiveness
- gosec - Security
- paralleltest - Parallel testing
- testpackage - Test package naming

## Code Formatting

```bash
# Format all code (Go, Nix, SQL, etc.)
nix fmt

# Format SQL specifically
sqlfluff format db/query.*.sql
sqlfluff format db/migrations/
```

## SQL Linting

```bash
# Lint SQL files
sqlfluff lint db/query.*.sql
sqlfluff lint db/migrations/
```

## Coverage

```bash
# Run with coverage
go test -race -coverprofile=coverage.out ./...

# View coverage
go tool cover -html=coverage.out
```

## Related Documentation

- [CONTRIBUTING.md](../../CONTRIBUTING.md) - Contribution guidelines
- [CLAUDE.md](../../CLAUDE.md) - Development environment
