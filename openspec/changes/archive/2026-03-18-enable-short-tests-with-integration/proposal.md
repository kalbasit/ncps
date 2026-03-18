## Why

Currently, there's no way to run a quick test suite for local development. The `-short` flag in Go's testing package exists but is unused. Running the full test suite takes too long for iterative development.

## What Changes

- Add `testing.Short()` guards to tests that take >500ms to run
- Start by running all tests and measuring execution time to identify slow tests
- Gate only the slowest tests with `-short`, not all integration tests

## Capabilities

### New Capabilities

- **short-test-mode**: A standardized way to run the fastest set of tests via `go test -short -race ./...`

### Modified Capabilities

- (none - this is a new capability)

## Impact

- Test files: Add `testing.Short()` guards to slow tests identified via profiling
- No changes to production code or APIs
