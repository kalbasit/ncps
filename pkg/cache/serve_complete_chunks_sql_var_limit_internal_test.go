package cache

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/ent"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/testdata"
)

// fakeChunkHash returns a deterministic, unique, non-empty 64-hex chunk hash for
// index i. The Chunk schema only constrains hash to NotEmpty + unique, so this is
// a valid stand-in without needing real content.
func fakeChunkHash(i int) string { return fmt.Sprintf("%064x", i) }

// seedCompletedChunkedNar creates a completed chunked nar_file with numChunks
// distinct chunks and junction links. Chunk rows are inserted ascending by index,
// but links are inserted in DESCENDING chunk_index order so that rowid/insertion
// order is the reverse of chunk_index order — a retrieval that fails to honor
// ORDER BY chunk_index would return hashes in the wrong order. Inserts are batched
// well under driver parameter limits so seeding itself never overflows.
func seedCompletedChunkedNar(ctx context.Context, t *testing.T, c *Cache, numChunks int) *ent.NarFile {
	t.Helper()

	client := c.dbClient.Ent()

	nf, err := client.NarFile.Create().
		SetHash(testdata.Nar1.NarHash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(1).
		SetTotalChunks(int64(numChunks)).
		Save(ctx)
	require.NoError(t, err)

	const insertBatch = 500

	// Insert chunks (ascending index) and remember each chunk's DB id by index.
	chunkIDByIndex := make([]int, numChunks)

	for start := 0; start < numChunks; start += insertBatch {
		end := min(start+insertBatch, numChunks)

		builders := make([]*ent.ChunkCreate, 0, end-start)
		for i := start; i < end; i++ {
			builders = append(builders, client.Chunk.Create().
				SetHash(fakeChunkHash(i)).
				SetSize(1).
				SetCompressedSize(1))
		}

		created, err := client.Chunk.CreateBulk(builders...).Save(ctx)
		require.NoError(t, err)

		for j, ch := range created {
			chunkIDByIndex[start+j] = ch.ID
		}
	}

	// Insert links in DESCENDING chunk_index order, batched.
	for start := numChunks; start > 0; start -= insertBatch {
		end := max(start-insertBatch, 0)

		builders := make([]*ent.NarFileChunkCreate, 0, start-end)
		for i := start - 1; i >= end; i-- {
			builders = append(builders, client.NarFileChunk.Create().
				SetNarFileID(nf.ID).
				SetChunkID(chunkIDByIndex[i]).
				SetChunkIndex(i))
		}

		_, err := client.NarFileChunk.CreateBulk(builders...).Save(ctx)
		require.NoError(t, err)
	}

	return nf
}

// TestCollectCompleteChunkHashes_AboveSQLiteParamLimit is the regression test for
// issue #1463: a completed chunked NAR with more chunks than the database driver's
// bound-parameter limit (SQLite 32766) must be retrievable. The pre-fix
// implementation loaded every chunk link in a single unbounded
// NarFileChunk.Query()…WithChunk().All(ctx), whose eager-load compiled to
// SELECT … WHERE id IN ($1 … $N) and failed with "too many SQL variables".
func TestCollectCompleteChunkHashes_AboveSQLiteParamLimit(t *testing.T) {
	t.Parallel()

	c, ctx := newCDCCacheForStreaming(t)

	// Strictly above the SQLite bound-parameter limit (32766), independent of the
	// internal batch size.
	const numChunks = 33000

	nf := seedCompletedChunkedNar(ctx, t, c, numChunks)

	hashes, err := c.collectCompleteChunkHashes(ctx, int64(nf.ID), int64(numChunks))
	require.NoError(t, err,
		"a NAR with more chunks than the driver parameter limit must be retrievable via batched queries")
	require.Len(t, hashes, numChunks)

	// Hashes must come back in ascending chunk_index order despite descending
	// insertion order.
	want := make([]string, numChunks)
	for i := range want {
		want[i] = fakeChunkHash(i)
	}

	assert.Equal(t, want, hashes, "chunk hashes must be ordered by chunk_index")
}

// TestCollectCompleteChunkHashes_ExactMultipleOfBatchSize guards the page-boundary
// walk: when the chunk count is an exact multiple of the batch size, the final
// full page must be followed by a terminating empty page rather than looping or
// over-reading.
func TestCollectCompleteChunkHashes_ExactMultipleOfBatchSize(t *testing.T) {
	t.Parallel()

	c, ctx := newCDCCacheForStreaming(t)

	numChunks := 2 * completeChunkQueryBatchSize

	nf := seedCompletedChunkedNar(ctx, t, c, numChunks)

	hashes, err := c.collectCompleteChunkHashes(ctx, int64(nf.ID), int64(numChunks))
	require.NoError(t, err)
	require.Len(t, hashes, numChunks)

	for i := range hashes {
		require.Equal(t, fakeChunkHash(i), hashes[i])
	}
}

// TestCollectCompleteChunkHashes_MissingLinkFailsCompleteness verifies the
// completeness check still fires after batching: if fewer links exist than
// total_chunks declares, retrieval returns storage.ErrNotFound rather than a short
// success.
func TestCollectCompleteChunkHashes_MissingLinkFailsCompleteness(t *testing.T) {
	t.Parallel()

	c, ctx := newCDCCacheForStreaming(t)

	const seeded = 5

	nf := seedCompletedChunkedNar(ctx, t, c, seeded)

	// Declare one more chunk than actually linked.
	_, err := c.collectCompleteChunkHashes(ctx, int64(nf.ID), int64(seeded+1))
	require.ErrorIs(t, err, storage.ErrNotFound,
		"an incomplete link set must fail the completeness check")
}
