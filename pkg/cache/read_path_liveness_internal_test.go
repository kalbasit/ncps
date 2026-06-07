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

// The #1230 crash leaves a fresh in-progress orphan (total_chunks=0, recent
// chunking_started_at) whose chunker is dead — no instance holds migrationLockKey(hash).
// isServable must report it NOT servable so GetNar re-downloads cleanly instead of
// committing 200 + partial chunks and stalling maxWaitPerChunk on a chunk that never
// arrives.
func TestIsServableFreshOrphanWithFreeLockIsNotServable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c := setupCDCRecoveryFixture(t)

	hash := testhelper.MustRandBase32NarHash()
	narURL := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}

	_, err := c.dbClient.Ent().NarFile.Create().
		SetHash(hash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(4096).
		SetTotalChunks(0).
		SetChunkingStartedAt(time.Now()). // fresh lock, well under cdcChunkingLockTTL
		Save(ctx)
	require.NoError(t, err)

	servable, err := c.isServable(ctx, narURL)
	require.NoError(t, err)
	assert.False(t, servable,
		"a fresh orphan whose chunker is dead (free migration lock) must not be servable")
}

// A genuinely in-progress chunk has a live producer holding migrationLockKey(hash);
// isServable must keep reporting it servable so legitimate slow chunking still streams.
func TestIsServableFreshInProgressWithHeldLockIsServable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c := setupCDCRecoveryFixture(t)

	hash := testhelper.MustRandBase32NarHash()
	narURL := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}

	_, err := c.dbClient.Ent().NarFile.Create().
		SetHash(hash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(4096).
		SetTotalChunks(0).
		SetChunkingStartedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	// Simulate a live chunker on this or a peer instance.
	acquired, err := c.downloadLocker.TryLock(ctx, migrationLockKey(hash), c.downloadLockTTL)
	require.NoError(t, err)
	require.True(t, acquired)
	t.Cleanup(func() { _ = c.downloadLocker.Unlock(ctx, migrationLockKey(hash)) })

	servable, err := c.isServable(ctx, narURL)
	require.NoError(t, err)
	assert.True(t, servable,
		"an in-progress row with a live chunker (held migration lock) remains servable")
}

// A fully chunked row (total_chunks > 0) is unconditionally servable and must not incur
// a liveness probe.
func TestIsServableFullyChunkedIsServable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c := setupCDCRecoveryFixture(t)

	hash := testhelper.MustRandBase32NarHash()
	narURL := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}

	_, err := c.dbClient.Ent().NarFile.Create().
		SetHash(hash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(4096).
		SetTotalChunks(5).
		Save(ctx)
	require.NoError(t, err)

	servable, err := c.isServable(ctx, narURL)
	require.NoError(t, err)
	assert.True(t, servable, "a fully chunked row must remain servable")
}
