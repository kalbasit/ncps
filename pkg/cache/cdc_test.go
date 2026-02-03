package cache_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		nuBlob := nar.URL{Hash: "blob1", Compression: nar.CompressionTypeNone}
		require.NoError(t, c.PutNar(ctx, nuBlob, io.NopCloser(strings.NewReader(blobContent))))

		// 2. Store chunks with CDC enabled
		require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

		chunkContent := "chunked content"
		nuChunk := nar.URL{Hash: "chunk1", Compression: nar.CompressionTypeNone}
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
