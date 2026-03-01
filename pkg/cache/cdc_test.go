package cache_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/chunker"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/pkg/zstd"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// compressXz compresses data using xz and returns the compressed bytes as a string.
func compressXz(t *testing.T, data string) string {
	t.Helper()

	var buf bytes.Buffer

	xw, err := xz.NewWriter(&buf)
	require.NoError(t, err)

	_, err = io.WriteString(xw, data)
	require.NoError(t, err)

	require.NoError(t, xw.Close())

	return buf.String()
}

// slowChunker wraps a real Chunker and adds a configurable delay before producing any chunks.
// Used to simulate slow CDC chunking in tests to verify that the HTTP response does not
// block on CDC chunking completion.
type slowChunker struct {
	real  chunker.Chunker
	delay time.Duration
}

func (s *slowChunker) Chunk(ctx context.Context, r io.Reader) (<-chan *chunker.Chunk, <-chan error) {
	chunksChan := make(chan *chunker.Chunk)
	errChan := make(chan error, 1)

	go func() {
		defer close(chunksChan)

		// Simulate slow chunking by sleeping before starting.
		timer := time.NewTimer(s.delay)
		defer timer.Stop()

		select {
		case <-timer.C:
		case <-ctx.Done():
			errChan <- ctx.Err()

			return
		}

		// Delegate to the real chunker.
		realChunksChan, realErrChan := s.real.Chunk(ctx, r)

		for {
			select {
			case <-ctx.Done():
				errChan <- ctx.Err()

				return
			case err := <-realErrChan:
				if err != nil {
					errChan <- err
				}

				return
			case ch, ok := <-realChunksChan:
				if !ok {
					return
				}

				select {
				case chunksChan <- ch:
				case <-ctx.Done():
					ch.Free()

					errChan <- ctx.Err()

					return
				}
			}
		}
	}()

	return chunksChan, errChan
}

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
	t.Run("chunks are stored compressed", testCDCChunksAreCompressed(factory))
	t.Run("decompress zstd before chunking", testCDCDecompressZstdBeforeChunking(factory))
	t.Run("pullNarInfo normalizes compression for CDC", testCDCPullNarInfoNormalizesCompression(factory))
	t.Run("pullNarInfo sets FileSize == NarSize for CDC", testCDCPullNarInfoSetsFileSizeForCDC(factory))
	t.Run("MigrateNarToChunks updates narinfo compression, FileSize, and FileHash",
		testCDCMigrateNarToChunksUpdatesNarInfo(factory))
	t.Run("MigrateNarToChunks links narinfo_nar_files", testCDCMigrateNarToChunksLinksNarInfoNarFiles(factory))
	t.Run("MigrateNarToChunks deletes original whole-file NAR", testCDCMigrateNarToChunksDeletesOriginalFile(factory))
	t.Run("MigrateNarToChunks compression normalization is atomic",
		testCDCMigrateNarToChunksCompressionNormalizationIsAtomic(factory))
	t.Run("MigrateNarToChunks recovers from partial chunking",
		testCDCMigrateNarToChunksRecoversFromPartialChunking(factory))
	t.Run("PutNarInfo normalizes compression for CDC", testCDCPutNarInfoNormalizesCompression(factory))
	t.Run("PutNarInfo sets FileSize == 0 and FileHash == null for CDC", testCDCPutNarInfoSetsFileSizeForCDC(factory))
	t.Run("PutNarInfo does not log context canceled after request ends", testCDCPutNarInfoNoContextCanceled(factory))
	t.Run("PutNar does not overwrite FileSize once chunked", testCDCPutNarDoesNotOverwriteFileSizeOnceChunked(factory))
	t.Run("MigrateNarToChunks heals stale narinfo URL on second call",
		testCDCMigrateNarToChunksHealsStaleNarInfoURLOnSecondCall(factory))
	t.Run("stale lock cleanup deletes orphaned chunk files",
		testCDCStaleLockCleansUpChunkFiles(factory))
	t.Run("first pull completes before CDC chunking finishes",
		testCDCFirstPullCompletesBeforeChunking(factory))
	t.Run("GetNar does not panic when CDC is disabled but DB has chunked NARs",
		testCDCDisabledWithChunkedNARsInDB(factory))
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

// testCDCDecompressZstdBeforeChunking verifies that when a zstd-compressed NAR
// is stored with CDC enabled, the data is decompressed before chunking.
// This ensures that:
// 1. The nar_files DB record stores Compression: none (not zstd)
// 2. Chunks contain raw uncompressed data (not double-compressed)
// 3. Reassembly returns the original uncompressed content.
func testCDCDecompressZstdBeforeChunking(factory cacheFactory) func(*testing.T) {
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

		// Create original uncompressed content
		originalContent := strings.Repeat("test content for decompression before chunking ", 100)

		// Compress it with zstd
		compressedContent := cache.CompressZstd(t, originalContent)

		// Store the zstd-compressed NAR with CompressionTypeZstd
		nu := nar.URL{Hash: "decompress-zstd-test1", Compression: nar.CompressionTypeZstd}

		r := io.NopCloser(strings.NewReader(compressedContent))
		err = c.PutNar(ctx, nu, r)
		require.NoError(t, err)

		// Verify the nar_files record stores Compression: none (not zstd)
		// After CDC decompression, the narURL should have been normalized

		narFile, err := db.GetNarFileByHashAndCompressionAndQuery(ctx, database.GetNarFileByHashAndCompressionAndQueryParams{
			Hash:        nu.Hash,
			Compression: nar.CompressionTypeNone.String(),
			Query:       "",
		})
		require.NoError(t, err, "nar_files should have Compression: none after CDC decompression")
		assert.Equal(t, nar.CompressionTypeNone.String(), narFile.Compression)

		// Verify chunks exist
		chunks, err := db.GetChunksByNarFileID(ctx, narFile.ID)
		require.NoError(t, err)
		require.NotEmpty(t, chunks, "should have chunks in the database")

		// Verify that the total uncompressed chunk size matches the original content size
		// (not the compressed size), proving decompression happened before chunking
		var totalChunkSize int64
		for _, ch := range chunks {
			totalChunkSize += int64(ch.Size)
		}

		assert.Equal(t, int64(len(originalContent)), totalChunkSize,
			"total chunk size should match original uncompressed content size, not compressed size")

		// Verify reassembly returns the original uncompressed content
		// We need to retrieve using CompressionTypeNone since that's what's in the DB
		nuNone := nar.URL{Hash: nu.Hash, Compression: nar.CompressionTypeNone}
		size, rc, err := c.GetNar(ctx, nuNone)
		require.NoError(t, err)

		defer rc.Close()

		data, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, originalContent, string(data), "reassembled data should be original uncompressed content")
		assert.Equal(t, int64(len(originalContent)), size)
	}
}

func testCDCChunksAreCompressed(factory cacheFactory) func(*testing.T) {
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

		// Use highly compressible data (repeated bytes)
		content := strings.Repeat("compressible", 1000)
		nu := nar.URL{Hash: "testnar-zstd", Compression: nar.CompressionTypeNone}

		r := io.NopCloser(strings.NewReader(content))
		err = c.PutNar(ctx, nu, r)
		require.NoError(t, err)

		// Verify chunks exist in DB and have compressed_size set
		narFile, err := db.GetNarFileByHashAndCompressionAndQuery(ctx, database.GetNarFileByHashAndCompressionAndQueryParams{
			Hash:        nu.Hash,
			Compression: nu.Compression.String(),
			Query:       nu.Query.Encode(),
		})
		require.NoError(t, err)

		chunks, err := db.GetChunksByNarFileID(ctx, narFile.ID)
		require.NoError(t, err)
		require.NotEmpty(t, chunks, "should have chunks in the database")

		var totalSize, totalCompressedSize int64
		for _, chunk := range chunks {
			totalSize += int64(chunk.Size)
			totalCompressedSize += int64(chunk.CompressedSize)
			assert.Positive(t, chunk.CompressedSize, "compressed size should be positive")
		}

		assert.Equal(t, int64(len(content)), totalSize, "sum of chunk sizes should equal original content size")
		assert.Less(t, totalCompressedSize, totalSize,
			"total compressed size should be less than total original size for compressible data")

		// Verify reassembly to ensure compression is transparent
		size, rc, err := c.GetNar(ctx, nu)
		require.NoError(t, err)

		defer rc.Close()

		data, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, content, string(data), "decompressed data should match original")
		assert.Equal(t, int64(len(content)), size, "size should match original content size")
	}
}

// testCDCPullNarInfoNormalizesCompression verifies that when CDC is enabled and
// a narinfo is pulled from upstream, the narinfo stored in the database has
// Compression: none and URL without compression extension, regardless of
// the upstream's compression (xz, zstd via Harmonia, etc.).
func testCDCPullNarInfoNormalizesCompression(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ts := testdata.NewTestServer(t, 40)
		t.Cleanup(ts.Close)

		c, db, _, dir, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Set up CDC
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
		require.NoError(t, err)

		// Set up upstream cache
		uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
			PublicKeys: testdata.PublicKeys(),
		})
		require.NoError(t, err)

		c.AddUpstreamCaches(newContext(), uc)

		// Wait for upstream to become available
		<-c.GetHealthChecker().Trigger()

		t.Run("xz-compressed upstream narinfo is normalized to none", func(t *testing.T) {
			// Nar2 has Compression: xz upstream
			ni, err := c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
			require.NoError(t, err)

			// Verify narinfo returned to client says Compression: none
			assert.Equal(t, nar.CompressionTypeNone.String(), ni.Compression,
				"narinfo Compression should be normalized to 'none' for CDC")

			// Verify the URL has no compression extension
			assert.NotContains(t, ni.URL, ".xz",
				"narinfo URL should not contain .xz extension for CDC")
			assert.NotContains(t, ni.URL, ".zst",
				"narinfo URL should not contain .zst extension for CDC")

			// Verify the narinfo in the database also says Compression: none
			var compression, url string

			err = db.DB().QueryRowContext(context.Background(),
				rebind("SELECT compression, url FROM narinfos WHERE hash = ?"),
				testdata.Nar2.NarInfoHash).Scan(&compression, &url)
			require.NoError(t, err)
			assert.Equal(t, nar.CompressionTypeNone.String(), compression,
				"narinfo in DB should have Compression: none")
			assert.NotContains(t, url, ".xz",
				"narinfo URL in DB should not contain .xz extension")
		})

		t.Run("Harmonia narinfo with none compression stays none", func(t *testing.T) {
			// Nar7 has Compression: none upstream (Harmonia-like, served with zstd Content-Encoding)
			ni, err := c.GetNarInfo(context.Background(), testdata.Nar7.NarInfoHash)
			require.NoError(t, err)

			// Should still be none
			assert.Equal(t, nar.CompressionTypeNone.String(), ni.Compression,
				"Harmonia narinfo Compression should remain 'none' for CDC")

			// Verify the URL has no compression extension
			assert.NotContains(t, ni.URL, ".zst",
				"Harmonia narinfo URL should not contain .zst extension for CDC")
		})
	}
}

// testCDCMigrateNarToChunksUpdatesNarInfo verifies that after migrating a NAR
// from whole-file storage to CDC chunks, the narinfo in the database is updated
// to Compression: none and URL without compression extension.
func testCDCMigrateNarToChunksUpdatesNarInfo(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		c, db, _, dir, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// 1. Store a NAR as whole file (CDC disabled) with xz compression
		entry := testdata.Nar1
		narURL := nar.URL{Hash: entry.NarHash, Compression: entry.NarCompression}

		err := c.PutNar(ctx, narURL, io.NopCloser(strings.NewReader(entry.NarText)))
		require.NoError(t, err)

		// 2. Store a narinfo referencing this NAR with xz compression
		err = c.PutNarInfo(ctx, entry.NarInfoHash, io.NopCloser(strings.NewReader(entry.NarInfoText)))
		require.NoError(t, err)

		// Verify narinfo in DB has original compression
		var (
			compression, url string
			fileSize         sql.NullInt64
			fileHash         sql.NullString
		)

		err = db.DB().QueryRowContext(ctx,
			rebind("SELECT compression, url, file_size, file_hash FROM narinfos WHERE hash = ?"),
			entry.NarInfoHash).Scan(&compression, &url, &fileSize, &fileHash)
		require.NoError(t, err)
		assert.Equal(t, "xz", compression, "narinfo should initially have xz compression")
		assert.Contains(t, url, ".xz", "narinfo URL should initially contain .xz")
		assert.True(t, fileSize.Valid, "file_size should not be NULL before migration")
		assert.True(t, fileHash.Valid, "file_hash should not be NULL before migration")

		// 3. Enable CDC
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
		require.NoError(t, err)

		// 4. Migrate the NAR to chunks
		err = c.MigrateNarToChunks(ctx, &narURL)
		require.NoError(t, err)

		// 5. Verify the narinfo in DB now says Compression: none and URL without .xz
		// as well as FileSize = 0 and FileHash = null
		err = db.DB().QueryRowContext(ctx,
			rebind("SELECT compression, url, file_size, file_hash FROM narinfos WHERE hash = ?"),
			entry.NarInfoHash).Scan(&compression, &url, &fileSize, &fileHash)
		require.NoError(t, err)
		assert.Equal(t, nar.CompressionTypeNone.String(), compression,
			"narinfo should have Compression: none after CDC migration")
		assert.NotContains(t, url, ".xz",
			"narinfo URL should not contain .xz after CDC migration")
		assert.False(t, fileSize.Valid, "file_size should be NULL after migration")
		assert.False(t, fileHash.Valid, "file_hash should be NULL after migration")

		// 6. Verify narinfo_nar_files is populated after migration.
		var narInfoNarFilesCount int

		err = db.DB().QueryRowContext(ctx,
			rebind("SELECT COUNT(*) FROM narinfo_nar_files WHERE narinfo_id = (SELECT id FROM narinfos WHERE hash = ?)"),
			entry.NarInfoHash).Scan(&narInfoNarFilesCount)
		require.NoError(t, err)
		assert.Equal(t, 1, narInfoNarFilesCount,
			"narinfo_nar_files must have exactly one entry after CDC migration")
	}
}

// testCDCMigrateNarToChunksLinksNarInfoNarFiles verifies that after MigrateNarToChunks,
// the narinfo_nar_files junction table is correctly populated so verify-data checks pass.
func testCDCMigrateNarToChunksLinksNarInfoNarFiles(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		c, db, _, dir, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// 1. Store a NAR as a whole file (CDC disabled) with xz compression
		entry := testdata.Nar1
		narURL := nar.URL{Hash: entry.NarHash, Compression: entry.NarCompression}

		err := c.PutNar(ctx, narURL, io.NopCloser(strings.NewReader(entry.NarText)))
		require.NoError(t, err)

		// 2. Store a narinfo referencing this NAR
		err = c.PutNarInfo(ctx, entry.NarInfoHash, io.NopCloser(strings.NewReader(entry.NarInfoText)))
		require.NoError(t, err)

		// 3. Verify narinfo is linked to a nar_file before migration
		var initialCount int

		err = db.DB().QueryRowContext(ctx,
			rebind("SELECT COUNT(*) FROM narinfo_nar_files WHERE narinfo_id = (SELECT id FROM narinfos WHERE hash = ?)"),
			entry.NarInfoHash).Scan(&initialCount)
		require.NoError(t, err)
		assert.Equal(t, 1, initialCount, "narinfo should be linked to a nar_file before migration")

		// 4. Enable CDC
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
		require.NoError(t, err)

		// 5. Migrate the NAR to chunks
		err = c.MigrateNarToChunks(ctx, &narURL)
		require.NoError(t, err)

		// 6. Verify narinfo_nar_files is still populated after migration (re-linked to new nar_file)
		var afterCount int

		err = db.DB().QueryRowContext(ctx,
			rebind("SELECT COUNT(*) FROM narinfo_nar_files WHERE narinfo_id = (SELECT id FROM narinfos WHERE hash = ?)"),
			entry.NarInfoHash).Scan(&afterCount)
		require.NoError(t, err)
		assert.Equal(t, 1, afterCount,
			"narinfo_nar_files must have exactly one entry after CDC migration")

		// 7. Verify the linked nar_file has compression=none and total_chunks > 0
		var linkedCompression string

		var linkedTotalChunks int64

		err = db.DB().QueryRowContext(ctx,
			rebind(`SELECT nf.compression, nf.total_chunks
				FROM nar_files nf
				INNER JOIN narinfo_nar_files nnf ON nf.id = nnf.nar_file_id
				WHERE nnf.narinfo_id = (SELECT id FROM narinfos WHERE hash = ?)`),
			entry.NarInfoHash).Scan(&linkedCompression, &linkedTotalChunks)
		require.NoError(t, err)
		assert.Equal(t, nar.CompressionTypeNone.String(), linkedCompression,
			"linked nar_file must have compression=none after CDC migration")
		assert.Positive(t, linkedTotalChunks,
			"linked nar_file must be fully chunked (total_chunks > 0)")
	}
}

// testCDCPutNarInfoNormalizesCompression verifies that PutNarInfo normalizes
// compression to "none" when CDC is enabled, regardless of the input compression type.
func testCDCPutNarInfoNormalizesCompression(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		c, db, _, dir, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Enable CDC
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
		require.NoError(t, err)

		compressions := []struct {
			name        string
			compression string
			narHash     string
		}{
			{"xz", "xz", "1s8p1kgdms8rmxkq24q51wc7zpn0aqcwgzvc473v9cii7z2qyxq0"},
			{"zstd", "zstd", "07kc6swib31psygpmwi8952lvywlpqn474059yxl7grwsvr6k0fj"},
			{"brotli", "br", "188g68hrjilbsjifcj70k8729zqhm9sl1q336vg5wxwzw0qp0sk4"},
			{"none stays none", "none", "14vg46h9nbbqgbrbszrqm48f0bgzj6c4q3wkkcjf6gp53g8b21gh"},
		}

		for _, tc := range compressions {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				hash := "putnarinfo-cdc-" + tc.name

				narURLStr := "nar/" + tc.narHash + ".nar"
				if ext := nar.CompressionTypeFromString(tc.compression).ToFileExtension(); ext != "" {
					narURLStr += "." + ext
				}

				niText := "StorePath: /nix/store/" + hash + "-test\n" +
					"URL: " + narURLStr + "\n" +
					"Compression: " + tc.compression + "\n" +
					"FileHash: sha256:" + tc.narHash + "\n" +
					"FileSize: 100\n" +
					"NarHash: sha256:" + tc.narHash + "\n" +
					"NarSize: 100\n"

				err := c.PutNarInfo(ctx, hash, io.NopCloser(strings.NewReader(niText)))
				require.NoError(t, err)

				// Verify narinfo in DB has Compression: none
				var compression, url string

				err = db.DB().QueryRowContext(ctx,
					rebind("SELECT compression, url FROM narinfos WHERE hash = ?"),
					hash).Scan(&compression, &url)
				require.NoError(t, err)
				assert.Equal(t, nar.CompressionTypeNone.String(), compression,
					"narinfo Compression should be normalized to 'none' for CDC")

				parsedURL, err := nar.ParseURL(url)
				require.NoError(t, err, "URL from database should be parsable")
				assert.Equal(
					t,
					nar.CompressionTypeNone,
					parsedURL.Compression,
					"URL from database should have no compression extension",
				)
			})
		}
	}
}

// testCDCPullNarInfoSetsFileSizeForCDC verifies that when CDC is enabled and
// a narinfo is pulled from upstream, the FileSize in the narinfo is set to
// NarSize (uncompressed size), not the upstream's compressed FileSize.
func testCDCPullNarInfoSetsFileSizeForCDC(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ts := testdata.NewTestServer(t, 40)
		t.Cleanup(ts.Close)

		c, db, _, dir, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Set up CDC
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
		require.NoError(t, err)

		// Set up upstream cache
		uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
			PublicKeys: testdata.PublicKeys(),
		})
		require.NoError(t, err)

		c.AddUpstreamCaches(newContext(), uc)

		// Wait for upstream to become available
		<-c.GetHealthChecker().Trigger()

		t.Run("upstream compressed FileSize != NarSize, CDC normalizes FileSize to 0 and FileHash to empty",
			func(t *testing.T) {
				// Nar2 has Compression: xz upstream with FileSize != NarSize (compressed vs uncompressed)
				ni, err := c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
				require.NoError(t, err)

				// Verify FileSize == 0 for CDC mode
				assert.Equal(t, uint64(0), ni.FileSize,
					"CDC mode should set FileSize == 0, not the compressed upstream size")

				// Verify FileHash == null for CDC mode (compressed upstream FileHash must be null)
				assert.Empty(t, ni.FileHash,
					"CDC mode should set FileHash == null, not the compressed upstream hash")

				// Also verify in the database
				var (
					fileSize          sql.NullInt64
					narSize           uint64
					fileHash, narHash sql.NullString
				)

				err = db.DB().QueryRowContext(context.Background(),
					rebind("SELECT file_size, nar_size, file_hash, nar_hash FROM narinfos WHERE hash = ?"),
					testdata.Nar2.NarInfoHash).Scan(&fileSize, &narSize, &fileHash, &narHash)
				require.NoError(t, err)
				assert.False(t, fileSize.Valid,
					"narinfo in DB should have FileSize == NULL for CDC")
				assert.Equal(t, sql.NullString{}, fileHash,
					"narinfo in DB should have FileHash == null for CDC")
			})
	}
}

// testCDCPutNarInfoSetsFileSizeForCDC verifies that when PutNarInfo is called
// with CDC enabled, the FileSize in the narinfo is set to NarSize, not kept
// from the input narinfo (which might be compressed).
func testCDCPutNarInfoSetsFileSizeForCDC(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		c, db, _, dir, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Enable CDC
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
		require.NoError(t, err)

		hash := "putnarinfo-cdc-filesize-test"
		narHash := "1s8p1kgdms8rmxkq24q51wc7zpn0aqcwgzvc473v9cii7z2qyxq0"

		// Create a narinfo with FileSize != NarSize (simulating upstream compression mismatch)
		niText := "StorePath: /nix/store/" + hash + "-test\n" +
			"URL: nar/" + narHash + ".nar.xz\n" +
			"Compression: xz\n" +
			"FileHash: sha256:" + narHash + "\n" +
			"FileSize: 5000\n" + // Compressed size (different from NarSize)
			"NarHash: sha256:" + narHash + "\n" +
			"NarSize: 10000\n" // Uncompressed size

		err = c.PutNarInfo(ctx, hash, io.NopCloser(strings.NewReader(niText)))
		require.NoError(t, err)

		// Verify narinfo in DB has FileSize == NULL and FileHash == null
		var (
			fileSize              sql.NullInt64
			narSize               uint64
			dbFileHash, dbNarHash sql.NullString
		)

		err = db.DB().QueryRowContext(ctx,
			rebind("SELECT file_size, nar_size, file_hash, nar_hash FROM narinfos WHERE hash = ?"),
			hash).Scan(&fileSize, &narSize, &dbFileHash, &dbNarHash)
		require.NoError(t, err)

		assert.Equal(t, uint64(10000), narSize, "NarSize should be 10000 from input")
		assert.False(t, fileSize.Valid,
			"CDC mode should set FileSize == NULL, not the upstream compressed size (5000)")
		assert.Equal(t, sql.NullString{}, dbFileHash,
			"CDC mode should set FileHash == null, not the upstream compressed hash")
	}
}

// testCDCPutNarInfoNoContextCanceled is a regression test for the bug where
// PutNarInfo called checkAndFixNarInfo with the HTTP request context, causing
// "context canceled" errors in background goroutines after the response was sent.
// When a NAR is stored as CDC chunks and then a narinfo is PUT (as happens with
// nix copy --to .../upload), checkAndFixNarInfo calls GetNar which spawns
// background goroutines. If these goroutines use the request context, they fail
// with "context canceled" once the HTTP response is sent and the context is canceled.
func testCDCPutNarInfoNoContextCanceled(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		c, _, _, dir, _, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Enable CDC
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
		require.NoError(t, err)

		// Store a NAR as CDC chunks first (simulates the upload NAR step)
		content := strings.Repeat("test nar content for regression test of context canceled bug ", 50)
		narHash := "1s8p1kgdms8rmxkq24q51wc7zpn0aqcwgzvc473v9cii7z2qyxq0"
		nu := nar.URL{Hash: narHash, Compression: nar.CompressionTypeNone}

		err = c.PutNar(context.Background(), nu, io.NopCloser(strings.NewReader(content)))
		require.NoError(t, err)

		// Set up a log buffer to capture error-level log lines.
		// We inject a zerolog writer into the context that PutNarInfo will use.
		var logBuf bytes.Buffer

		logger := zerolog.New(&logBuf).Level(zerolog.ErrorLevel)

		// Create a cancelable context simulating an HTTP request context.
		reqCtx, cancel := context.WithCancel(logger.WithContext(context.Background()))

		// Build a narinfo referencing the CDC-stored NAR
		niText := "StorePath: /nix/store/0amzzlz5w7ihknr59cn0q56pvp17bqqz-test-path\n" +
			"URL: nar/" + narHash + ".nar\n" +
			"Compression: none\n" +
			"FileHash: sha256:" + narHash + "\n" +
			"FileSize: 3000\n" +
			"NarHash: sha256:" + narHash + "\n" +
			"NarSize: 3000\n"

		// PutNarInfo with the cancelable context (simulates the HTTP handler)
		err = c.PutNarInfo(reqCtx, "0amzzlz5w7ihknr59cn0q56pvp17bqqz", io.NopCloser(strings.NewReader(niText)))
		require.NoError(t, err)

		// Cancel the context immediately after PutNarInfo returns — simulating
		// the HTTP server canceling the request context after sending the 204 response.
		cancel()

		// Give background goroutines time to run and potentially fail.
		time.Sleep(200 * time.Millisecond)

		// Check no "context canceled" errors were logged.
		assert.NotContains(t, logBuf.String(), "context canceled",
			"PutNarInfo should not trigger 'context canceled' errors"+
				"in background goroutines after the request context is canceled")
	}
}

// testCDCPutNarDoesNotOverwriteFileSizeOnceChunked verifies that once a NAR
// is fully chunked, subsequent PutNar calls (e.g., with a compressed version
// of the same NAR) do not overwrite the correct uncompressed FileSize in the DB.
func testCDCPutNarDoesNotOverwriteFileSizeOnceChunked(factory cacheFactory) func(*testing.T) {
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

		// 1. Store an uncompressed NAR
		content := "this is a test nar content that should be chunked"
		uncompressedSize := uint64(len(content))
		nu := nar.URL{Hash: "filesize-overwrite-test", Compression: nar.CompressionTypeNone}

		err = c.PutNar(ctx, nu, io.NopCloser(strings.NewReader(content)))
		require.NoError(t, err)

		// Verify FileSize in DB is uncompressed size
		narFile, err := db.GetNarFileByHashAndCompressionAndQuery(ctx, database.GetNarFileByHashAndCompressionAndQueryParams{
			Hash:        nu.Hash,
			Compression: nu.Compression.String(),
			Query:       "",
		})
		require.NoError(t, err)
		assert.Equal(t, uncompressedSize, narFile.FileSize)
		assert.Positive(t, narFile.TotalChunks)

		// 2. Try to store the same NAR again but as a compressed NAR (simulated)
		// We'll use a smaller size to simulate compression
		compressedContent := "smaller"
		compressedSize := uint64(len(compressedContent))
		require.Less(t, compressedSize, uncompressedSize)

		// Create a new reader with the "compressed" content
		// We still use CompressionTypeNone for the nu because the bug is in how
		// findOrCreateNarFileForCDC handles the input fileSize regardless of compression normalization
		// which happens later. Actually, normalize happens BEFORE findOrCreateNarFileForCDC.
		// Let's re-read storeNarWithCDC logic.

		// In storeNarWithCDC:
		// fileSize := uint64(fi.Size())
		// originalCompression := narURL.Compression
		// narURL.Compression = nar.CompressionTypeNone
		// narFileID, err := c.findOrCreateNarFileForCDC(ctx, narURL, fileSize)

		// So if we pass a compressed NAR, it will be normalized to CompressionTypeNone
		// but fileSize will be the compressed file size.

		nuCompressed := nu
		nuCompressed.Compression = nar.CompressionTypeZstd

		// Use a real compressed reader to be more realistic
		var buf bytes.Buffer

		zw := zstd.NewPooledWriter(&buf)
		_, err = zw.Write([]byte(content))
		require.NoError(t, err)
		require.NoError(t, zw.Close())

		//nolint:gosec // G115: buf.Len() is non-negative
		realCompressedSize := uint64(buf.Len())
		require.NotEqual(t, uncompressedSize, realCompressedSize)

		err = c.PutNar(ctx, nuCompressed, io.NopCloser(&buf))
		require.NoError(t, err)

		// 3. Verify FileSize in DB IS STILL the uncompressed size
		params := database.GetNarFileByHashAndCompressionAndQueryParams{
			Hash:        nu.Hash,
			Compression: nar.CompressionTypeNone.String(),
			Query:       "",
		}
		narFileAfter, err := db.GetNarFileByHashAndCompressionAndQuery(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, uncompressedSize, narFileAfter.FileSize,
			"FileSize should NOT have been overwritten by compressed size")
	}
}

// testCDCMigrateNarToChunksCompressionNormalizationIsAtomic verifies the invariants that
// must hold after MigrateNarToChunks when compression is normalized (xz → none):
//
//  1. No nar_files record with the original compression (xz) exists for the hash.
//  2. Exactly one nar_files record with compression=none exists.
//  3. narinfo_nar_files has exactly one link pointing to the none-compression nar_file.
//
// These invariants depend on DeleteNarFileByHash and relinkNarInfosToNarFile being
// executed atomically. If they are not, a process kill between the two operations
// leaves narinfos with no nar_file link (narinfo_nar_files empty) — a dead-end state
// that cannot self-heal.
func testCDCMigrateNarToChunksCompressionNormalizationIsAtomic(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		c, db, _, dir, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// 1. Store a NAR as a whole file with xz compression (CDC disabled).
		entry := testdata.Nar1
		narURL := nar.URL{Hash: entry.NarHash, Compression: entry.NarCompression}

		err := c.PutNar(ctx, narURL, io.NopCloser(strings.NewReader(entry.NarText)))
		require.NoError(t, err)

		// 2. Store the narinfo referencing this NAR with xz compression.
		err = c.PutNarInfo(ctx, entry.NarInfoHash, io.NopCloser(strings.NewReader(entry.NarInfoText)))
		require.NoError(t, err)

		// 3. Verify the pre-migration state: one nar_files row with xz compression.
		var xzCountBefore int

		err = db.DB().QueryRowContext(ctx,
			rebind("SELECT COUNT(*) FROM nar_files WHERE hash = ? AND compression = 'xz'"),
			entry.NarHash).Scan(&xzCountBefore)
		require.NoError(t, err)
		require.Equal(t, 1, xzCountBefore, "should have exactly one xz nar_files record before migration")

		// 4. Enable CDC.
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
		require.NoError(t, err)

		// 5. Migrate the NAR to chunks (triggers compression normalization: xz → none).
		err = c.MigrateNarToChunks(ctx, &narURL)
		require.NoError(t, err)

		// Invariant 1: No nar_files record with the old compression (xz) must exist.
		var xzCountAfter int

		err = db.DB().QueryRowContext(ctx,
			rebind("SELECT COUNT(*) FROM nar_files WHERE hash = ? AND compression = 'xz'"),
			entry.NarHash).Scan(&xzCountAfter)
		require.NoError(t, err)
		assert.Equal(t, 0, xzCountAfter,
			"old nar_files record with xz compression must be deleted after CDC migration")

		// Invariant 2: Exactly one nar_files record with compression=none must exist.
		var noneCount int

		err = db.DB().QueryRowContext(ctx,
			rebind("SELECT COUNT(*) FROM nar_files WHERE hash = ? AND compression = 'none'"),
			entry.NarHash).Scan(&noneCount)
		require.NoError(t, err)
		assert.Equal(t, 1, noneCount,
			"exactly one nar_files record with compression=none must exist after CDC migration")

		// Invariant 3: narinfo_nar_files must have exactly one link, pointing to the none-compression nar_file.
		// If DeleteNarFileByHash and relinkNarInfosToNarFile are not atomic, a crash between them
		// leaves this count at 0 — narinfos orphaned with no nar_file link.
		var linkCount int

		err = db.DB().QueryRowContext(ctx, rebind(`
			SELECT COUNT(*)
			FROM narinfo_nar_files nnf
			INNER JOIN nar_files nf ON nf.id = nnf.nar_file_id
			WHERE nnf.narinfo_id = (SELECT id FROM narinfos WHERE hash = ?)
			AND nf.compression = 'none'
		`), entry.NarInfoHash).Scan(&linkCount)
		require.NoError(t, err)
		assert.Equal(t, 1, linkCount,
			"narinfo_nar_files must link to the none-compression nar_file after CDC migration "+
				"(broken if DeleteNarFileByHash and relinkNarInfosToNarFile are not atomic)")
	}
}

// testCDCMigrateNarToChunksRecoversFromPartialChunking verifies that MigrateNarToChunks
// recovers from a previous partial chunking attempt. Before this fix, if a process was
// killed mid-chunking (leaving nar_files.total_chunks=0 but some chunks already written),
// findOrCreateNarFileForCDC would see len(chunks)>0 and return ErrAlreadyExists, causing
// the NAR to be permanently stuck — never fully chunked, never recoverable.
//
// The fix uses chunking_started_at to track lock state: if it was set more than the TTL
// ago, the partial chunks are cleaned up and chunking restarts from scratch.
func testCDCMigrateNarToChunksRecoversFromPartialChunking(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		c, db, _, dir, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// 1. Store a NAR as a whole file (CDC disabled) with xz compression.
		entry := testdata.Nar1
		narURL := nar.URL{Hash: entry.NarHash, Compression: entry.NarCompression}

		err := c.PutNar(ctx, narURL, io.NopCloser(strings.NewReader(entry.NarText)))
		require.NoError(t, err)

		err = c.PutNarInfo(ctx, entry.NarInfoHash, io.NopCloser(strings.NewReader(entry.NarInfoText)))
		require.NoError(t, err)

		// 2. Enable CDC.
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
		require.NoError(t, err)

		// 3. Simulate a partial chunking state: create the nar_files record
		// with total_chunks=0 and set chunking_started_at to a stale past time.
		noneURL := nar.URL{Hash: entry.NarHash, Compression: nar.CompressionTypeNone}
		narFile, err := db.CreateNarFile(ctx, database.CreateNarFileParams{
			Hash:        noneURL.Hash,
			Compression: noneURL.Compression.String(),
			Query:       noneURL.Query.Encode(),
			FileSize:    0,
			TotalChunks: 0,
		})
		require.NoError(t, err)

		// Insert a fake partial chunk to simulate in-progress state.
		// Use a valid 52-character Nix base32 hash
		fakeChunk, err := db.CreateChunk(ctx, database.CreateChunkParams{
			Hash:           "fakehashxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			Size:           512,
			CompressedSize: 256,
		})
		require.NoError(t, err)

		err = db.LinkNarFileToChunk(ctx, database.LinkNarFileToChunkParams{
			NarFileID:  narFile.ID,
			ChunkID:    fakeChunk.ID,
			ChunkIndex: 0,
		})
		require.NoError(t, err)

		// Mark chunking as started (sets chunking_started_at = CURRENT_TIMESTAMP).
		err = db.SetNarFileChunkingStarted(ctx, narFile.ID)
		require.NoError(t, err)

		// Move chunking_started_at to 2 hours in the past to simulate a stale lock.
		_, err = db.DB().ExecContext(ctx,
			rebind("UPDATE nar_files SET chunking_started_at = ? WHERE id = ?"),
			time.Now().Add(-2*time.Hour),
			narFile.ID,
		)
		require.NoError(t, err)

		// 4. Verify: at this point, HasNarInChunks returns false (total_chunks=0)
		// but there ARE partial chunks in the DB — the stuck state.
		hasChunks, err := c.HasNarInChunks(ctx, noneURL)
		require.NoError(t, err)
		assert.False(t, hasChunks, "NAR should not be considered fully chunked (total_chunks=0)")

		chunks, err := db.GetChunksByNarFileID(ctx, narFile.ID)
		require.NoError(t, err)
		assert.Len(t, chunks, 1, "should have exactly 1 partial/fake chunk before recovery")

		// 5. Call MigrateNarToChunks — should recover from partial state.
		// Without the fix, this returns an error because findOrCreateNarFileForCDC
		// sees chunks>0 and returns ErrAlreadyExists.
		err = c.MigrateNarToChunks(ctx, &narURL)
		require.NoError(t, err, "MigrateNarToChunks must succeed even after partial chunking")

		// 6. Verify the NAR is now fully chunked.
		noneURL = nar.URL{Hash: entry.NarHash, Compression: nar.CompressionTypeNone}

		hasChunks, err = c.HasNarInChunks(ctx, noneURL)
		require.NoError(t, err)
		assert.True(t, hasChunks, "NAR must be fully chunked after recovery")

		// 7. Verify the partial nar_file_chunks links are gone (replaced by real ones).
		// The immediate cleanup during stale lock recovery also removes the orphaned
		// chunks record from the DB (not just the junction table entry).
		var fakeChunkLinkCount int

		err = db.DB().QueryRowContext(ctx,
			rebind(`SELECT COUNT(*) FROM nar_file_chunks nfc
				INNER JOIN chunks c ON c.id = nfc.chunk_id
				WHERE c.hash = ?`),
			"fakehashxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		).Scan(&fakeChunkLinkCount)
		require.NoError(t, err)
		assert.Zero(t, fakeChunkLinkCount,
			"partial nar_file_chunks links for fake chunk must be removed after recovery")

		// Verify the orphaned chunk DB record itself is also immediately deleted
		// (not left for the background GC).
		_, fakeChunkErr := db.GetChunkByHash(ctx, "fakehashxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
		assert.True(t, database.IsNotFoundError(fakeChunkErr),
			"orphaned fake chunk DB record must be deleted immediately during stale lock cleanup")
	}
}

// testCDCMigrateNarToChunksDeletesOriginalFile verifies that after MigrateNarToChunks
// completes successfully, the original whole-file NAR is deleted from narStore.
// Without this fix, storage files accumulate after CDC migration even though NARs are
// fully chunked and the whole-file is no longer referenced.
func testCDCMigrateNarToChunksDeletesOriginalFile(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		c, _, _, dir, _, cleanup := factory(t)
		t.Cleanup(cleanup)

		// 1. Store a NAR as a whole file (CDC disabled) with xz compression.
		entry := testdata.Nar1
		originalNarURL := nar.URL{Hash: entry.NarHash, Compression: entry.NarCompression}
		narURL := originalNarURL // MigrateNarToChunks mutates narURL.Compression in-place

		err := c.PutNar(ctx, originalNarURL, io.NopCloser(strings.NewReader(entry.NarText)))
		require.NoError(t, err)

		// 2. Store a narinfo referencing this NAR.
		err = c.PutNarInfo(ctx, entry.NarInfoHash, io.NopCloser(strings.NewReader(entry.NarInfoText)))
		require.NoError(t, err)

		// 3. Verify the file exists in narStore before migration.
		assert.True(t, c.HasNarInStore(ctx, originalNarURL),
			"NAR should exist as a whole file in narStore before CDC migration")

		// 4. Enable CDC.
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
		require.NoError(t, err)

		// 5. Migrate the NAR to chunks.
		// NOTE: MigrateNarToChunks modifies narURL.Compression in-place (sets it to none).
		// We preserve originalNarURL to check the pre-migration storage path after migration.
		err = c.MigrateNarToChunks(ctx, &narURL)
		require.NoError(t, err)

		// 6. Verify chunks exist (sanity check).
		hasChunks, err := c.HasNarInChunks(ctx, narURL)
		require.NoError(t, err)
		assert.True(t, hasChunks, "NAR should be fully chunked after migration")

		// 7. Verify the original whole-file NAR is GONE from narStore.
		// We check the original URL (xz) — this is what was written to storage before migration.
		assert.False(t, c.HasNarInStore(ctx, originalNarURL),
			"original whole-file NAR (xz) must be deleted from narStore after CDC migration")
	}
}

// testCDCMigrateNarToChunksHealsStaleNarInfoURLOnSecondCall verifies that
// MigrateNarToChunks is fully idempotent: if the process previously crashed
// between storeNarWithCDC (which commits chunks and relinks narinfos) and the
// subsequent UpdateNarInfoCompressionAndURL call, calling MigrateNarToChunks
// again must heal the stale narinfo URL and delete the original whole-file NAR.
//
// Simulated crash state (after first partial run):
//   - nar_file record: compression=none, total_chunks=N (chunking complete)
//   - narinfo.url still has the old URL (e.g., hash.nar.xz)
//   - Original whole-file NAR still in narStore (deletion never ran)
func testCDCMigrateNarToChunksHealsStaleNarInfoURLOnSecondCall(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		c, db, localStore, dir, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		entry := testdata.Nar1
		narURL := nar.URL{Hash: entry.NarHash, Compression: entry.NarCompression}

		// 1. Store NAR as whole file (no CDC) and narinfo.
		err := c.PutNar(ctx, narURL, io.NopCloser(strings.NewReader(entry.NarText)))
		require.NoError(t, err)

		err = c.PutNarInfo(ctx, entry.NarInfoHash, io.NopCloser(strings.NewReader(entry.NarInfoText)))
		require.NoError(t, err)

		// 2. Enable CDC.
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
		require.NoError(t, err)

		// 3. Run MigrateNarToChunks successfully (first migration).
		err = c.MigrateNarToChunks(ctx, &narURL)
		require.NoError(t, err, "first migration should succeed")

		// Verify migration completed (sanity check).
		var url, compression string

		err = db.DB().QueryRowContext(ctx,
			rebind("SELECT url, compression FROM narinfos WHERE hash = ?"),
			entry.NarInfoHash).Scan(&url, &compression)
		require.NoError(t, err)
		assert.Equal(t, nar.CompressionTypeNone.String(), compression,
			"narinfo compression should be none after first migration")

		// 4. Simulate a crash between storeNarWithCDC and UpdateNarInfoCompressionAndURL:
		//    - Reset the narinfo URL back to the original xz URL in the DB.
		//    - Put the original whole-file NAR back in narStore.
		oldURL := nar.URL{Hash: entry.NarHash, Compression: nar.CompressionTypeXz}.String()
		_, err = db.DB().ExecContext(ctx,
			rebind("UPDATE narinfos SET url = ?, compression = ? WHERE hash = ?"),
			oldURL, "xz", entry.NarInfoHash)
		require.NoError(t, err, "resetting narinfo URL to simulate crash state")

		_, err = localStore.PutNar(ctx, nar.URL{Hash: entry.NarHash, Compression: nar.CompressionTypeXz},
			io.NopCloser(strings.NewReader(entry.NarText)))
		require.NoError(t, err, "putting whole-file NAR back to simulate crash state")

		// Verify the crash state is set up correctly.
		var staleURL string

		err = db.DB().QueryRowContext(ctx,
			rebind("SELECT url FROM narinfos WHERE hash = ?"),
			entry.NarInfoHash).Scan(&staleURL)
		require.NoError(t, err)
		assert.Contains(t, staleURL, ".xz", "narinfo URL should be stale (xz) before second migration")
		assert.True(t, c.HasNarInStore(ctx, nar.URL{Hash: entry.NarHash, Compression: nar.CompressionTypeXz}),
			"original whole-file NAR should be back in narStore before second migration")

		// 5. Call MigrateNarToChunks again on the original xz URL.
		//    Chunks already exist (total_chunks > 0), so HasNarInChunks returns true.
		//    The fix: instead of returning immediately, perform the cleanup steps.
		xzNarURL := nar.URL{Hash: entry.NarHash, Compression: nar.CompressionTypeXz}
		err = c.MigrateNarToChunks(ctx, &xzNarURL)
		// After the fix this must succeed (either nil or ErrNarAlreadyChunked is acceptable —
		// what matters is that the side effects are applied).
		if err != nil {
			require.ErrorIs(t, err, cache.ErrNarAlreadyChunked, "only ErrNarAlreadyChunked is acceptable")
		}

		// 6. Verify the narinfo URL is updated to the none URL (healing completed).
		var healedURL, healedCompression string

		err = db.DB().QueryRowContext(ctx,
			rebind("SELECT url, compression FROM narinfos WHERE hash = ?"),
			entry.NarInfoHash).Scan(&healedURL, &healedCompression)
		require.NoError(t, err)
		assert.Equal(t, nar.CompressionTypeNone.String(), healedCompression,
			"narinfo compression must be healed to none by second MigrateNarToChunks call")
		assert.NotContains(t, healedURL, ".xz",
			"narinfo URL must not contain .xz after healing")

		// 7. Verify the original whole-file NAR was deleted from narStore.
		assert.False(t, c.HasNarInStore(ctx, nar.URL{Hash: entry.NarHash, Compression: nar.CompressionTypeXz}),
			"original whole-file NAR must be deleted from narStore after healing")
	}
}

// testCDCStaleLockCleansUpChunkFiles verifies that when a stale CDC chunking lock is
// detected and cleaned up, the orphaned chunk FILES in the chunkStore are immediately
// deleted (in addition to the nar_file_chunks junction table entries and chunks DB records).
// Without this fix, orphaned chunk files accumulate on disk until the next GC (RunLRU) run.
func testCDCStaleLockCleansUpChunkFiles(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		c, db, _, dir, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// 1. Store a NAR as a whole file (CDC disabled) with xz compression.
		entry := testdata.Nar1
		narURL := nar.URL{Hash: entry.NarHash, Compression: entry.NarCompression}

		err := c.PutNar(ctx, narURL, io.NopCloser(strings.NewReader(entry.NarText)))
		require.NoError(t, err)

		err = c.PutNarInfo(ctx, entry.NarInfoHash, io.NopCloser(strings.NewReader(entry.NarInfoText)))
		require.NoError(t, err)

		// 2. Enable CDC.
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
		require.NoError(t, err)

		// 3. Simulate a partial chunking state with a REAL physical chunk file.
		noneURL := nar.URL{Hash: entry.NarHash, Compression: nar.CompressionTypeNone}

		narFile, err := db.CreateNarFile(ctx, database.CreateNarFileParams{
			Hash:        noneURL.Hash,
			Compression: noneURL.Compression.String(),
			Query:       noneURL.Query.Encode(),
			FileSize:    0,
			TotalChunks: 0,
		})
		require.NoError(t, err)

		// Write a real physical chunk file to chunkStore.
		fakeChunkHash := testhelper.MustRandBase32NarHash()
		fakeChunkData := []byte("fake partial chunk data that simulates a mid-chunking crash")
		_, _, err = chunkStore.PutChunk(ctx, fakeChunkHash, fakeChunkData)
		require.NoError(t, err)

		// Create the matching DB record and junction entry.
		fakeChunk, err := db.CreateChunk(ctx, database.CreateChunkParams{
			Hash:           fakeChunkHash,
			Size:           64,
			CompressedSize: 64,
		})
		require.NoError(t, err)

		err = db.LinkNarFileToChunk(ctx, database.LinkNarFileToChunkParams{
			NarFileID:  narFile.ID,
			ChunkID:    fakeChunk.ID,
			ChunkIndex: 0,
		})
		require.NoError(t, err)

		// Mark chunking as started and move the timestamp 2 hours into the past.
		err = db.SetNarFileChunkingStarted(ctx, narFile.ID)
		require.NoError(t, err)

		_, err = db.DB().ExecContext(ctx,
			rebind("UPDATE nar_files SET chunking_started_at = ? WHERE id = ?"),
			time.Now().Add(-2*time.Hour),
			narFile.ID,
		)
		require.NoError(t, err)

		// 4. Precondition: chunk file and DB record must exist before recovery.
		hasChunk, err := chunkStore.HasChunk(ctx, fakeChunkHash)
		require.NoError(t, err)
		assert.True(t, hasChunk, "fake chunk file must exist in chunkStore before stale lock cleanup")

		_, err = db.GetChunkByHash(ctx, fakeChunkHash)
		require.NoError(t, err, "fake chunk DB record must exist before stale lock cleanup")

		// 5. Call MigrateNarToChunks — stale lock is detected and cleaned up.
		err = c.MigrateNarToChunks(ctx, &narURL)
		require.NoError(t, err, "MigrateNarToChunks must succeed after stale lock cleanup")

		// 6. Verify the NAR is now fully chunked.
		hasChunks, err := c.HasNarInChunks(ctx, noneURL)
		require.NoError(t, err)
		assert.True(t, hasChunks, "NAR must be fully chunked after stale lock recovery")

		// 7. Verify the orphaned chunk FILE was immediately deleted from chunkStore.
		hasChunk, err = chunkStore.HasChunk(ctx, fakeChunkHash)
		require.NoError(t, err)
		assert.False(t, hasChunk,
			"orphaned chunk file must be immediately deleted during stale lock cleanup, not left for GC")

		// 8. Verify the orphaned chunk DB record was also immediately deleted.
		_, fakeChunkErr := db.GetChunkByHash(ctx, fakeChunkHash)
		assert.True(t, database.IsNotFoundError(fakeChunkErr),
			"orphaned chunk DB record must be immediately deleted during stale lock cleanup, not left for GC")
	}
}

// testCDCFirstPullCompletesBeforeChunking verifies two properties of CDC first pull:
//
//  1. GetNar returns all NAR bytes to the client BEFORE CDC chunking finishes. Without
//     this fix the HTTP connection would stay open for the full duration of CDC chunking
//     (~18 s for large NARs). The fix (Bug 1) signals ds.stored immediately after the
//     nar_file DB record is created.
//
//  2. When GetNar piggybacks on a download started by prePullNarInfo (which always fetches
//     the upstream compression format for better TTFB), the streaming goroutine correctly
//     decompresses the compressed temp file so the client receives uncompressed bytes
//     (Bug 2).
func testCDCFirstPullCompletesBeforeChunking(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ts := testdata.NewTestServer(t, 40)
		t.Cleanup(ts.Close)

		c, _, _, dir, _, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Set up CDC with small chunk sizes for fast chunking.
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
		require.NoError(t, err)

		// Inject a slow chunker that adds a significant delay before producing chunks.
		// This simulates chunking a large NAR (e.g., 180 MB taking ~18 s).
		const chunkingDelay = 2 * time.Second

		realChunker, err := chunker.NewCDCChunker(1024, 4096, 8192)
		require.NoError(t, err)

		c.SetChunker(&slowChunker{real: realChunker, delay: chunkingDelay})

		// Create real xz-compressed NAR content.
		// The test server will serve this at Nar2's NAR URL so that the streaming
		// goroutine can verify decompression correctness (Bug 2).
		originalContent := testhelper.MustRandString(50160)
		xzContent := compressXz(t, originalContent)

		// Override the default Nar2 NAR response with real xz-compressed content.
		// The test server normally serves Nar2.NarText (random bytes pretending to
		// be xz). We replace it so the xz decompressor can actually decompress it.
		nar2NARPath := "/nar/" + testdata.Nar2.NarHash + ".nar.xz"
		handlerIdx := ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != nar2NARPath {
				return false
			}

			_, _ = io.WriteString(w, xzContent)

			return true
		})

		t.Cleanup(func() { ts.RemoveMaybeHandler(handlerIdx) })

		// Set up upstream cache.
		uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
			PublicKeys: testdata.PublicKeys(),
		})
		require.NoError(t, err)

		c.AddUpstreamCaches(newContext(), uc)

		// Wait for upstream to become available.
		<-c.GetHealthChecker().Trigger()

		// Pull the narinfo. This triggers prePullNar in the background which downloads
		// Nar2's xz-compressed NAR (real xz data from the MaybeHandler above).
		// prePullNarInfo normalizes the narinfo URL to compression=none for CDC.
		_, err = c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
		require.NoError(t, err)

		// Request the NAR using the CDC-normalized URL (no compression extension).
		// GetNar piggybacks on the active xz download started by prePullNarInfo.
		narURL := nar.URL{Hash: testdata.Nar2.NarHash, Compression: nar.CompressionTypeNone}

		start := time.Now()

		_, rc, err := c.GetNar(context.Background(), narURL)
		require.NoError(t, err)

		defer rc.Close()

		body, err := io.ReadAll(rc)
		require.NoError(t, err)

		elapsed := time.Since(start)

		// Bug 1 assertion: reading all NAR bytes must complete BEFORE the slow chunker
		// delay expires. If ds.stored is correctly signaled right after the nar_file DB
		// record is created (not after chunking finishes), elapsed will be well under
		// chunkingDelay.
		assert.Less(t, elapsed, chunkingDelay,
			"GetNar response must complete before CDC chunking finishes (elapsed: %s, chunking delay: %s)",
			elapsed, chunkingDelay)

		// Bug 2 assertion: the client must receive the ORIGINAL uncompressed content.
		// Even though prePullNarInfo downloaded xz-compressed bytes, the streaming
		// goroutine must decompress them before sending to the client.
		assert.Equal(t, originalContent, string(body),
			"NAR body must be the decompressed original content, not the raw xz bytes")
	}
}

// testCDCDisabledWithChunkedNARsInDB verifies that disabling CDC after NARs have been
// stored as chunks does not cause a nil pointer panic. When CDC is disabled, GetNar must
// not attempt to stream from chunks (which would dereference a nil chunkStore).
func testCDCDisabledWithChunkedNARsInDB(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		ts := testdata.NewTestServer(t, 40)
		t.Cleanup(ts.Close)

		c, db, _, dir, _, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Step 1: Enable CDC and store a NAR as chunks.
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
		require.NoError(t, err)

		content := "this is a test nar content that should be chunked by fastcdc algorithm"
		nu := nar.URL{Hash: "testnar-cdc-disabled", Compression: nar.CompressionTypeNone}

		err = c.PutNar(ctx, nu, io.NopCloser(strings.NewReader(content)))
		require.NoError(t, err)

		// Verify that DB has chunked NAR records.
		count, err := db.GetChunkCount(ctx)
		require.NoError(t, err)
		require.Positive(t, count, "DB should have chunk records after PutNar with CDC enabled")

		// Verify that HasNarInChunks returns true with CDC enabled.
		hasChunks, err := c.HasNarInChunks(ctx, nu)
		require.NoError(t, err)
		require.True(t, hasChunks, "HasNarInChunks should return true while CDC is enabled")

		// Step 2: Disable CDC (simulates config change or deployment rollback).
		// The DB still has total_chunks > 0 for the NAR we stored above.
		err = c.SetCDCConfiguration(false, 0, 0, 0)
		require.NoError(t, err)

		// Step 3: HasNarInChunks must return false when CDC is disabled,
		// so the system does not try the chunk path.
		hasChunks, err = c.HasNarInChunks(ctx, nu)
		require.NoError(t, err)
		assert.False(t, hasChunks, "HasNarInChunks should return false when CDC is disabled")
	}
}
