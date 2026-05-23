## ADDED Requirements

### Requirement: Profiling workflow for slow tests

The project SHALL provide a reproducible workflow that ranks Go test execution time by package and by individual test, runnable both locally (within the Nix dev shell) and inside `nix flake check`. The workflow MUST capture timings for the full suite including integration tests when integration env vars are set.

#### Scenario: Profiling the full suite locally

- **WHEN** a developer has integration env vars enabled (`eval "$(enable-integration-tests)"`) and runs the profiling workflow
- **THEN** the workflow produces a ranked list of packages by total wall time
- **AND** produces a ranked list of individual tests (parent and subtests) with `Elapsed > 500ms`
- **AND** writes the ranking to a deterministic output file under `dev-scripts/` or `openspec/changes/<change>/`

#### Scenario: Profiling without integration env vars

- **WHEN** a developer runs the profiling workflow without integration env vars set
- **THEN** the workflow still runs the unit-test portion of the suite and produces the same ranked outputs
- **AND** integration tests show as skipped, not as failures

### Requirement: No two tests assert the same behavior on the same code path

The Go test suite MUST NOT contain two tests (or subtests) where the second test exercises a code-path coverage strict-subset of another test AND asserts a strict subset of that test's properties. Such a pair indicates redundancy and one MUST be removed.

#### Scenario: Reviewer evaluates a candidate test for removal

- **WHEN** a reviewer considers removing a test
- **THEN** the reviewer MUST verify all four conditions hold for the test
  - its per-test coverage profile is a subset of another surviving test's profile
  - every property it asserts is asserted by a surviving test with equal or stronger specificity
  - it exercises no unique input boundary (empty input, max-size input, unicode, concurrent access, backend-specific quirk)
  - it exercises no unique goroutine interleaving that no surviving test exercises
- **AND** if any of the four fails, the test MUST be kept

#### Scenario: Adding a new test that duplicates an existing one

- **WHEN** a new test is proposed for the suite
- **AND** an existing test already asserts the same behavior on the same code path
- **THEN** the new test SHALL NOT be added; the existing test is extended instead

### Requirement: Coverage parity gate

When tests are removed or restructured, line coverage per affected package MUST NOT decrease from the pre-change baseline. Branch coverage per affected package MUST NOT decrease by more than 1 percentage point. The check MUST be applied per batch of changes, not only at the end.

#### Scenario: Removing a test passes the coverage gate

- **WHEN** a batch of test removals is proposed for package `pkg/X`
- **AND** `go test -coverpkg=./... -coverprofile=cover-after.out -race ./pkg/X/...` is run on the post-change tree
- **THEN** line coverage of `cover-after.out` is greater than or equal to line coverage of the pre-change `cover-before.out`
- **AND** branch coverage of `cover-after.out` is greater than or equal to `cover-before.out` minus 1 percentage point

#### Scenario: A batch fails the coverage gate

- **WHEN** a batch of test removals causes line coverage to drop or branch coverage to drop by more than 1 percentage point for any affected package
- **THEN** the batch MUST be reverted in full
- **AND** the offending removal MUST be re-evaluated against the four-gate rule before any further attempt

### Requirement: Shared fixtures within a TestXxx function

Within a single Go `TestXxx` function, expensive setup that is not mutated by subtests SHALL be hoisted out of the subtests and constructed once in the parent test. Per-subtest unique identifiers (keys, paths, table names) MUST be derived from `t.Name()` or a counter so that subtests using `t.Parallel()` do not collide.

#### Scenario: Hoisting setup out of subtests

- **WHEN** a `TestXxx` function has multiple subtests that each construct an identical fixture (e.g., open a SQLite connection, run migrations, create a MinIO bucket)
- **AND** no subtest mutates the fixture in a way that would affect other subtests
- **THEN** the fixture SHALL be constructed once in the parent test
- **AND** each subtest MUST receive the fixture as a parameter or via a closure

#### Scenario: Parallel subtests share a database

- **WHEN** subtests share a single database connection hoisted by the parent test
- **AND** subtests call `t.Parallel()`
- **THEN** each subtest MUST use a per-subtest keyspace (unique rows, unique table prefixes, or unique transaction scopes) so parallel subtests cannot observe each other's writes

### Requirement: Package-level shared fixtures are read-only

Package-level shared state (set up in `TestMain` or `init`) SHALL be limited to read-only data such as pre-parsed NAR fixtures, pre-computed test vectors, or constants. Mutable shared state (open DB connections, open S3 sessions, open Redis connections) MUST NOT be hoisted to package level.

#### Scenario: Read-only fixture at package level

- **WHEN** a NAR file or fixture byte buffer is needed by multiple `TestXxx` functions in a package
- **THEN** it MAY be parsed once at package level and read by all tests
- **AND** no test mutates the shared buffer

#### Scenario: Forbidden mutable package-level state

- **WHEN** a developer proposes a package-level `*sql.DB`, S3 client session, or Redis client shared across `TestXxx` functions
- **THEN** the proposal MUST be rejected unless an existing helper already provides this with documented thread-safety guarantees
- **AND** the default SHALL be a per-test or per-`TestXxx` connection

### Requirement: Integration test split — wiring smoke vs business logic

For each external backend exercised by integration tests (Postgres, MySQL, S3/MinIO, Redis), the suite SHALL contain at least one wiring smoke test that connects to the real backend and performs a minimal roundtrip. Business-logic assertions (error branches, retries, edge cases) MAY be tested against in-memory fakes implementing the storage/database interfaces, provided a fake already exists or is trivial to write.

#### Scenario: Backend wiring smoke test exists

- **WHEN** the suite finishes running with all integration env vars enabled
- **THEN** for each of Postgres, MySQL, S3/MinIO, and Redis there is at least one test that connected to the real backend and exercised a connect → write → read → close cycle

#### Scenario: Backend-specific behavior stays as integration test

- **WHEN** a test asserts behavior that depends on backend-specific semantics (e.g., Postgres upsert with `ON CONFLICT`, S3 multipart upload, MySQL collation)
- **THEN** the test MUST remain an integration test against the real backend
- **AND** MUST NOT be moved to a fake

### Requirement: Per-removal justification in change tasks

Every test removed by a `less-tests`-style cleanup MUST have a written one-line justification in the change's `tasks.md` naming the surviving test that subsumes it. This requirement applies whenever the test suite is being reduced; it does not apply to ordinary refactors that incidentally move a test.

#### Scenario: Test removed with justification

- **WHEN** a test is deleted under a test-suite-efficiency change
- **THEN** `tasks.md` contains a line referencing the deleted test's name and the surviving test that covers its assertions

#### Scenario: Test removed without justification

- **WHEN** a deletion appears in the change's diff without a corresponding line in `tasks.md`
- **THEN** the change MUST NOT be marked complete until the justification is added
