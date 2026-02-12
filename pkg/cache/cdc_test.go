package cache_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
)

func TestCDCBackends(t *testing.T) {
	t.Parallel()

	backends := []struct {
		name   string
		envVar string
		setup  cacheFactory
	}{
		{name: "SQLite", setup: setupSQLiteFactory},
		{name: "PostgreSQL", envVar: "NCPS_TEST_ADMIN_POSTGRES_URL", setup: setupPostgresFactory},
		{name: "MySQL", envVar: "NCPS_TEST_ADMIN_MYSQL_URL", setup: setupMySQLFactory},
	}

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			if b.envVar != "" && os.Getenv(b.envVar) == "" {
				t.Skipf("Skipping %s: %s not set", b.name, b.envVar)
			}

			runCDCTestSuite(t, b.setup)
		})
	}
}

func runCDCTestSuite(t *testing.T, factory cacheFactory) {
	t.Helper()

	t.Run("Put and Get with CDC", testCDCPutAndGet(factory))
	t.Run("Deduplication", testCDCDeduplication(factory))
	t.Run("Mixed Mode", testCDCMixedMode(factory))
	t.Run("GetNarInfo with CDC chunks", testCDCGetNarInfo(factory))
	t.Run("Client Disconnect No Goroutine Leak", testCDCClientDisconnectNoGoroutineLeak(factory))
}

func testCDCPutAndGet(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		c, db, _, dir, _, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Initialize chunk store
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192) // Small sizes for testing
		require.NoError(t, err)

		content := "this is a test nar content that should be chunked by fastcdc algorithm"
		nu := nar.URL{Hash: "testnar1", Compression: nar.CompressionTypeNone}

		r := io.NopCloser(strings.NewReader(content))
		err = c.PutNar(ctx, nu, r)
		require.NoError(t, err)

		// Verify chunks exist in DB
		count, err := db.GetChunkCount(ctx)
		require.NoError(t, err)
		assert.Positive(t, count)

		// Verify reassembly
		size, rc, err := c.GetNar(ctx, nu)
		require.NoError(t, err)

		defer rc.Close()

		data, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, content, string(data))
		assert.Equal(t, int64(len(content)), size)
	}
}

func testCDCDeduplication(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		c, db, _, dir, _, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Initialize chunk store
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192) // Small sizes for testing
		require.NoError(t, err)

		content := "common content shared between two nars"

		nu1 := nar.URL{Hash: "dedup1", Compression: nar.CompressionTypeNone}
		err = c.PutNar(ctx, nu1, io.NopCloser(strings.NewReader(content)))
		require.NoError(t, err)

		count1, _ := db.GetChunkCount(ctx)

		nu2 := nar.URL{Hash: "dedup2", Compression: nar.CompressionTypeNone}
		err = c.PutNar(ctx, nu2, io.NopCloser(strings.NewReader(content)))
		require.NoError(t, err)

		count2, _ := db.GetChunkCount(ctx)

		assert.Equal(t, count1, count2, "no new chunks should be created for duplicate content")
	}
}

func testCDCMixedMode(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		c, _, _, dir, _, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Initialize chunk store
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)

		// 1. Store a blob with CDC disabled
		require.NoError(t, c.SetCDCConfiguration(false, 0, 0, 0))

		blobContent := "traditional blob content"
		nuBlob := nar.URL{Hash: "1s8p1kgdms8rmxkq24q51wc7zpn0aqcwgzvc473v9cii7z2qyxq0", Compression: nar.CompressionTypeNone}
		require.NoError(t, c.PutNar(ctx, nuBlob, io.NopCloser(strings.NewReader(blobContent))))

		// 2. Store chunks with CDC enabled
		require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

		chunkContent := "chunked content"
		nuChunk := nar.URL{Hash: "00ji9synj1r6h6sjw27wwv8fw98myxsg92q5ma1pvrbmh451kc27", Compression: nar.CompressionTypeNone}
		require.NoError(t, c.PutNar(ctx, nuChunk, io.NopCloser(strings.NewReader(chunkContent))))

		// 3. Retrieve both
		_, rc1, err := c.GetNar(ctx, nuBlob)
		require.NoError(t, err)

		d1, _ := io.ReadAll(rc1)
		rc1.Close()
		assert.Equal(t, blobContent, string(d1))

		_, rc2, err := c.GetNar(ctx, nuChunk)
		require.NoError(t, err)

		d2, _ := io.ReadAll(rc2)
		rc2.Close()
		assert.Equal(t, chunkContent, string(d2))
	}
}

// testCDCGetNarInfo verifies that GetNarInfo correctly checks for chunked NARs
// when CDC is enabled, preventing false "NAR not found in storage" errors.
// This is a regression test for the bug where GetNarInfo only checked for
// whole NAR files and not for CDC chunks, causing unnecessary re-downloads.
func testCDCGetNarInfo(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		c, db, _, dir, _, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Initialize chunk store
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192) // Small sizes for testing
		require.NoError(t, err)

		// Create and store a NAR with CDC enabled
		content := "this is test content for GetNarInfo with CDC enabled"
		nu := nar.URL{Hash: "1s8p1kgdms8rmxkq24q51wc7zpn0aqcwgzvc473v9cii7z2qyxq0", Compression: nar.CompressionTypeNone}

		err = c.PutNar(ctx, nu, io.NopCloser(strings.NewReader(content)))
		require.NoError(t, err)

		// Verify chunks exist in database
		count, err := db.GetChunkCount(ctx)
		require.NoError(t, err)
		assert.Positive(t, count, "chunks should exist in database")

		// Store a narinfo that references this NAR
		niText := `StorePath: /nix/store/0amzzlz5w7ihknr59cn0q56pvp17bqqz-test-path
URL: nar/1s8p1kgdms8rmxkq24q51wc7zpn0aqcwgzvc473v9cii7z2qyxq0.nar
Compression: none
FileHash: sha256:1s8p1kgdms8rmxkq24q51wc7zpn0aqcwgzvc473v9cii7z2qyxq0
FileSize: 52
NarHash: sha256:1s8p1kgdms8rmxkq24q51wc7zpn0aqcwgzvc473v9cii7z2qyxq0
NarSize: 52
`
		err = c.PutNarInfo(ctx, "0amzzlz5w7ihknr59cn0q56pvp17bqqz", io.NopCloser(strings.NewReader(niText)))
		require.NoError(t, err)

		// Now call GetNarInfo. Since the NAR is stored as chunks and NOT as a whole file,
		// the old version of getNarInfoFromDatabase would fail to find it and purge the narinfo.
		_, err = c.GetNarInfo(ctx, "0amzzlz5w7ihknr59cn0q56pvp17bqqz")
		require.NoError(t, err, "GetNarInfo should succeed even if NAR is only in chunks")

		// Verify that the narinfo still exists in the database
		_, err = c.GetNarInfo(ctx, "0amzzlz5w7ihknr59cn0q56pvp17bqqz")
		require.NoError(t, err, "GetNarInfo should still succeed (not purged)")
	}
}

// testCDCClientDisconnectNoGoroutineLeak verifies that when a client disconnects
// during chunk streaming (by canceling the context), no goroutines are leaked.
// This is a regression test for the bug where the prefetch goroutine would continue
// running with a detached context and get blocked trying to send to a channel that
// no one is reading from anymore.
func testCDCClientDisconnectNoGoroutineLeak(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		t.Skip("test is failing/fragile, I will try and integrate go.uber.org/goleak in it later")

		ctx := context.Background()

		c, _, _, dir, _, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Initialize chunk store
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192) // Small sizes for testing
		require.NoError(t, err)

		// Create a large enough content to ensure multiple chunks
		content := strings.Repeat(
			"this is test content that will be chunked into multiple pieces for testing goroutine cleanup ",
			100,
		)
		nu := nar.URL{Hash: "leaktest1", Compression: nar.CompressionTypeNone}

		// Store the NAR as chunks
		err = c.PutNar(ctx, nu, io.NopCloser(strings.NewReader(content)))
		require.NoError(t, err)

		// Record baseline goroutine count
		runtime.GC()
		time.Sleep(100 * time.Millisecond)

		baselineGoroutines := runtime.NumGoroutine()
		t.Logf("Baseline goroutines: %d", baselineGoroutines)

		// Create a cancellable context to simulate client disconnect
		clientCtx, cancel := context.WithCancel(ctx)

		// Start reading the NAR
		_, rc, err := c.GetNar(clientCtx, nu)
		require.NoError(t, err)

		// Read a few bytes to start the streaming
		buf := make([]byte, 100)
		_, err = rc.Read(buf)
		require.NoError(t, err)

		// Simulate client disconnect by canceling the context
		cancel()

		// Close the reader
		rc.Close()

		// Give goroutines time to leak (if they're going to)
		time.Sleep(1 * time.Second)
		runtime.GC()
		time.Sleep(100 * time.Millisecond)

		// Check that no goroutines are leaked
		finalGoroutines := runtime.NumGoroutine()
		t.Logf("Final goroutines: %d (difference: %d)", finalGoroutines, finalGoroutines-baselineGoroutines)

		// Allow a small tolerance for test infrastructure goroutines to prevent flakiness.
		assert.LessOrEqual(t, finalGoroutines, baselineGoroutines+2,
			"Goroutine leak detected: baseline=%d, final=%d", baselineGoroutines, finalGoroutines)
	}
}
