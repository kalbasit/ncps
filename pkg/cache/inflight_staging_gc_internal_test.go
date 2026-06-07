package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/ent/stagingstate"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
)

// TestReclaimStaging_DeletesPartsAndRecord verifies that reclaiming staging
// removes both the part-objects and the staging_state record.
func TestReclaimStaging_DeletesPartsAndRecord(t *testing.T) {
	t.Parallel()

	c, _, store, _, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	c.SetInflightStaging(true, 5*time.Minute, 4, true)

	ctx := context.Background()

	const hash = "1111111111111111111111111111aaaa"

	nParts := putStagingParts(t, store, hash, "abcdefghij", 4)
	require.NoError(t, c.markStagingRequested(ctx, hash))
	require.NoError(t, c.advanceStagingParts(ctx, hash, nParts, nar.CompressionTypeNone.String()))
	require.NoError(t, c.markStagingComplete(ctx, hash))

	require.NoError(t, c.reclaimStaging(ctx, hash))

	st, err := c.getStagingState(ctx, hash)
	require.NoError(t, err)
	assert.Nil(t, st, "staging_state record must be deleted")

	_, err = store.GetStagingPart(ctx, hash, 0)
	require.ErrorIs(t, err, storage.ErrNotFound, "part-objects must be deleted")
}

// TestScheduleStagingReclaim_DeletesAfterGrace verifies the event-driven reclaim:
// once staging completes, the artifacts are deleted after the retention grace.
func TestScheduleStagingReclaim_DeletesAfterGrace(t *testing.T) {
	t.Parallel()

	c, _, store, _, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	// Tiny grace so the test does not wait the production default.
	c.SetInflightStaging(true, 50*time.Millisecond, 4, true)

	ctx := context.Background()

	const hash = "2222222222222222222222222222bbbb"

	nParts := putStagingParts(t, store, hash, "abcdefghij", 4)
	require.NoError(t, c.markStagingRequested(ctx, hash))
	require.NoError(t, c.advanceStagingParts(ctx, hash, nParts, nar.CompressionTypeNone.String()))
	require.NoError(t, c.markStagingComplete(ctx, hash))

	c.scheduleStagingReclaim(hash)

	require.Eventually(t, func() bool {
		st, err := c.getStagingState(ctx, hash)

		return err == nil && st == nil
	}, 5*time.Second, 20*time.Millisecond, "staging must be reclaimed after the grace period")

	_, err := store.GetStagingPart(ctx, hash, 0)
	require.ErrorIs(t, err, storage.ErrNotFound)
}

// TestSweepStagingGC_ReclaimsOrphansAndStaleComplete verifies the periodic sweep:
// it reclaims orphaned staging (holder died: non-complete and stale beyond
// orphanAge) and completed staging past its retention grace, while leaving fresh
// completed staging in place.
func TestSweepStagingGC_ReclaimsOrphansAndStaleComplete(t *testing.T) {
	t.Parallel()

	c, _, store, _, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	c.SetInflightStaging(true, time.Hour, 4, true)

	ctx := context.Background()

	const (
		orphan       = "33333333333333333333333333330000" // non-complete, stale -> reclaim
		completeOld  = "44444444444444444444444444441111" // complete, stale > retention -> reclaim
		completeNew  = "55555555555555555555555555552222" // complete, fresh -> keep
		orphanFresh  = "66666666666666666666666666663333" // non-complete, fresh -> keep
		retentionArg = time.Hour
		orphanArg    = time.Minute
	)

	// Orphan: staging in progress, holder died (updated_at stale).
	putStagingParts(t, store, orphan, "abcd", 4)
	require.NoError(t, c.markStagingRequested(ctx, orphan))
	require.NoError(t, c.advanceStagingParts(ctx, orphan, 1, nar.CompressionTypeNone.String()))
	forceStagingUpdatedAt(t, c, orphan, time.Now().Add(-90*time.Minute))

	// Complete but past retention.
	putStagingParts(t, store, completeOld, "efgh", 4)
	require.NoError(t, c.markStagingRequested(ctx, completeOld))
	require.NoError(t, c.advanceStagingParts(ctx, completeOld, 1, nar.CompressionTypeNone.String()))
	require.NoError(t, c.markStagingComplete(ctx, completeOld))
	forceStagingUpdatedAt(t, c, completeOld, time.Now().Add(-2*time.Hour))

	// Complete and fresh.
	putStagingParts(t, store, completeNew, "ijkl", 4)
	require.NoError(t, c.markStagingRequested(ctx, completeNew))
	require.NoError(t, c.advanceStagingParts(ctx, completeNew, 1, nar.CompressionTypeNone.String()))
	require.NoError(t, c.markStagingComplete(ctx, completeNew))

	// Non-complete but fresh (a live holder mid-staging).
	putStagingParts(t, store, orphanFresh, "mnop", 4)
	require.NoError(t, c.markStagingRequested(ctx, orphanFresh))
	require.NoError(t, c.advanceStagingParts(ctx, orphanFresh, 1, nar.CompressionTypeNone.String()))

	n, err := c.sweepStagingGC(ctx, retentionArg, orphanArg)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "exactly the orphan and the stale-complete records are reclaimed")

	assertStagingAbsent(t, c, store, orphan)
	assertStagingAbsent(t, c, store, completeOld)
	assertStagingPresent(t, c, completeNew)
	assertStagingPresent(t, c, orphanFresh)
}

func forceStagingUpdatedAt(t *testing.T, c *Cache, hash string, when time.Time) {
	t.Helper()

	n, err := c.dbClient.Ent().StagingState.Update().
		Where(stagingstate.HashEQ(hash)).
		SetUpdatedAt(when).
		Save(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func assertStagingAbsent(t *testing.T, c *Cache, store storage.NarStore, hash string) {
	t.Helper()

	st, err := c.getStagingState(context.Background(), hash)
	require.NoError(t, err)
	assert.Nil(t, st, "staging_state for %q must be reclaimed", hash)

	_, err = store.GetStagingPart(context.Background(), hash, 0)
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func assertStagingPresent(t *testing.T, c *Cache, hash string) {
	t.Helper()

	st, err := c.getStagingState(context.Background(), hash)
	require.NoError(t, err)
	require.NotNil(t, st, "staging_state for %q must be preserved", hash)
}
