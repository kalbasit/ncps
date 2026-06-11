## 1. Reproduce (TDD red)

- [x] 1.1 Add a `pkg/ncps` test that seeds a desynced narinfo (advertises `xz`, `url=nar/<file_hash>.nar.xz`, `file_hash`/`file_size` set) linked to a non-xz `nar_file`, plus a healthy `xz`/`xz` pair, then calls the repair and asserts only the desynced one is rewritten to `none` form. Confirm it fails before the repair exists (RED).

## 2. Implement the repair (TDD green)

- [x] 2.1 Add `repairNarInfoCompressionDesync(ctx, dbClient) (int, error)` in `pkg/ncps/fsck.go`: find narinfos advertising a non-producible compression (`xz`) with no matching `xz` nar_file but a backing non-xz nar_file; rewrite each to `URL: nar/<nar_hash>.nar`, `Compression: none`, clearing `FileHash`/`FileSize`. Return the count.
- [x] 2.2 Invoke it from `repairFsckIssues` (under `--repair`) and log the repaired count.
- [x] 2.3 Confirm the test passes (GREEN) and a second run repairs zero (idempotent).

## 3. Verify (no regressions)

- [x] 3.1 Existing fsck tests pass; healthy narinfos untouched.
- [x] 3.2 `task fmt`, `task lint`, `task test` all green.
