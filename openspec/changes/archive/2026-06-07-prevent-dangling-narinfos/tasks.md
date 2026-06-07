## 1. Red — failing tests for the GC narinfo cleanup

- [x] 1.1 In `pkg/cache/recovery_gc_internal_test.go` (or a new `recovery_gc_narinfo_cleanup_internal_test.go`), add a table-driven test for `gcOrSkipBackingLessNarFile`: seed a backing-less `nar_file` (no whole-file, `total_chunks=0`) linked to one narinfo, stub every healthy upstream to report `ExistenceAbsent`, run the GC, and assert BOTH the `nar_file` AND the narinfo are deleted (no narinfo row references the hash; no narinfo left without a `narinfo_nar_files` link). Must fail today.
- [x] 1.2 Add a multi-narinfo case (several store paths sharing the one NAR, all `ExistenceAbsent`): assert all linked narinfos and the `nar_file` are deleted. Must fail today.
- [x] 1.3 Add a guard case: at least one linked narinfo `ExistencePresent` (and a separate case for `ExistenceUnknown`, and a no-healthy-upstreams case): assert NOTHING is deleted (nar_file and all narinfos intact). Should already pass — locks in the safety gate.
- [x] 1.4 Add the existing zero-linked-narinfo case: assert the `nar_file` is GC'd and no narinfo deletion is attempted. Should already pass — regression guard.
- [x] 1.5 Ensure all new tests call `t.Parallel()` (incl. subtests) and use `require`/`assert` per repo conventions; run with the race detector and confirm 1.1–1.2 fail for the right reason.

## 2. Green — implement atomic narinfo deletion in the GC

- [x] 2.1 In `gcOrSkipBackingLessNarFile` (`pkg/cache/cache.go`), in the genuinely-absent branch (after the `narInfoGenuinelyAbsentUpstream` loop passes), delete the linked narinfos collected in `nis` together with the `nar_file` in a single Ent transaction (`withEntTransaction`), relying on the existing `narinfo_nar_files` cascade to clean link rows.
- [x] 2.2 Leave the zero-linked-narinfo branch and the skip (Present/Unknown/no-upstreams) branch unchanged.
- [x] 2.3 Extend the existing `garbage-collected genuinely-absent placeholder nar_file` info log with the count of deleted narinfos for observability (per design Open Question).
- [x] 2.4 Run the test suite with the race detector; confirm 1.1–1.4 pass.

## 3. Verify & finalize

- [x] 3.1 Run `task fmt`, `task lint`, `task test` — all exit 0 (per verify-before-completion rule).
- [x] 3.2 Confirm no Ent schema change crept in and no migration was generated (this change is delete-only on existing rows; `task ent:check` clean if touched).
- [x] 3.3 Re-read the delta spec scenarios against the implementation; confirm each scenario maps to a passing test.
- [x] 3.4 Update `openspec/changes/prevent-dangling-narinfos` status and archive the change after merge (openspec-guard requires the active change be archived before merge).
