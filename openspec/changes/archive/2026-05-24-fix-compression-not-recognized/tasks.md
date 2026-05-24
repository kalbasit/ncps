## Tasks

### Task 1: Write failing tests for GetNarInfo normalization

File: `pkg/cache/cache_test.go`

Add tests:
- `TestGetNarInfo_CDCMode_NormalizesCompressionWhenChunked`: narinfo with xz in DB,
  NAR in chunks → returned narinfo has Compression=none
- `TestGetNarInfo_CDCMode_NoNormalizationWhenNotChunked`: narinfo with xz in DB, NAR
  NOT in chunks → returned narinfo retains xz
- `TestGetNarInfo_CDCDisabled_NoNormalization`: CDC disabled, narinfo with xz → retains xz

### Task 2: Implement the fix in GetNarInfo

File: `pkg/cache/cache.go`

In the block at lines ~3175-3195 where `narURL.Compression != nar.CompressionTypeNone`:
- After calling `maybeBackgroundMigrateNarToChunks`
- Add: if `c.isCDCEnabled()` and `c.HasNarInChunks(ctx, narURL)` returns true,
  normalize `narInfo.URL`, `narInfo.Compression`, `narInfo.FileHash`, `narInfo.FileSize`

### Task 3: Run tests and lint

- `go test -race -run TestGetNarInfo ./pkg/cache/...`
- `golangci-lint run --fix ./pkg/cache/...`
- `go test -race ./...` (full suite)
