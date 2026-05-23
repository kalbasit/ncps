## Why

`nix flake check` takes too long to be useful as a tight feedback loop. The bulk of that time is spent in `go test -race ./...`, with `pkg/cache` suspected to be the worst offender. Years of accumulated tests have produced redundant assertions (multiple tests covering the same code path with the same expectations) and duplicated expensive setup (per-test fresh databases, S3 buckets, chunk stores, and zstd pools) that could be amortized across subtests. The net effect: long CI wall time, slow local validation, and discouragement from running the full suite before pushing.

## What Changes

- **Profile first**: Run the full suite with `-json` timing across all packages (unit + integration with deps running) and produce a ranked list of slow tests and slow packages. No code changes before measurement.
- **Dedupe**: Identify tests that assert the same behavior on the same code path and remove the redundant copies, keeping the test with the best coverage (most edge cases, clearest name, parallel-safe).
- **Consolidate**: Fold scattered single-case tests covering the same function into table-driven tests where doing so does not lose assertions.
- **Share expensive setup**: Within a single `Test*` function, hoist DB/storage/CDC/zstd fixtures out of subtests where the subtests do not mutate shared state. Keep `t.Parallel()` correctness intact (use per-subtest sub-keys, isolated rows, etc.).
- **Restructure slow integration tests**: Where an integration test covers both wiring AND business logic, split: keep a thin wiring smoke test against the real backend, move business-logic assertions to unit tests with fakes/mocks of the storage interfaces.
- **Coverage parity gate**: Before any removal, capture `go test -coverprofile` for affected packages. After changes, line coverage MUST NOT decrease for the touched packages. Branch coverage MUST NOT decrease by more than 1 percentage point per package.
- **Scope**: All packages with measured slow tests. Integration tests (S3/Postgres/MySQL/Redis) are in scope.

## Capabilities

### New Capabilities

- `test-suite-efficiency`: Codifies rules for the Go test suite — no two tests SHALL assert the same behavior on the same code path; expensive fixtures SHALL be shared across subtests when safe; coverage SHALL NOT regress when tests are removed; the suite SHALL have a documented profiling workflow to find slow tests.

### Modified Capabilities

- `short-test-mode`: After dedup/restructure, the set of tests gated by `testing.Short()` may shift. This change updates the spec to describe how the gated set is re-derived from a fresh profile after the cleanup.

## Impact

- **Code**: `pkg/cache/*_test.go` (primary target), plus any other packages flagged by profiling — likely `pkg/server`, `pkg/storage/{local,s3}`, `pkg/database/*`, `pkg/ncps`.
- **CI**: Lower wall time for `nix flake check`; same coverage signal.
- **Risk**: A truly-unique assertion deleted by mistake could mask a regression. Mitigated by the coverage parity gate and by requiring a written justification per removed test in `tasks.md`.

## Non-goals

- Adding new test coverage. This is a reduction/restructuring effort only.
- Changing production code under test. If a test fails for a real reason during the work, that fix is out of scope and tracked separately.
- Changing the test runner, race-detector usage, or `nix flake check` structure.
- Removing integration tests in favor of mocks wholesale. Wiring smoke tests against real backends stay.
