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

// TestHasFinishedNarExcludesActivelyChunking is the crux of the CDC-window
// non-holder 404 fix: hasFinishedNar MUST report an actively-chunking NAR
// (total_chunks==0, chunker live) as NOT finished, even though isServable reports
// it as servable. The download-coordination poll loop relies on this split so a
// cross-pod waiter routes an in-flight chunked NAR to in-flight staging or
// progressive chunk streaming instead of treating it as a finished asset — which
// would route to chunk serving and 404 a compressed (.nar.xz) request.
func TestHasFinishedNarExcludesActivelyChunking(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := setupCDCRecoveryFixture(t)

	hash := testhelper.MustRandBase32NarHash()
	narURL := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}

	// An actively-chunking row: total_chunks==0, a fresh chunking_started_at, and the
	// migration lock held so cdcChunkerLive() reports the producer alive.
	_, err := c.dbClient.Ent().NarFile.Create().
		SetHash(hash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(4096).
		SetTotalChunks(0).
		SetChunkingStartedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	acquired, err := c.downloadLocker.TryLock(ctx, migrationLockKey(hash), c.downloadLockTTL)
	require.NoError(t, err)
	require.True(t, acquired)
	t.Cleanup(func() { _ = c.downloadLocker.Unlock(ctx, migrationLockKey(hash)) })

	servable, err := c.isServable(ctx, narURL)
	require.NoError(t, err)
	assert.True(t, servable,
		"an actively-chunking NAR with a live producer is servable (it can be progressively streamed)")

	assert.False(t, c.hasFinishedNar(ctx, narURL),
		"an actively-chunking NAR must NOT count as finished: it is still in-flight and must "+
			"route to staging/progressive serving, not be treated as a finished chunk-served asset")
}

// TestHasFinishedNarCountsFullyChunked verifies a fully-chunked NAR (total_chunks>0)
// IS finished.
func TestHasFinishedNarCountsFullyChunked(t *testing.T) {
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
		SetTotalChunks(3).
		Save(ctx)
	require.NoError(t, err)

	assert.True(t, c.hasFinishedNar(ctx, narURL),
		"a fully-chunked NAR (total_chunks>0) is a finished, servable asset")
}
