package cache

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entnarfilechunk "github.com/kalbasit/ncps/ent/narfilechunk"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/testhelper"
)

func TestRunCDCLazyRecoveryClearsStaleCDCChunkingLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c := setupCDCRecoveryFixture(t)

	stale, err := c.dbClient.Ent().NarFile.Create().
		SetHash(testhelper.MustRandBase32NarHash()).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(4096).
		SetTotalChunks(0).
		SetChunkingStartedAt(time.Now().Add(-2 * cdcChunkingLockTTL)).
		Save(ctx)
	require.NoError(t, err)

	seedChunkLinks(ctx, t, c, stale.ID, 3)

	runCDCRecoveryForTest(ctx, t, c)

	healed, err := c.dbClient.Ent().NarFile.Get(ctx, stale.ID)
	require.NoError(t, err)
	assert.Nil(t, healed.ChunkingStartedAt)
	assert.Equal(t, int64(0), healed.TotalChunks)
	assert.Zero(t, countChunkLinks(ctx, t, c, stale.ID))
}

func TestRunCDCLazyRecoveryLeavesFreshAndCompletedChunkingRowsUntouched(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c := setupCDCRecoveryFixture(t)

	freshStartedAt := time.Now()
	fresh, err := c.dbClient.Ent().NarFile.Create().
		SetHash(testhelper.MustRandBase32NarHash()).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(1024).
		SetTotalChunks(0).
		SetChunkingStartedAt(freshStartedAt).
		Save(ctx)
	require.NoError(t, err)
	seedChunkLinks(ctx, t, c, fresh.ID, 1)

	completedStartedAt := time.Now().Add(-2 * cdcChunkingLockTTL)
	completed, err := c.dbClient.Ent().NarFile.Create().
		SetHash(testhelper.MustRandBase32NarHash()).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(2048).
		SetTotalChunks(5).
		SetChunkingStartedAt(completedStartedAt).
		Save(ctx)
	require.NoError(t, err)
	seedChunkLinks(ctx, t, c, completed.ID, 2)

	runCDCRecoveryForTest(ctx, t, c)

	freshAfter, err := c.dbClient.Ent().NarFile.Get(ctx, fresh.ID)
	require.NoError(t, err)
	require.NotNil(t, freshAfter.ChunkingStartedAt)
	assert.Equal(t, freshStartedAt.Unix(), freshAfter.ChunkingStartedAt.Unix())
	assert.Equal(t, int64(0), freshAfter.TotalChunks)
	assert.Equal(t, 1, countChunkLinks(ctx, t, c, fresh.ID))

	completedAfter, err := c.dbClient.Ent().NarFile.Get(ctx, completed.ID)
	require.NoError(t, err)
	require.NotNil(t, completedAfter.ChunkingStartedAt)
	assert.Equal(t, completedStartedAt.Unix(), completedAfter.ChunkingStartedAt.Unix())
	assert.Equal(t, int64(5), completedAfter.TotalChunks)
	assert.Equal(t, 2, countChunkLinks(ctx, t, c, completed.ID))
}

func TestRunCDCLazyRecoverySkipsStaleCDCChunkingRowWhenMigrationLockHeld(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c := setupCDCRecoveryFixture(t)

	stale, err := c.dbClient.Ent().NarFile.Create().
		SetHash(testhelper.MustRandBase32NarHash()).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(4096).
		SetTotalChunks(0).
		SetChunkingStartedAt(time.Now().Add(-2 * cdcChunkingLockTTL)).
		Save(ctx)
	require.NoError(t, err)

	seedChunkLinks(ctx, t, c, stale.ID, 2)

	acquired, err := c.downloadLocker.TryLock(ctx, migrationLockKey(stale.Hash), c.downloadLockTTL)
	require.NoError(t, err)
	require.True(t, acquired)
	t.Cleanup(func() { _ = c.downloadLocker.Unlock(ctx, migrationLockKey(stale.Hash)) })

	runCDCRecoveryForTest(ctx, t, c)

	stillLocked, err := c.dbClient.Ent().NarFile.Get(ctx, stale.ID)
	require.NoError(t, err)
	require.NotNil(t, stillLocked.ChunkingStartedAt)
	assert.Equal(t, int64(0), stillLocked.TotalChunks)
	assert.Equal(t, 2, countChunkLinks(ctx, t, c, stale.ID))
}

func setupCDCRecoveryFixture(t *testing.T) *Cache {
	t.Helper()

	c, _, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	chunkStore, err := chunk.NewLocalStore(filepath.Join(dir, "chunks-store"))
	require.NoError(t, err)
	c.SetChunkStore(chunkStore)
	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

	return c
}

func runCDCRecoveryForTest(ctx context.Context, t *testing.T, c *Cache) {
	t.Helper()

	schedule, err := cron.ParseStandard("@every 5m")
	require.NoError(t, err)

	c.runCDCLazyRecovery(ctx, schedule, 10)()
}

func countChunkLinks(ctx context.Context, t *testing.T, c *Cache, narFileID int) int {
	t.Helper()

	count, err := c.dbClient.Ent().NarFileChunk.Query().
		Where(entnarfilechunk.NarFileIDEQ(narFileID)).
		Count(ctx)
	require.NoError(t, err)

	return count
}

func seedChunkLinks(ctx context.Context, t *testing.T, c *Cache, narFileID int, count int) {
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
