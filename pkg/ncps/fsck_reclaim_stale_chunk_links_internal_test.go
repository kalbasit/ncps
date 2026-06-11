package ncps

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entchunk "github.com/kalbasit/ncps/ent/chunk"
	entnarfilechunk "github.com/kalbasit/ncps/ent/narfilechunk"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/testhelper"
)

// TestReclaimStaleChunkLinks verifies the orphaned-chunk-residue cleanup
// (issue #1392 follow-up): chunks stranded behind stale nar_file_chunks links to
// dechunked (total_chunks=0) nar_files are reclaimed, while chunked NARs and any
// chunk still shared with a chunked NAR are retained. Idempotent.
func TestReclaimStaleChunkLinks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	dbClient := newCDCModeTestDB(t)

	chunkStore, err := chunk.NewLocalStore(filepath.Join(t.TempDir(), "chunks-store"))
	require.NoError(t, err)

	// Dechunked nar_file (total_chunks=0): its links are stale.
	dechunkedNF, err := dbClient.Ent().NarFile.Create().
		SetHash("dechunkednarfilehashaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa").
		SetCompression(nar.CompressionTypeNone.String()).SetQuery("").
		SetFileSize(10).SetTotalChunks(0).
		Save(ctx)
	require.NoError(t, err)

	// Healthy chunked nar_file (total_chunks>0): keep its links/chunks.
	chunkedNF, err := dbClient.Ent().NarFile.Create().
		SetHash("chunkednarfilehashbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb").
		SetCompression(nar.CompressionTypeNone.String()).SetQuery("").
		SetFileSize(20).SetTotalChunks(2).
		Save(ctx)
	require.NoError(t, err)

	// Mid-chunking nar_file (total_chunks=0 but chunking_started_at set): its links
	// are being written, NOT stale, and must be preserved by the not-actively-chunking
	// guard.
	midChunkingNF, err := dbClient.Ent().NarFile.Create().
		SetHash("midchunkingnarfilehashcccccccccccccccccccccccccccccc").
		SetCompression(nar.CompressionTypeNone.String()).SetQuery("").
		SetFileSize(15).SetTotalChunks(0).SetChunkingStartedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	mkChunk := func() string {
		h := testhelper.MustRandBase32NarHash()
		_, _, perr := chunkStore.PutChunk(ctx, h, []byte("chunk-"+h))
		require.NoError(t, perr)

		_, cerr := dbClient.Ent().Chunk.Create().SetHash(h).SetSize(10).SetCompressedSize(5).Save(ctx)
		require.NoError(t, cerr)

		return h
	}

	staleHash := mkChunk()   // linked only to the dechunked nar_file → reclaim
	sharedHash := mkChunk()  // linked to both → retain (chunked keeps a live link)
	healthyHash := mkChunk() // linked only to the chunked nar_file → retain
	midHash := mkChunk()     // linked only to the mid-chunking nar_file → retain

	link := func(nfID int, chunkHash string, idx int) {
		ch, qerr := dbClient.Ent().Chunk.Query().Where(entchunk.HashEQ(chunkHash)).Only(ctx)
		require.NoError(t, qerr)

		_, lerr := dbClient.Ent().NarFileChunk.Create().
			SetNarFileID(nfID).SetChunkID(ch.ID).SetChunkIndex(idx).Save(ctx)
		require.NoError(t, lerr)
	}

	link(dechunkedNF.ID, staleHash, 0)  // stale
	link(dechunkedNF.ID, sharedHash, 1) // stale
	link(chunkedNF.ID, sharedHash, 0)   // live
	link(chunkedNF.ID, healthyHash, 1)  // live
	link(midChunkingNF.ID, midHash, 0)  // mid-chunking, not stale

	linksDeleted, chunksReclaimed, err := reclaimStaleChunkLinks(ctx, dbClient, chunkStore)
	require.NoError(t, err)
	assert.Equal(t, 2, linksDeleted, "both stale links on the dechunked nar_file must be deleted")
	assert.Equal(t, 1, chunksReclaimed, "only the stale-only chunk is reclaimed; the shared chunk is retained")

	// staleHash: gone from DB and storage.
	staleExists, err := dbClient.Ent().Chunk.Query().Where(entchunk.HashEQ(staleHash)).Exist(ctx)
	require.NoError(t, err)
	assert.False(t, staleExists, "stale-only chunk DB row must be reclaimed")

	staleBlob, err := chunkStore.HasChunk(ctx, staleHash)
	require.NoError(t, err)
	assert.False(t, staleBlob, "stale-only chunk blob must be reclaimed")

	// sharedHash + healthyHash retained (still linked to the chunked nar_file);
	// midHash retained because its parent is mid-chunking (not stale).
	for _, h := range []string{sharedHash, healthyHash, midHash} {
		exists, err := dbClient.Ent().Chunk.Query().Where(entchunk.HashEQ(h)).Exist(ctx)
		require.NoError(t, err)
		assert.True(t, exists, "chunk that must be retained was reclaimed: %s", h)
	}

	// The mid-chunking nar_file's link must be preserved (the not-actively-chunking guard).
	midLinks, err := dbClient.Ent().NarFileChunk.Query().
		Where(entnarfilechunk.NarFileIDEQ(midChunkingNF.ID)).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, midLinks, "a mid-chunking nar_file's links must not be treated as stale")

	// The chunked nar_file keeps both its links.
	chunkedLinks, err := dbClient.Ent().NarFileChunk.Query().
		Where(entnarfilechunk.NarFileIDEQ(chunkedNF.ID)).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, chunkedLinks, "the chunked nar_file's links must be untouched")

	// Idempotent.
	l2, c2, err := reclaimStaleChunkLinks(ctx, dbClient, chunkStore)
	require.NoError(t, err)
	assert.Zero(t, l2, "a second run deletes no links")
	assert.Zero(t, c2, "a second run reclaims no chunks")
}
