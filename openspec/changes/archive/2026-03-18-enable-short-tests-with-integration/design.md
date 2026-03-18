## Context

Go's `-short` flag exists but is unused in this project. We want to enable fast local test runs during development.

## Goals / Non-Goals

**Goals:**
- Enable running `go test -short -race ./...` for quick local development
- Data-driven: identify slow tests by measuring execution time first
- Gate only the slowest tests, not all integration tests

**Non-Goals:**
- Replace full test suite - this is for quick local development only
- Change test coverage requirements
- Modify production code

## Decisions

1. **Use Go's built-in `-short` flag** with `go test -short -race ./...`
   - Rationale: Standard Go testing convention

2. **Measure first, then gate**
   - Run full test suite with timing to identify slow tests
   - Gate only tests that take >500ms (configurable threshold)
   - Not all integration tests are slow, and some non-integration tests may be slow

3. **Run with race detector enabled**
   - Rationale: Project convention - all tests run with `-race`

## Risks / Trade-offs

- **Risk**: Tests skipped in `-short` mode may miss regressions
  - **Mitigation**: This is documented as a quick local development workflow, not CI replacement

- **Risk**: Threshold of 500ms may be too aggressive or too lenient
  - **Mitigation**: Adjust based on actual test run times
