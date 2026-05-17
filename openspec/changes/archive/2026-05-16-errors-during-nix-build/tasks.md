## 1. Result Types and Flag

- [x] 1.1 Add `narFilesWithCorruptChunks []database.NarFile` and `narFilesWithHashMismatch []database.NarFile` fields to `fsckResults` in `pkg/ncps/fsck.go`
- [x] 1.2 Update `fsckResults.totalIssues()` to include both new categories
- [x] 1.3 Add `--verify-content` bool flag to `fsckCommand`; thread `verifyContent bool` through to `collectFsckSuspects` and `reVerifyFsckSuspects`
- [x] 1.4 Update `printFsckSummary` to render "NAR files w/ corrupt chunks" and "NAR files w/ hash mismatch" CDC rows only when `verifyContent` is true

## 2. Chunk Content Verification (TDD)

- [x] 2.1 Write failing tests in `pkg/ncps/fsck_test.go`: corrupt chunk is detected; clean chunks pass; flag absent skips content reads
- [x] 2.2 Implement `collectNarFilesWithCorruptChunks(ctx, db, chunkStore, allNarFiles []database.GetAllNarFilesRow, verifiedSince time.Duration)`: iterate CDC NAR files, call `GetChunk` per chunk, stream into `sha256.New()` via `io.Copy`, compare digest against stored hash; skip NARs already in `narFilesWithChunkIssues`
- [x] 2.3 Call `collectNarFilesWithCorruptChunks` from `collectFsckSuspects` (phase 1g or 1h, only when `verifyContent` is true)
- [x] 2.4 Run tests green; run `golangci-lint run --fix`

## 3. End-to-End NAR Hash Verification (TDD)

- [x] 3.1 Write failing tests: assembled hash matches narinfo NarHash; assembled hash mismatches; check is skipped when a chunk is already corrupt
- [x] 3.2 Implement `collectNarFilesWithHashMismatch(ctx, db, chunkStore, allNarFiles []database.GetAllNarFilesRow, corruptByID map[int64]struct{}, verifiedSince time.Duration)`: for each CDC NAR file not in `corruptByID`, fetch chunks in order, stream all into `sha256.New()`, decode narinfo `NarHash` from nix-base32 and compare
- [x] 3.3 Call `collectNarFilesWithHashMismatch` from `collectFsckSuspects` after corrupt-chunk collection, passing `corruptByID`
- [x] 3.4 Run tests green; run `golangci-lint run --fix`

## 4. Re-verify and Repair

- [x] 4.1 Write failing tests for re-verify phase: corrupt chunk clears after fix; hash-mismatch clears after fix
- [x] 4.2 Add re-verify passes for `narFilesWithCorruptChunks` and `narFilesWithHashMismatch` in `reVerifyFsckSuspects` (re-run content hash checks per NAR)
- [x] 4.3 Add repair in `repairFsckIssues`: call `repairBrokenCDCNarFiles` for both `narFilesWithCorruptChunks` and `narFilesWithHashMismatch` (same cascade — delete nar_file, orphaned narinfo, orphaned chunks)
- [x] 4.4 Write failing tests for repair cascade: nar_file + narinfo + chunks deleted; dry-run leaves everything intact
- [x] 4.5 Run tests green; run `golangci-lint run --fix`

## 5. Helm Chart

- [x] 5.1 Add `verifyContent: false` under `fsck:` in `charts/ncps/values.yaml` with a comment explaining the I/O cost
- [x] 5.2 Add `{{- if .Values.fsck.verifyContent }} - --verify-content {{- end }}` block in `charts/ncps/templates/fsck-cronjob.yaml`
- [x] 5.3 Write helm-unittest test in `charts/ncps/tests/` verifying `--verify-content` is present when `fsck.verifyContent: true` and absent when false
- [x] 5.4 Run `helm unittest charts/ncps` to confirm tests pass

## 6. Documentation

- [x] 6.1 Update `docs/docs/User Guide/Operations/Integrity Check (fsck).md`: add `--verify-content` to the flags reference, explain the I/O cost, document the `--verify-content --verified-since` combination for large caches
- [x] 6.2 Update `docs/docs/User Guide/Installation/Helm Chart.md`: document `fsck.verifyContent` value (type, default, when to enable)
- [x] 6.3 Run `nix fmt` to format all changed files
