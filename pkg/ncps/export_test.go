package ncps

// Test-only re-exports of fsck internals so regression tests in package
// ncps_test can exercise the bounded-batch query paths directly without
// driving the full fsck CLI. export_test.go is the canonical Go idiom for
// exposing unexported symbols to a black-box _test package.
//
//nolint:gochecknoglobals // intentional: see comment above.
var (
	QueryCDCNarFilesWithSizeMismatchForTest   = queryCDCNarFilesWithSizeMismatch
	ChunksForNarFileForTest                   = chunksForNarFile
	MigrateChunksToNarProgressIntervalForTest = &migrateChunksToNarProgressInterval
)
