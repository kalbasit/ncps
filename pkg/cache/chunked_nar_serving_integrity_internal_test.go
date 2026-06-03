package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// TestGetNarFromChunks_MidChunkingPartialLinksNotFalse404 guards the HA-safety
// invariant: a NAR that is legitimately mid-chunking (total_chunks = 0,
// chunking_started_at set) with only a partial set of junction links — e.g. a
// concurrent replica is still writing chunks — MUST NOT be resolved to a
// synchronous storage.ErrNotFound by the completed-path completeness check. The
// check is gated on total_chunks > 0; total_chunks is the completion latch, so a
// total_chunks = 0 row with fewer links is in-progress, not corrupt. The
// progressive path must still be taken (reader handed back, no synchronous error).
func TestGetNarFromChunks_MidChunkingPartialLinksNotFalse404(t *testing.T) {
	t.Parallel()

	c, ctx := newCDCCacheForStreaming(t)

	// Fail fast on the progressive wait so the test does not block on a chunk
	// that never arrives.
	c.SetChunkWaitTimeout(200 * time.Millisecond)

	nf, err := c.dbClient.Ent().NarFile.Create().
		SetHash(testdata.Nar1.NarHash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(12345).
		SetTotalChunks(0).
		SetChunkingStartedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	// Seed one partial chunk link (mid-chunking: some links written, not all).
	ch, err := c.dbClient.Ent().Chunk.Create().
		SetHash(testhelper.MustRandBase32NarHash()).
		SetSize(64).
		SetCompressedSize(64).
		Save(ctx)
	require.NoError(t, err)

	_, err = c.dbClient.Ent().NarFileChunk.Create().
		SetNarFileID(nf.ID).
		SetChunkID(ch.ID).
		SetChunkIndex(0).
		Save(ctx)
	require.NoError(t, err)

	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeNone}

	// The completeness guard must NOT fire: getNarFromChunks returns a reader (the
	// progressive path), not a synchronous ErrNotFound.
	_, rc, err := c.getNarFromChunks(ctx, &narURL)
	require.NoError(t, err,
		"mid-chunking (total_chunks=0) must take the progressive path, not be 404'd by the completeness check")

	t.Cleanup(func() { _ = rc.Close() })
}
