## 1. De-chunk robustness (chunks-to-nar-migration)

- [x] 1.1 Add a `narURL` parameter to `linkedNarinfoNarHash` and thread it from the `MigrateChunksToNar` caller
- [x] 1.2 On no join link (`IsNotFound`), resolve the narinfo by the NAR's `Compression:none` URL; return its NarHash
- [x] 1.3 Test: an unlinked chunked NAR de-chunks via the URL-resolved NarHash (`TestMigrateChunksToNar_DeChunksUnlinkedNarViaURLFallback`)

## 2. fsck safety (fsck)

- [x] 2.1 Add `relinkNarInfoToBackingNarFile`: parse the narinfo URL, find a `nar_file` by hash+query (any compression), upsert the missing `narinfo_nar_files` link
- [x] 2.2 In `repairFsckIssues`, attempt relink before deleting an unlinked narinfo; delete only when no backing nar_file exists
- [x] 2.3 Tests: repair-not-delete when a backing nar_file exists; still-delete when none exists (`testFsckRepairsUnlinkedNarinfoWithBackingNarFile`, `testFsckDeletesUnlinkedNarinfoWithNoBackingNarFile`)

## 3. Creation-race prevention (cdc-chunking)

- [x] 3.1 In `checkAndFixNarInfosForNar`, resolve the backing nar_file and upsert the `narinfo_nar_files` link for every URL-matching narinfo (idempotent)
- [x] 3.2 Test: chunking-completion reconciliation creates the missing link (`TestCheckAndFixNarInfosForNar_ReconcilesMissingLink`)

## 4. Verification

- [x] 4.1 `task fmt`, `task lint`, `task test` pass
- [x] 4.2 fsck repair tests run `-count=3` non-flaky

## 5. Operational remediation (cdc-drain-mode)

- [ ] 5.1 Deploy the image; re-run `migrate-chunks-to-nar` to drain the previously-stranded chunked NARs
- [ ] 5.2 Confirm the remaining-chunked count reaches zero, then exit drain mode (remove the chunk-store config)
