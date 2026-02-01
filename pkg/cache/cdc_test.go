package cache_test

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
)

func TestCDC(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, db, _, dir, cleanup := setupTestCache(t)
	defer cleanup()

	// Initialize chunk store
	chunkStoreDir := filepath.Join(dir, "chunks-store")
	chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
	require.NoError(t, err)

	c.SetChunkStore(chunkStore)
	err = c.SetCDCConfiguration(true, 1024, 4096, 8192) // Small sizes for testing
	require.NoError(t, err)

	t.Run("Put and Get with CDC", func(t *testing.T) {
		content := "this is a test nar content that should be chunked by fastcdc algorithm"
		nu := nar.URL{Hash: "testnar1", Compression: nar.CompressionTypeNone}

		r := io.NopCloser(strings.NewReader(content))
		err := c.PutNar(ctx, nu, r)
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
	})

	t.Run("Deduplication", func(t *testing.T) {
		content := "common content shared between two nars"

		nu1 := nar.URL{Hash: "dedup1", Compression: nar.CompressionTypeNone}
		err := c.PutNar(ctx, nu1, io.NopCloser(strings.NewReader(content)))
		require.NoError(t, err)

		count1, _ := db.GetChunkCount(ctx)

		nu2 := nar.URL{Hash: "dedup2", Compression: nar.CompressionTypeNone}
		err = c.PutNar(ctx, nu2, io.NopCloser(strings.NewReader(content)))
		require.NoError(t, err)

		count2, _ := db.GetChunkCount(ctx)

		assert.Equal(t, count1, count2, "no new chunks should be created for duplicate content")
	})

	t.Run("Mixed Mode", func(t *testing.T) {
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
	})
}
