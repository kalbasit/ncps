## 1. Shared constant

- [x] 1.1 Define a package-level constant `fsckEagerLoadBatchSize = 1000` in
      `pkg/ncps/fsck.go` with a one-line comment naming the PostgreSQL 65535
      extended-protocol cap as the reason. Both refactors below reference it.

## 2. Refactor `queryCDCNarFilesWithSizeMismatch`

- [x] 2.1 Rewrite `queryCDCNarFilesWithSizeMismatch` in `pkg/ncps/fsck.go` to:
      (a) walk `nar_file` rows with `TotalChunksGT(0)` in keyset-paginated batches of
      `fsckEagerLoadBatchSize`, ordered by ID ascending (use `IDGT(lastID)` as the
      pagination predicate);
      (b) for each batch, run a second query against `narinfo_nar_files` with
      `NarFileIDIn(ids...)` and `WithNarinfo()` eager-load (now bounded by batch size);
      (c) compare `nar_file.file_size` to each linked `narinfo.nar_size` in memory and
      append to the mismatched slice, deduplicating per `nar_file` as the existing
      `break` already does;
      (d) stop when a short page (fewer than `fsckEagerLoadBatchSize` rows) is returned.
- [x] 2.2 Remove the unbounded `.All(ctx)` + outer `WithNarInfoNarFiles(...)` eager-load
      from the old implementation.
- [x] 2.3 Ensure all three call sites of `queryCDCNarFilesWithSizeMismatch` (phase 1g
      collection at `fsck.go:679`, re-verify at `fsck.go:978`, and repair re-verify at
      `fsck.go:1580`) continue to compile and behave identically — no signature change.

## 3. Refactor `chunksForNarFile`

- [x] 3.1 Rewrite `chunksForNarFile` in `pkg/ncps/fsck.go` to:
      (a) walk `nar_file_chunks` rows for the given `narFileID` in keyset-paginated
      batches of `fsckEagerLoadBatchSize`, ordered by `chunk_index` ascending (use
      `ChunkIndexGT(lastIndex)` as the pagination predicate);
      (b) for each batch, fetch chunks with `Chunk.Query().Where(IDIn(ids...)).All(ctx)`
      and re-order locally to match the link order from the page before appending to
      the result slice;
      (c) stop when a short page is returned.
- [x] 3.2 Remove the unbounded `WithChunk().All(ctx)` from the old implementation.
- [x] 3.3 Confirm the function's return contract (chunks in `chunk_index` order, no
      `nil` entries) is preserved — both call paths in fsck rely on it.

## 4. Tests

- [x] 4.1 Confirm the existing small-cache CDC size-mismatch and chunk-walk tests in
      `pkg/ncps/fsck_test.go` still pass under SQLite, PostgreSQL, and MySQL cohorts
      (no behavioral change for small inputs).
- [x] 4.2 Add `TestQueryCDCNarFilesWithSizeMismatch_LargePostgreSQL` in
      `pkg/ncps/fsck_test.go` (package `ncps_test`, `t.Parallel()`), gated on
      `NCPS_TEST_ADMIN_POSTGRES_URL`. Seed > 70_000 CDC `nar_file` rows (using bulk
      insert) with a known-small subset whose `file_size` differs from the linked
      `narinfo.nar_size`. Assert the function returns without error and the returned
      set equals exactly the seeded mismatched hashes.
- [x] 4.3 Add `TestChunksForNarFile_LargePostgreSQL` in `pkg/ncps/fsck_test.go`
      (package `ncps_test`, `t.Parallel()`), gated on `NCPS_TEST_ADMIN_POSTGRES_URL`.
      Seed one `nar_file` linked to > 70_000 distinct chunks via `nar_file_chunks`
      (bulk insert). Assert the function returns without error, returns chunks in
      `chunk_index` order, and length equals the seeded count.
- [x] 4.4 Verify both new tests fail on the pre-fix code (sanity check that they would
      have caught the regression) — record the observed errors in the PR description.
      Confirmed: pre-fix `TestQueryCDCNarFilesWithSizeMismatch_LargePostgreSQL` fails
      with `query CDC nar_files: extended protocol limited to 65535 parameters`;
      pre-fix `TestChunksForNarFile_LargePostgreSQL` fails with
      `extended protocol limited to 65535 parameters`. Both pass after the fix.

## 5. Validation

- [x] 5.1 Run `nix fmt` and `golangci-lint run --fix` to satisfy formatting and lint.
- [x] 5.2 Run `go test -race ./pkg/ncps/...` with all three database cohorts enabled
      (`eval "$(enable-integration-tests)"`) and confirm green. Full
      `TestFsckBackends` tree (SQLite + PostgreSQL + MySQL, all subtests including
      the CDC tree) plus both new large-PG regression tests passed.
- [ ] 5.3 Run `nix build .#checks.x86_64-linux.ncps-postgres-tests -L --no-link` to
      confirm the PostgreSQL cohort passes both new regression tests.
- [ ] 5.4 Manually re-run `ncps fsck` against a PostgreSQL cache snapshot equivalent to
      the failing production run (or a synthetic dataset of equivalent scale) and
      confirm phase 1g completes and fsck advances to phase 2.
