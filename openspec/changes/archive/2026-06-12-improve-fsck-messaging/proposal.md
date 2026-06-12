## Why

`ncps fsck` runs a long, multi-phase scan but its output is hard to follow. It
jumps straight into "phase 1: collecting suspects" with no preview of what work
is coming, the eleven phase-1 sub-steps carry cryptic labels (`phase 1c`,
`phase 1g`) that don't say what is being checked, and progress reporting is
inconsistent: phase 1 logs `suspects`/`skipped`, phase 2 logs `remaining`, and
phase 3 (repair) emits no progress at all. Operators running fsck against large
production caches cannot tell which phase is active, how far along it is, or how
long the whole run will take.

## What Changes

- Print an up-front **run plan**: a summary listing every phase (and the CDC /
  `--verify-content` sub-checks that will actually run given the current flags)
  before any work starts, so the operator knows the full scope.
- Give every phase a **human-readable name** describing what it checks (e.g.
  "scanning storage for orphaned NAR files" instead of "phase 1d"), while keeping
  a stable phase identifier for log correlation.
- **Unify progress reporting** across all phases through one helper so every
  phase emits the same fields in the same format: phase name, items
  checked / total, percent, rate (items/s), and elapsed — including phase 3
  (repair), which currently reports nothing.
- Emit a consistent **phase start** and **phase done** line (with the issue/
  item count) for each phase and sub-phase, replacing the current mix of
  start-only and end-only messages.

This is presentation-only: detection logic, repair behavior, exit codes, and the
final summary-table counts are unchanged.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `fsck`: add requirements for an up-front phase run plan, human-readable phase
  naming, and uniform progress reporting (including the repair phase). No change
  to detection, repair, or exit-code behavior.

## Impact

- Code: `pkg/ncps/fsck.go` (phase logging, `startProgressTicker`, `logProgress`,
  the phase-1/2/3 drivers, and a new run-plan printer). Possible new tests under
  `pkg/ncps/fsck_*_test.go` for the run-plan output and progress-field shape.
- No database, migration, API, storage-format, or dependency changes.
- I/O / latency / memory: negligible. Output is logging only; no extra scans,
  network calls, or buffering. The run plan is computed from already-known flag
  state before phase 1 begins.

## Non-goals

- No change to what fsck detects or repairs, to the summary table layout, or to
  exit codes.
- No switch to a TUI/progress-bar library; output stays line-based zerolog so it
  remains usable in non-TTY/CI logs.
- No change to the 30-second progress-ticker cadence or to the `--verified-since`
  skip semantics.
