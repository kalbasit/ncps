## Context

`ncps fsck` (in `pkg/ncps/fsck.go`) runs three top-level phases — collect
suspects, re-verify suspects, repair — driven by `fsckCommand()`. Phase 1
internally fans out into ~11 sub-checks (1a–1j) covering narinfos without
nar_files, orphaned nar_files, storage-vs-DB walks, orphaned chunks, chunk
issues, CDC size mismatch, orphaned chunk files, and (under `--verify-content`)
chunk and assembled-NAR hash verification.

Current messaging has three problems:

1. **No preview.** The run jumps into `phase 1: collecting suspects` with no
   indication of how many phases/sub-checks will run or whether repair will
   happen. Whether a sub-check runs depends on CDC mode and `--verify-content`,
   so the operator can't predict scope.
2. **Cryptic labels.** Sub-checks are logged as `phase 1c`, `phase 1g`, etc.
   The descriptive text is inconsistent — some phases log a start line, some
   only an end count, some both.
3. **Inconsistent progress.** Three different progress shapes: phase 1 logs
   `suspects` + `skipped`, phase 2 logs `remaining`, phase 3 logs nothing.
   `logProgress` (fsck.go:2563) already centralizes `checked`/`total`/`percent`/
   `rate`, and `startProgressTicker` (fsck.go:2540) runs a 30s ticker, but each
   phase wires them up ad hoc with divergent extra fields and no shared phase
   label.

Logging is zerolog throughout (`zerolog.Ctx(ctx)`); the final summary table is
printed via `fmt` in `printFsckSummary` (fsck.go:1252). This change is
presentation-only — no detection, repair, schema, or exit-code changes.

## Goals / Non-Goals

**Goals:**

- Print a run plan up front that enumerates the phases/sub-checks that will
  actually run given CDC mode and `--verify-content`, and states the run mode
  (dry-run / prompt / repair).
- Attach a stable phase id **and** a human-readable description to every phase
  and sub-phase, and emit a consistent start + done (with count) line for each.
- Route all progress — phases 1, 2, and the previously-silent phase 3 — through
  one shared helper so fields and formatting are identical everywhere.
- Keep output line-based so it stays readable in CI / non-TTY logs.

**Non-Goals:**

- No change to detection logic, repair behavior, the summary-table layout, or
  exit codes.
- No TUI / progress-bar dependency.
- No change to the 30s ticker cadence or `--verified-since` skip semantics.
- No new flags.

## Decisions

### Decision 1: A single phase descriptor table drives both the plan and the labels

Introduce a small ordered set of phase descriptors, each with a stable id (e.g.
`1d`), a human-readable description ("scanning storage for orphaned NAR files"),
and predicates for when it is active (`always`, `cdcOnly`, `verifyContentOnly`).
The run-plan printer and the per-phase start/done lines both read from this one
table, so the plan can never drift from what actually runs.

- **Why over the status quo (inline strings):** today the label text lives
  inline at each call site, which is exactly why the plan is missing and labels
  are inconsistent. One source of truth fixes both at once.
- **Alternative considered — compute the plan by dry-running detection:**
  rejected; it would duplicate work and risk side effects. The active set is a
  pure function of CDC mode + `--verify-content`, both known before phase 1.

### Decision 2: One progress helper carries the phase label; phase 3 adopts it

Extend the shared progress path so the phase description is a first-class field
emitted by every update, replacing the per-phase divergent fields. Phase-1
`suspects`/`skipped` and phase-2 `remaining` become uniform `checked`/`total`
(plus the existing `percent`/`rate` from `logProgress`); any genuinely
phase-specific counter is added consistently rather than ad hoc. Phase 3 gains a
`checked`/`total` counter over the repair work-list and is wired to
`startProgressTicker` like the others, plus a start and done line.

- **Why:** uniform fields are the core ask ("each phase should have the same
  progress/rate"). Centralizing also means future phases inherit the format for
  free.
- **Trade-off:** phase 2's `remaining` and phase 1's `suspects` were
  semantically useful. We keep equivalent information by reporting issues-found
  in the per-phase done line, so no signal is lost while the periodic update
  stays uniform.

### Decision 3: Run plan and summary use `fmt` (operator-facing), phases use zerolog

The run plan is operator-facing framing, like the existing `printFsckSummary`,
so it prints via `fmt` to stdout for clean unconditional display. Per-phase
start/done/progress lines stay on zerolog so they carry timestamps/levels and
remain greppable and structured in CI logs. This mirrors the existing split
(summary via `fmt`, phase logs via zerolog) rather than introducing a new
convention.

## Risks / Trade-offs

- **[Tests asserting current log strings break]** → Search `fsck_*_test.go` for
  assertions on phase strings; current tests focus on functional outcomes, not
  log text, so exposure is low. Update any string assertions as part of the
  change.
- **[Plan drifts from actual execution if a new sub-check is added later
  without registering it]** → Mitigated by the single descriptor table: a new
  sub-check that isn't added to the table simply has no label, which is an
  obvious omission; document the table as the place to register phases.
- **[Phase 3 progress adds overhead]** → Negligible: it's an atomic counter and
  the same 30s ticker; no extra I/O or scans.
- **[Output volume slightly increases]** (run plan + per-phase done lines) →
  Acceptable and intentional; it is a handful of lines and is the point of the
  change.

## Migration Plan

Pure presentation change shipped in one PR; no DB migration, no config, no
rollout coordination. Rollback is reverting the commit. TDD: add/adjust tests
asserting (a) the run plan lists the active phases for a given flag combination
and (b) progress updates across phases share the same field set, then refactor
the phase drivers to satisfy them.

## Open Questions

- Should the run plan also print an estimated item count per phase where it is
  cheaply known up front (e.g. narinfo / nar_file row counts)? Leaning no for
  the first cut — keep the plan to phase names + mode — but it is a natural
  follow-up if operators want a size preview.
