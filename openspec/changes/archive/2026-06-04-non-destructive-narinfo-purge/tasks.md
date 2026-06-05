## 1. Failing tests (red)

- [x] 1.1 Add internal test: non-upload `getNarInfoFromDatabase` for a narinfo with a missing backing NAR returns `errNarInfoPurged` and does NOT delete the narinfo or `nar_file` records (assert both rows still present after the call).
- [x] 1.2 Add internal test: non-upload `getNarInfoFromStore` for a store-sourced narinfo with a missing NAR returns `errNarInfoPurged` without deleting records.
- [x] 1.3 Add internal/handler test: substituter `GET /{hash}.narinfo` with missing NAR but available upstream returns HTTP 200 and the records are never deleted mid-request (re-fetch heals in place); upstream-miss returns 404 with records left intact.
- [x] 1.4 Add internal test for refcount-safe `purgeNarInfo`: two narinfos `A` and `B` linked to the same `nar_file` `F`; purging `A` removes `A` but leaves `F` (and its NAR bytes) because `B` still links it; a sole-linker purge does delete `F`.
- [x] 1.5 Run the new tests and confirm they FAIL against current code (capture output).

## 2. Implementation (green)

- [x] 2.1 In `getNarInfoFromDatabase` (`pkg/cache/cache.go` ~4427-4439): remove the `purgeNarInfo` call on the missing-NAR branch; return `errNarInfoPurged` for both upload-only and non-upload (collapse the now-redundant `IsUploadOnly` purge special-case).
- [x] 2.2 In `getNarInfoFromStore` (~4164-4173): remove the `purgeNarInfo` call on the missing-NAR branch; return `errNarInfoPurged`.
- [x] 2.3 Make `purgeNarInfo` (~4580) refcount-aware: delete the `nar_file` record + NAR bytes only when no other `NarInfoNarFile` link to that `nar_file` remains (mirror `RunLRU`'s `Not(HasNarInfoNarFiles())`); always still delete the narinfo row + narinfo store file.
- [x] 2.4 Verify the upstream-fetch narinfo upsert overwrites a stale in-place record (add/confirm a test); fix the upsert if it does not.
- [x] 2.5 Run the new tests and confirm they PASS.

## 3. Regression + spec alignment

- [x] 3.1 Update existing `narinfo-purge-serving` tests that asserted a destructive root purge to assert the non-destructive behavior (modify, do not duplicate).
- [x] 3.2 Confirm upload-only purge tests and `nar-cache-miss-recovery` tests still pass unchanged.
- [x] 3.3 Grep for any remaining `purgeNarInfo` callers in read paths; confirm only the corrupt-narinfo (`:4136`) caller remains.

## 4. Verify

- [x] 4.1 `task fmt` exits 0.
- [x] 4.2 `task lint` exits 0.
- [x] 4.3 `task test` exits 0 (cache package under race).
- [x] 4.4 `openspec validate non-destructive-narinfo-purge` passes.
