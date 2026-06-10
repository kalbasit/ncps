package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
)

// TestShouldCoordinateInflightUncompressedRequiresStaging pins the read-path half
// of closing the eager-CDC chunking-window staging gap (#1289): an uncompressed
// (none) request for an actively-chunking NAR (servable but not finished) must be
// routed to download coordination — so it contends, records an in-flight staging
// request, and serves from staging — rather than served directly from progressive
// chunks, but ONLY when in-flight staging is enabled. With staging disabled it
// must keep serving progressively (unchanged behavior).
func TestShouldCoordinateInflightUncompressedRequiresStaging(t *testing.T) {
	t.Parallel()

	c := setupCDCRecoveryFixture(t)

	// Staging disabled (default): an uncompressed read serves from chunks, no
	// coordination — the progressive-chunk path is preserved.
	assert.False(t, c.shouldCoordinateInflight(nar.CompressionTypeNone),
		"uncompressed read must NOT coordinate when staging is disabled (progressive chunks preserved)")

	// Staging enabled: the uncompressed read coordinates so it engages in-flight
	// staging instead of the fragile progressive reassembly.
	c.SetInflightStaging(true, 5*time.Minute, 4, true)
	assert.True(t, c.shouldCoordinateInflight(nar.CompressionTypeNone),
		"uncompressed read must coordinate when staging is enabled (prefer staging over progressive chunks)")
}

// TestShouldCoordinateInflightCompressedAlways verifies a compressed (.nar.xz)
// request for an actively-chunking NAR always routes to coordination — decompressed
// chunks cannot satisfy it — independent of the staging flag.
func TestShouldCoordinateInflightCompressedAlways(t *testing.T) {
	t.Parallel()

	c := setupCDCRecoveryFixture(t)

	assert.True(t, c.shouldCoordinateInflight(nar.CompressionTypeXz),
		"compressed read always coordinates (chunks can't satisfy it) with staging disabled")

	c.SetInflightStaging(true, 5*time.Minute, 4, true)
	assert.True(t, c.shouldCoordinateInflight(nar.CompressionTypeXz),
		"compressed read always coordinates with staging enabled too")
}

// TestPollDStateWaitsForStagingPartsBeforeProgressive pins the poll-loop half of
// closing the gap: when in-flight staging is enabled and the holder is actively
// chunking (servable, not finished) but staging parts are not yet available on the
// first poll tick, state (D) MUST keep polling for staging parts (so the staging
// serve wins) instead of immediately returning a served-by-peer state that routes
// the reader to progressive chunk reassembly.
func TestPollDStateWaitsForStagingPartsBeforeProgressive(t *testing.T) {
	t.Parallel()

	c, _, _, _, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	c.SetInflightStaging(true, 5*time.Minute, 4, true)

	ctx := context.Background()

	const (
		hash    = "deadbeefdeadbeefdeadbeefdeadbeef"
		lockKey = "dstate-staging-wait-lock"
	)

	// Holder is alive: hold the lock so TryLock fails on every tick (no takeover),
	// forcing the waiter to choose between staging and progressive.
	acquired, err := c.downloadLocker.TryLock(ctx, lockKey, c.downloadLockTTL)
	require.NoError(t, err)
	require.True(t, acquired)
	t.Cleanup(func() { _ = c.downloadLocker.Unlock(ctx, lockKey) })

	// The holder's producer stages a complete part set shortly AFTER the waiter
	// begins polling — i.e. parts are not yet available on the first (D) tick.
	staged := make(chan error, 1)

	go func() {
		time.Sleep(300 * time.Millisecond)

		_ = c.markStagingRequested(ctx, hash)
		if e := c.advanceStagingParts(ctx, hash, 1, nar.CompressionTypeNone.String()); e != nil {
			staged <- e

			return
		}

		staged <- c.markStagingComplete(ctx, hash)
	}()

	// Actively chunking: servable, not finished.
	ds, tookOver := c.pollForDownloadOrTakeOver(
		ctx, ctx, lockKey, hash, true, storage.ErrNotFound,
		func(context.Context) (bool, bool) { return true, false },
	)

	require.NoError(t, <-staged)
	require.False(t, tookOver, "the holder is alive (lock held); there is no takeover")
	require.NotNil(t, ds)
	assert.NotNil(t, ds.stagingServe,
		"poll-loop state (D) must wait for staging parts and serve from staging, "+
			"not fall back to progressive chunks on the first tick")
}

// TestPollDStateProgressiveWhenStagingDisabled guards the gate: with in-flight
// staging disabled, an actively-chunking servable NAR must still route immediately
// to progressive chunk serving (a served-by-peer state with no stagingServe) — the
// existing behavior is unchanged.
func TestPollDStateProgressiveWhenStagingDisabled(t *testing.T) {
	t.Parallel()

	c, _, _, _, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)
	// Staging left disabled (default).

	ctx := context.Background()

	const (
		hash    = "cafecafecafecafecafecafecafecafe"
		lockKey = "dstate-no-staging-lock"
	)

	acquired, err := c.downloadLocker.TryLock(ctx, lockKey, c.downloadLockTTL)
	require.NoError(t, err)
	require.True(t, acquired)
	t.Cleanup(func() { _ = c.downloadLocker.Unlock(ctx, lockKey) })

	ds, tookOver := c.pollForDownloadOrTakeOver(
		ctx, ctx, lockKey, hash, true, storage.ErrNotFound,
		func(context.Context) (bool, bool) { return true, false },
	)

	require.False(t, tookOver)
	require.NotNil(t, ds)
	assert.Nil(t, ds.stagingServe,
		"with staging disabled, state (D) must route to progressive chunk serving immediately")
	assert.True(t, ds.closed, "served-by-peer state is a completed downloadState")
}

// TestPollDStateFallsBackToProgressiveWhenStagingStalls verifies the safety net:
// when staging is active but the producer never publishes parts, state (D) must
// not wait forever — after the bounded staging wait it falls back to progressive
// chunk serving (a served-by-peer state) rather than hanging until the give-up
// bound.
func TestPollDStateFallsBackToProgressiveWhenStagingStalls(t *testing.T) {
	t.Parallel()

	c, _, _, _, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	c.SetInflightStaging(true, 5*time.Minute, 4, true)

	ctx := context.Background()

	const (
		hash    = "f00df00df00df00df00df00df00df00d"
		lockKey = "dstate-staging-stall-lock"
	)

	// Holder alive, but its producer never stages any parts (a stall).
	acquired, err := c.downloadLocker.TryLock(ctx, lockKey, c.downloadLockTTL)
	require.NoError(t, err)
	require.True(t, acquired)
	t.Cleanup(func() { _ = c.downloadLocker.Unlock(ctx, lockKey) })

	start := time.Now()
	ds, tookOver := c.pollForDownloadOrTakeOver(
		ctx, ctx, lockKey, hash, true, storage.ErrNotFound,
		func(context.Context) (bool, bool) { return true, false },
	)
	elapsed := time.Since(start)

	require.False(t, tookOver)
	require.NotNil(t, ds)
	assert.Nil(t, ds.stagingServe,
		"with no staging parts ever published, the bounded wait must fall back to progressive")
	assert.True(t, ds.closed, "served-by-peer state is a completed downloadState")
	assert.Less(t, elapsed, 30*time.Second,
		"the staging wait must be bounded (well under the give-up bound), not hang")
}
