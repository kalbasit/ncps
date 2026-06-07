## 1. Reproduce (TDD red)

- [x] 1.1 Write a failing test: store a `Compression:none` NAR (physical object under `.nar.zst`) with `bytes_stored_at` set, call `Cache.DeleteNar(nar/<H>.nar)`, then assert the `.nar.zst` object is gone from the store (currently it leaks / errors)
- [x] 1.2 Assert that after deletion the `/upload` presence check reports absent AND no physical object remains (marker and disk agree)

## 2. Fix (TDD green)

- [x] 2.1 In `Cache.DeleteNar`, delete the actually-stored variant (the `.nar.zst` variant for `Compression:none`, mirroring `statNarInStore`) before clearing `bytes_stored_at`
- [x] 2.2 Track whether any variant was present; return `ErrNotFound` only when none was (preserve the established contract), and return non-not-found errors without clearing the marker

## 3. Verify

- [x] 3.1 Add a test that an absent `none` NAR (no variant present) returns `ErrNotFound` and leaves the marker set (contract preserved); confirm the existing xz "absent → ErrNotFound" backend test still passes
- [x] 3.2 `task fmt`, `task lint`, and `task test` all exit zero
