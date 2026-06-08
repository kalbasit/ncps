## 1. Reproduce with failing tests (TDD red)

- [x] 1.1 Add a test fixture / test-upstream narinfo with `Compression: zstd`, valid `NarHash`/`NarSize`, and NO `FileSize`/`FileHash` (mirror issue #1314's example).
- [x] 1.2 In `pkg/cache/upstream/cache_test.go`, add a failing test asserting `GetNarInfo` accepts the compressed-no-FileSize narinfo (no `invalid narinfo: FileSize is missing` error).
- [x] 1.3 In `pkg/cache/cache_test.go`, add a failing test (`testNarInfoFileHashFix`): a compressed narinfo lacking `FileHash`/`FileSize`, once its NAR is stored, has both backfilled — `FileSize` == stored compressed size and `FileHash` == `sha256:<nixbase32>` of the stored compressed bytes.
- [x] 1.4 Add a regression subtest asserting an upstream narinfo that already provides `FileHash` keeps it verbatim (no recompute) after the NAR is stored.

## 2. Stop rejecting compressed narinfos (upstream layer)

- [x] 2.1 In `pkg/cache/upstream/cache.go` (`GetNarInfo`, ~line 468-477), remove the `Compression != none` rejection branch and its TODO; keep the `FileSize = NarSize` fallback only for the `Compression: none` case. Leave `FileSize == 0`/`FileHash == nil` for compressed NARs.
- [x] 2.2 Confirm tasks 1.1-1.2 now pass; the `ErrInvalidNarInfo` "FileSize is missing" path is gone.

## 3. Compute FileHash from the stored NAR (green)

> Note: FileSize backfill already existed via the post-store fixup
> (`CheckAndFixNarInfo` → `getNarActualSize` → `fixNarInfoFileSize`, reading
> `nar_file.file_size`). It simply never ran for compressed NARs because the upstream
> layer rejected them first. So the only new computation needed is FileHash. The fix
> reuses that existing fixup path instead of tapping the in-flight pull stream — no
> schema migration, idempotent, runs for both client uploads and upstream pulls.

- [x] 3.1 Add `computeStoredNarFileHash` (`pkg/cache/cache.go`): stream the stored compressed NAR (`narStore.GetNar`) through `sha256` once (constant memory, no buffering), returning `sha256:<nixbase32>`.
- [x] 3.2 Build the hash via `nixhash.MustNewHashWithEncoding(nixhash.SHA256, sum, nixhash.NixBase32, true)`; add `fixNarInfoFileHash` to persist it (mirrors `fixNarInfoFileSize`).
- [x] 3.3 Gate strictly in `CheckAndFixNarInfo`: compute only when `Compression != none` (the `none` branch returns earlier), the NAR is whole-file in store (`hasNarInStore`), and `file_hash` is currently NULL/empty (skips the SHA-256 pass when upstream supplied it).

## 4. Backfill the computed values into the persisted narinfo

- [x] 4.1 Restructure `CheckAndFixNarInfo` so the FileSize fix no longer early-returns, then conditionally backfill `file_hash`. The backfill runs from `checkAndFixNarInfosForNar`, already invoked after every store in `pullNarIntoStore` (upstream pulls) and `PutNar` (client uploads).
- [x] 4.2 Verify task 1.3 passes: after the NAR is stored, the persisted narinfo carries the backfilled `FileSize`/`FileHash`.

## 5. Protect existing behavior

- [x] 5.1 Confirm `Compression: none` still nulls `file_hash`/`file_size` — unchanged `checkAndFixNarInfoNoCompression` branch; covered by existing `testNarInfoFileSizeFix` none subtests (the new logic lives only in the compressed branch).
- [x] 5.2 Opaque-URL compressed case is served under the same compressed-serve/fixup path, so it is covered by the same compute/backfill logic (no separate code path).

## 6. Verify

- [x] 6.1 Run `task fmt`, `task lint`, `task test` and confirm all exit 0. (fmt: 0 changed; lint: 0 issues in this worktree; test: all packages ok.)
- [x] 6.2 Add a `### Fixed` CHANGELOG entry referencing issue #1314.
