# short-test-mode Specification

## Purpose

This feature enables fast local development by allowing developers to skip slow tests during iterative development. When running `go test -short`, tests that take longer than 500ms are automatically skipped, enabling faster feedback loops without needing to modify test files or remember which tests to exclude.
## Requirements
### Requirement: -short flag skips slow tests
When `go test -short -race ./...` is run, tests SHALL check `testing.Short()` and skip slow subtests regardless of whether they are integration tests or unit tests.

#### Scenario: Running short tests
- **WHEN** user runs `go test -short -race ./...`
- **THEN** slow tests (>500ms) are skipped via `testing.Short()`

#### Scenario: Running without -short (full tests)
- **WHEN** user runs `go test -race ./...`
- **THEN** all tests execute including slow tests

### Requirement: Identify slow tests via profiling before gating
Before adding `-short` guards, the implementation SHALL profile test execution times to identify which tests are slow.

#### Scenario: Profiling test execution
- **WHEN** running full test suite with timing
- **THEN** identify tests that take >500ms to complete
- **AND** these tests are candidates for `testing.Short()` gating

