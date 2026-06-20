## 1. Phase descriptor table (single source of truth)

- [x] 1.1 Add an ordered set of fsck phase descriptors in `pkg/ncps/fsck.go`, each with a stable id (e.g. `1d`), a human-readable description, and an activation predicate (`always` / `cdcOnly` / `verifyContentOnly`).
- [x] 1.2 Add a helper that, given CDC mode + `--verify-content` + run mode (dry-run / prompt / repair), returns the active descriptors in execution order.
- [x] 1.3 Write a unit test asserting the active set is correct for each flag combination (no-CDC, CDC, CDC+verify-content) and that order matches execution order.

## 2. Run plan printer

- [x] 2.1 Write a failing test asserting that, for a given flag/mode combination, the run-plan output lists exactly the active phases in order and states the run mode.
- [x] 2.2 Implement a `fmt`-based run-plan printer that consumes the active descriptors and prints before the phase-1 start line; wire it into `fsckCommand()`.
- [x] 2.3 Verify CDC sub-checks and `--verify-content` checks appear/disappear from the plan per flag state, and dry-run vs repair mode is shown.

## 3. Uniform progress reporting

- [x] 3.1 Extend the shared progress path (`logProgress` / `startProgressTicker`) so the phase description is a first-class field on every periodic update.
- [x] 3.2 Write a failing test asserting progress updates across phases 1, 2, and 3 share the same field set (phase, checked, total, percent-when-known, rate).
- [x] 3.3 Refactor phase 1 to emit the uniform fields (fold `suspects`/`skipped` reporting into the per-phase done line rather than the periodic update).
- [x] 3.4 Refactor phase 2 to emit the uniform fields (fold `remaining` into the done line).
- [x] 3.5 Add a `checked`/`total` counter to the repair phase (phase 3), wire it to `startProgressTicker`, and emit start + progress + done lines.

## 4. Consistent per-phase start/done messaging

- [x] 4.1 Replace each phase/sub-phase start log with a description-carrying start line sourced from the descriptor table.
- [x] 4.2 Ensure every phase/sub-phase emits a done line carrying its processed count and issues-found count.
- [x] 4.3 Remove now-redundant ad hoc start-only / end-only log lines.

## 5. Verification

- [x] 5.1 Audit `pkg/ncps/fsck_*_test.go` for assertions on old phase strings; update any that break.
- [x] 5.2 Add/confirm a test proving messaging changes do not alter detection counts or exit code (run fsck against a fixture before/after-equivalent path).
- [x] 5.3 Run `task fmt`, `task lint`, and `task test` and confirm each exits zero.
