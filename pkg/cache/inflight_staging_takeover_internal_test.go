package cache

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
)

// TestPollTakeover_DiscardsStaleStaging verifies that when a waiter re-acquires
// the (expired) download lock — the holder died — it takes over the download and
// discards the dead holder's partial staging part-objects and staging_state, so
// the restarted download re-stages from zero (D5).
func TestPollTakeover_DiscardsStaleStaging(t *testing.T) {
	t.Parallel()

	c, _, store, _, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	c.SetInflightStaging(true, 5*time.Minute, 4, true)

	ctx := context.Background()

	const (
		hash    = "deadbeefdeadbeefdeadbeefdeadbeef"
		lockKey = "takeover-test-lock"
	)

	// A dead holder left a partial, never-completed staging set behind.
	putStagingParts(t, store, hash, "abcd", 4)
	require.NoError(t, c.markStagingRequested(ctx, hash))
	require.NoError(t, c.advanceStagingParts(ctx, hash, 1, nar.CompressionTypeNone.String()))

	// The lock is free (the holder's lock expired), so TryLock succeeds on the
	// first tick and the waiter takes over.
	ds, tookOver := c.pollForDownloadOrTakeOver(
		ctx, ctx, lockKey, hash, true, storage.ErrNotFound,
		func(context.Context) bool { return false },
	)
	require.True(t, tookOver, "an acquirable lock means the holder is gone: take over")
	assert.Nil(t, ds)

	// The new owner now holds the lock; release it.
	require.NoError(t, c.downloadLocker.Unlock(ctx, lockKey))

	// coordinateDownload runs the staging reset after the lock refresher is active;
	// invoke it directly here to exercise the takeover cleanup.
	require.NoError(t, c.resetStagingForTakeover(ctx, hash))

	// The dead holder's partial parts are discarded, but the staging_state row is
	// preserved (reset to "requested") so a persisting cross-pod waiter is re-served.
	st, err := c.getStagingState(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, st, "the request marker must survive takeover")
	assert.Equal(t, int64(0), st.PartsAvailable, "reset rewinds progress to zero")
	assert.Equal(t, stagingStatusRequested, st.Status, "reset preserves the request for re-staging")

	_, err = store.GetStagingPart(ctx, hash, 0)
	require.ErrorIs(t, err, storage.ErrNotFound, "partial parts must be discarded on takeover")
}

// TestStagingTakeover_NoTruncatedServeAcrossDeath verifies the correctness
// contract across a holder-death + takeover transition: a reader tailing the dead
// holder's partial (never-completed) staging surfaces a stream error rather than a
// clean EOF at a truncated length, and the takeover then discards those parts.
func TestStagingTakeover_NoTruncatedServeAcrossDeath(t *testing.T) {
	t.Parallel()

	c, _, store, _, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	c.SetInflightStaging(true, 5*time.Minute, 4, true)

	ctx := context.Background()

	const (
		hash    = "cafecafecafecafecafecafecafecafe"
		lockKey = "takeover-truncation-lock"
	)

	// Holder died after staging only the first of (conceptually) several parts.
	putStagingParts(t, store, hash, "abcd", 4)
	require.NoError(t, c.markStagingRequested(ctx, hash))
	require.NoError(t, c.advanceStagingParts(ctx, hash, 1, nar.CompressionTypeNone.String()))

	// A reader tailing this partial set delivers the prefix then stalls — and
	// surfaces a stream error, never a truncated clean EOF.
	r := c.newStagingPartReader(ctx, hash)
	r.maxWait = 200 * time.Millisecond
	r.pollEvery = 20 * time.Millisecond

	body, readErr := io.ReadAll(r)
	require.NoError(t, r.Close())
	require.Error(t, readErr, "a never-completed staging set must not read as a clean EOF")
	require.NotErrorIs(t, readErr, io.EOF)
	assert.Equal(t, "abcd", string(body), "the partial prefix is delivered, but not as success")

	// Takeover re-acquires the lock; coordinateDownload then runs resetStagingForTakeover
	// once the lock refresher is active. The reset discards the truncated parts so a
	// fresh download re-stages cleanly, while preserving the staging_state row at
	// "requested" so a persisting cross-pod waiter is re-served.
	_, tookOver := c.pollForDownloadOrTakeOver(
		ctx, ctx, lockKey, hash, true, storage.ErrNotFound,
		func(context.Context) bool { return false },
	)
	require.True(t, tookOver)
	require.NoError(t, c.downloadLocker.Unlock(ctx, lockKey))

	require.NoError(t, c.resetStagingForTakeover(ctx, hash))

	_, err := store.GetStagingPart(ctx, hash, 0)
	require.ErrorIs(t, err, storage.ErrNotFound, "truncated parts must be discarded on takeover")

	st, err := c.getStagingState(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, st, "the waiter's request marker must survive takeover")
	assert.Equal(t, int64(0), st.PartsAvailable, "reset rewinds progress to zero")
	assert.Equal(t, stagingStatusRequested, st.Status, "reset preserves the request for re-staging")
}
