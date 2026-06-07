package cache_test

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entnarfile "github.com/kalbasit/ncps/ent/narfile"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
)

// testServeCompletedNarMissingLinkReturns404 reproduces the production
// "expected N chunks but got M" truncated-200 failure. A completed chunked NAR
// (total_chunks = N > 0) loses one nar_file_chunks junction link — exactly what
// the chunks(id) ON DELETE CASCADE FK does when a shared chunk row is deleted,
// without resetting total_chunks. The serve fast path must detect this BEFORE
// committing the response and return storage.ErrNotFound (→ HTTP 404 → upstream
// fallback), never hand back a reader that truncates a 200 mid-stream.
func testServeCompletedNarMissingLinkReturns404(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		c, dbClient, _, dir, _, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Enable CDC and store the NAR as chunks (consistent total_chunks + links + blobs).
		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

		// Large, varied content so the chunker produces several chunks; deleting a
		// single junction link then leaves a genuine gap (total_chunks > links).
		multiChunkContent := strings.Repeat("ncps-chunked-nar-serving-integrity-regression ", 400)

		nu := nar.URL{Hash: "missinglinknar", Compression: nar.CompressionTypeNone}
		require.NoError(t, c.PutNar(ctx, nu, io.NopCloser(strings.NewReader(multiChunkContent))))

		// Confirm the NAR is fully chunked into more than one chunk.
		nf, err := dbClient.Ent().NarFile.Query().
			Where(entnarfile.HashEQ(nu.Hash)).
			Only(ctx)
		require.NoError(t, err)
		require.Greater(t, nf.TotalChunks, int64(1), "test needs a multi-chunk NAR")

		// Simulate the cascade loss: drop exactly one junction link, leaving
		// total_chunks unchanged. Now links == total_chunks - 1.
		dropLastChunkLink(ctx, t, dbClient, nf.ID)

		// GetNar must fail synchronously with ErrNotFound, before returning a reader
		// that would truncate a committed 200.
		_, _, rc, err := c.GetNar(ctx, nu)
		if rc != nil {
			_ = rc.Close()
		}

		require.Error(t, err, "GetNar must not hand back a reader for an un-reassemblable NAR")
		assert.ErrorIs(t, err, storage.ErrNotFound,
			"an un-reassemblable completed chunked NAR must resolve to ErrNotFound (HTTP 404 → upstream fallback)")
	}
}
