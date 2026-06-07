## ADDED Requirements

### Requirement: Local CDC lifecycle e2e driver

The project SHALL provide a local end-to-end test (a sibling to `dev-scripts/test-migration-e2e.py`) that brings up a single ncps instance plus its backing services via the existing `task test:deps` / `nix run .#deps` harness and drives the full `non-CDC → CDC → drain → non-CDC` lifecycle, exercising the service exclusively through its public HTTP surface and the `ncps` CLI. The driver MUST exit non-zero if any phase assertion fails and MUST tear down the services and ncps process it started, including on failure.

#### Scenario: Driver runs against the shared dependency harness

- **WHEN** the e2e driver is invoked (e.g. via a `task` target) with backing services already started by `task test:deps:start`
- **THEN** it connects to those services, runs every lifecycle phase in order, and reports per-phase pass/fail with an overall non-zero exit on any failure

#### Scenario: Driver cleans up on failure

- **WHEN** any phase assertion fails mid-run
- **THEN** the driver stops the ncps process it started and leaves no orphaned ncps process, while preserving captured logs/artifacts for diagnosis

### Requirement: CDC-off baseline phase

The driver SHALL first run with CDC disabled and verify that a NAR can be pushed and then served, establishing a whole-file baseline.

#### Scenario: Whole-file push and serve with CDC off

- **WHEN** CDC is disabled and a store path is pushed to ncps, then fetched back
- **THEN** the served NAR is byte-identical to what was pushed, its narinfo resolves, and the database records the nar_file as a whole file (zero chunks)

### Requirement: CDC-on chunking and narinfo normalization phase

With CDC enabled (both eager and lazy chunking paths), the driver SHALL verify that newly stored NARs are chunked and that narinfo is normalized at serve time.

#### Scenario: Eager chunking stores chunk sequences

- **WHEN** CDC is enabled and a new store path is pushed
- **THEN** the database records the nar_file with a non-zero chunk count and the assembled chunk stream serves back byte-identically to the source NAR

#### Scenario: Lazy chunking on first read

- **WHEN** CDC is enabled and a whole-file NAR from the baseline phase is read
- **THEN** the lazy path produces chunks for it (per the configured eager/lazy mode) and the served bytes remain identical

#### Scenario: Narinfo normalized at serve

- **WHEN** a chunked NAR's narinfo is served
- **THEN** the narinfo fields (e.g. URL/compression) are normalized consistently regardless of the underlying chunked storage representation

### Requirement: CDC-disable drain phase

When CDC is disabled while chunked NARs remain, the driver SHALL verify that drain mode is active and that `ncps migrate-chunks-to-nar` drains all chunked NARs back to whole files.

#### Scenario: Drain mode active with chunks remaining

- **WHEN** CDC is disabled while chunked NARs still exist in the database
- **THEN** the running instance serves those NARs from chunks (drain mode active) and `migrate-chunks-to-nar --dry-run` reports the remaining chunked NARs

#### Scenario: migrate-chunks-to-nar drains all chunks

- **WHEN** `ncps migrate-chunks-to-nar` is run to completion
- **THEN** every previously chunked NAR is rewritten as a whole file, the served bytes remain identical, and the database reports zero NARs with chunks

### Requirement: Restart drain auto-completion phase

After draining, the driver SHALL restart ncps and verify that `initCDCDrainMode` auto-completes: the stored CDC config is cleared and no chunk store is initialized.

#### Scenario: initCDCDrainMode clears stored config on boot

- **WHEN** ncps is restarted after all chunked NARs have been drained
- **THEN** boot logs/state show drain completion, the stored CDC config keys are removed from the database, and no chunk store backend is created

### Requirement: Cross-cutting lifecycle invariants

Across all phases, the driver SHALL assert the cross-cutting invariants that previously regressed: upload/reference presence and fsck repair-not-delete.

#### Scenario: Upload reference presence holds

- **WHEN** a closure is pushed via the upload path during any phase
- **THEN** HEAD/GET presence for every NAR referenced by an uploaded narinfo agrees with actual stored bytes, so `nix copy` does not abort with a missing-reference error

#### Scenario: fsck --repair preserves referenced NARs

- **WHEN** `ncps fsck --repair` is run against the post-lifecycle store (which contains orphaned nar_file rows left by repeated CDC on/off transitions)
- **THEN** fsck may reclaim the genuinely orphaned rows, but every NAR still referenced by a narinfo continues to serve afterwards (no data loss for referenced paths)

<!-- Note: the precise "repair a broken narinfo<->nar_file link instead of deleting
     the record" behavior requires constructing a broken link and is covered by the
     fsck unit tests; the e2e asserts the system-level no-data-loss invariant. -->
