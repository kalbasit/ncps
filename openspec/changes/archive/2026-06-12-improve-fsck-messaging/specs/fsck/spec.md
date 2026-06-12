## ADDED Requirements

### Requirement: Fsck MUST print an up-front run plan before any scanning begins

Before phase 1 starts, `ncps fsck` SHALL print a run plan that lists, in
execution order, every top-level phase it will run and every sub-check that is
active for the current invocation. The plan SHALL reflect the resolved flag and
mode state: CDC-only sub-checks SHALL appear only when CDC mode is detected, and
the `--verify-content` chunk/NAR hash checks SHALL appear only when
`--verify-content` is set. The plan SHALL also indicate the run mode (dry-run,
report-and-prompt, or `--repair`) so the operator knows whether a repair phase
will run.

#### Scenario: Run plan lists active phases before phase 1

- **WHEN** `ncps fsck` starts
- **THEN** a run plan is printed before the "phase 1" start line
- **AND** it enumerates each phase that will run in execution order

#### Scenario: CDC sub-checks appear in the plan only in CDC mode

- **WHEN** `ncps fsck` runs and CDC mode is detected
- **THEN** the run plan includes the CDC sub-checks (orphaned chunks, chunk
  issues, size mismatch, orphaned chunk files, chunked residue)
- **AND** when CDC mode is NOT detected, the run plan omits every CDC sub-check

#### Scenario: Content-verification checks appear only with the flag

- **WHEN** `ncps fsck --verify-content` runs in CDC mode
- **THEN** the run plan includes the "corrupt chunks" and "NAR hash mismatch"
  checks
- **AND** when `--verify-content` is NOT set, the run plan omits both checks

#### Scenario: Run plan states the run mode

- **WHEN** `ncps fsck --dry-run` runs
- **THEN** the run plan indicates that no repair phase will run
- **AND** when `--repair` is set, the run plan indicates the repair phase will run

### Requirement: Every fsck phase MUST be reported with a human-readable name

Each phase and sub-phase SHALL be announced with a description of what it checks
(for example "scanning storage for orphaned NAR files") rather than only an
opaque identifier such as "phase 1d". A stable phase identifier MAY accompany
the description for log correlation, but the description SHALL always be present.

#### Scenario: Sub-phase start line describes the work

- **WHEN** a phase-1 sub-check begins
- **THEN** its start log line includes a human-readable description of what is
  being checked

#### Scenario: Phase identifier alone is never the only label

- **WHEN** any phase or sub-phase is reported
- **THEN** the message contains a human-readable description, not just a bare
  phase identifier

### Requirement: Fsck progress reporting MUST be uniform across all phases

All phases — including the repair phase — SHALL report progress through a single
shared mechanism that emits the same fields in the same format: the phase
description, items checked, total items (when known), percent complete (when the
total is known), processing rate in items per second, and elapsed time. Each
phase SHALL emit a start line and, on completion, a done line carrying the count
of items processed and issues found for that phase.

#### Scenario: Every phase reports the same progress fields

- **WHEN** a periodic progress update fires during any phase
- **THEN** it reports the phase description, items checked, total (when known),
  percent (when total is known), and rate using the shared format

#### Scenario: Repair phase reports progress

- **WHEN** the repair phase runs against a set of issues
- **THEN** it emits a start line and progress updates using the same shared
  mechanism as the scan phases
- **AND** it emits a done line on completion

#### Scenario: Each phase emits a completion line with its count

- **WHEN** a phase finishes
- **THEN** a done line reports how many items it processed and how many issues it
  found for that phase

### Requirement: Fsck messaging changes MUST NOT alter detection, repair, or exit-code behavior

The run plan, phase naming, and progress-reporting changes SHALL be
presentation-only. The set of issues detected, the repairs performed, the final
summary-table counts, and the process exit codes SHALL be identical to the
behavior before this change.

#### Scenario: Issue counts and exit code are unchanged

- **WHEN** `ncps fsck` runs against a given cache state before and after this
  change
- **THEN** the summary-table issue counts are identical
- **AND** the process exit code is identical

#### Scenario: Repair outcomes are unchanged

- **WHEN** `ncps fsck --repair` runs against a given cache state
- **THEN** the same rows, narinfos, chunk records, and chunk files are repaired
  or deleted as before this change
