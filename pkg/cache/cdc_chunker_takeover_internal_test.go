package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testhelper"
)

// findOrCreateNarFileForCDC always runs while the caller holds migrationLockKey(hash)
// (download path, putNarWithCDC, MigrateNarToChunks). Holding the lock proves any prior
// chunker is dead — a live one would still hold it. So a fresh chunking_started_at on a
// total_chunks=0 row must be reclaimed and taken over (not refused with ErrAlreadyExists),
// otherwise a re-download triggered by the #1230 read-path fix can never re-chunk the
// orphan until the cron reaps it.
func TestFindOrCreateNarFileForCDCTakesOverFreshOrphanUnderLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c := setupCDCRecoveryFixture(t)

	hash := testhelper.MustRandBase32NarHash()
	narURL := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}

	nf, err := c.dbClient.Ent().NarFile.Create().
		SetHash(hash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(4096).
		SetTotalChunks(0).
		SetChunkingStartedAt(time.Now()). // fresh lock, well under cdcChunkingLockTTL
		Save(ctx)
	require.NoError(t, err)

	seedChunkLinks(ctx, t, c, nf.ID, 3)

	// Mirror reality: every caller of findOrCreateNarFileForCDC holds the migration lock.
	acquired, err := c.downloadLocker.TryLock(ctx, migrationLockKey(hash), c.downloadLockTTL)
	require.NoError(t, err)
	require.True(t, acquired)
	t.Cleanup(func() { _ = c.downloadLocker.Unlock(ctx, migrationLockKey(hash)) })

	id, stale, err := c.findOrCreateNarFileForCDC(ctx, &narURL, 4096)
	require.NoError(t, err,
		"takeover under the migration lock must not refuse a fresh orphan with ErrAlreadyExists")
	assert.Equal(t, int64(nf.ID), id)
	assert.Len(t, stale, 3, "the dead chunker's partial chunks must be reclaimed")
	assert.Zero(t, countChunkLinks(ctx, t, c, nf.ID), "partial chunk links must be cleared on takeover")
}
