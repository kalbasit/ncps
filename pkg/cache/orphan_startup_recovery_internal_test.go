package cache

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entnarfile "github.com/kalbasit/ncps/ent/narfile"
	entnarfilechunk "github.com/kalbasit/ncps/ent/narfilechunk"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/testhelper"
)

func TestRecoverOrphanedChunkingOnStartupClearsFreshCrashState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, _, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	chunkStore, err := chunk.NewLocalStore(filepath.Join(dir, "chunks-store"))
	require.NoError(t, err)
	c.SetChunkStore(chunkStore)
	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

	orphaned, err := c.dbClient.Ent().NarFile.Create().
		SetHash(testhelper.MustRandBase32NarHash()).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(4096).
		SetTotalChunks(0).
		SetChunkingStartedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	seedChunkLinks(t, ctx, c, orphaned.ID, 3)

	require.NoError(t, c.RecoverOrphanedChunkingOnStartup(ctx))

	healed, err := c.dbClient.Ent().NarFile.Get(ctx, orphaned.ID)
	require.NoError(t, err)
	assert.Nil(t, healed.ChunkingStartedAt)
	assert.Equal(t, int64(0), healed.TotalChunks)

	count, err := c.dbClient.Ent().NarFileChunk.Query().
		Where(entnarfilechunk.NarFileIDEQ(orphaned.ID)).
		Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, count)
}

func TestRecoverOrphanedChunkingOnStartupLeavesNonOrphansUntouched(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, _, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	chunkStore, err := chunk.NewLocalStore(filepath.Join(dir, "chunks-store"))
	require.NoError(t, err)
	c.SetChunkStore(chunkStore)
	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

	notStarted, err := c.dbClient.Ent().NarFile.Create().
		SetHash(testhelper.MustRandBase32NarHash()).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(1024).
		SetTotalChunks(0).
		Save(ctx)
	require.NoError(t, err)
	seedChunkLinks(t, ctx, c, notStarted.ID, 1)

	startedAt := time.Now()
	completed, err := c.dbClient.Ent().NarFile.Create().
		SetHash(testhelper.MustRandBase32NarHash()).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(2048).
		SetTotalChunks(5).
		SetChunkingStartedAt(startedAt).
		Save(ctx)
	require.NoError(t, err)
	seedChunkLinks(t, ctx, c, completed.ID, 2)

	notStartedBefore, err := c.dbClient.Ent().NarFile.Get(ctx, notStarted.ID)
	require.NoError(t, err)

	completedBefore, err := c.dbClient.Ent().NarFile.Get(ctx, completed.ID)
	require.NoError(t, err)

	require.NoError(t, c.RecoverOrphanedChunkingOnStartup(ctx))

	notStartedAfter, err := c.dbClient.Ent().NarFile.Get(ctx, notStarted.ID)
	require.NoError(t, err)
	assert.Nil(t, notStartedAfter.ChunkingStartedAt)
	assert.Equal(t, notStartedBefore.TotalChunks, notStartedAfter.TotalChunks)

	completedAfter, err := c.dbClient.Ent().NarFile.Get(ctx, completed.ID)
	require.NoError(t, err)
	require.NotNil(t, completedAfter.ChunkingStartedAt)
	assert.Equal(t, completedBefore.TotalChunks, completedAfter.TotalChunks)
	assert.Equal(t, completedBefore.ChunkingStartedAt.UnixNano(), completedAfter.ChunkingStartedAt.UnixNano())

	for _, nr := range []struct {
		name string
		id   int
		want int
	}{
		{name: "not started", id: notStarted.ID, want: 1},
		{name: "completed", id: completed.ID, want: 2},
	} {
		count, err := c.dbClient.Ent().NarFileChunk.Query().
			Where(entnarfilechunk.NarFileIDEQ(nr.id)).
			Count(ctx)
		require.NoError(t, err)
		assert.Equal(t, nr.want, count, "%s chunk links must be preserved", nr.name)
	}

	remaining, err := c.dbClient.Ent().NarFile.Query().
		Where(entnarfile.IDIn(notStarted.ID, completed.ID)).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, remaining)
}

func seedChunkLinks(t *testing.T, ctx context.Context, c *Cache, narFileID int, count int) {
	t.Helper()

	for i := 0; i < count; i++ {
		ch, err := c.dbClient.Ent().Chunk.Create().
			SetHash(testhelper.MustRandBase32NarHash()).
			SetSize(1024).
			SetCompressedSize(512).
			Save(ctx)
		require.NoError(t, err)

		_, err = c.dbClient.Ent().NarFileChunk.Create().
			SetNarFileID(narFileID).
			SetChunkID(ch.ID).
			SetChunkIndex(i).
			Save(ctx)
		require.NoError(t, err)
	}
}
