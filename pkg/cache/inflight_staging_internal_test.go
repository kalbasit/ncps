package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/ent/stagingstate"
	"github.com/kalbasit/ncps/pkg/nar"
)

// TestInflightStagingEnabled verifies the activation guard: the feature is off
// by default, and even when the flag is set it stays off unless the locker is
// distributed (a single-instance / local-locker deployment can never have a
// cross-pod waiter, so staging would only add overhead).
func TestInflightStagingEnabled(t *testing.T) {
	t.Parallel()

	c, _, _, _, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	assert.False(t, c.InflightStagingEnabled(),
		"feature must be disabled by default")

	c.SetInflightStaging(true, 30*time.Minute, 8<<20, false)
	assert.False(t, c.InflightStagingEnabled(),
		"feature must stay disabled under a local (non-distributed) locker")

	c.SetInflightStaging(true, 30*time.Minute, 8<<20, true)
	assert.True(t, c.InflightStagingEnabled(),
		"feature must be enabled when the flag is set and the locker is distributed")

	assert.Equal(t, 30*time.Minute, c.InflightStagingRetention())
	assert.Equal(t, int64(8<<20), c.InflightStagingPartSize())

	c.SetInflightStaging(false, 30*time.Minute, 8<<20, true)
	assert.False(t, c.InflightStagingEnabled(),
		"feature must be disabled when the flag is false even under a distributed locker")
}

// TestStagingState_MarkRequestedIdempotent verifies a waiter can record a staging
// request keyed by hash alone (no nar_file row need exist), and that concurrent
// duplicate requests collapse to a single row rather than erroring.
func TestStagingState_MarkRequestedIdempotent(t *testing.T) {
	t.Parallel()

	c, _, _, _, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	ctx := context.Background()

	const hash = "0a1b2c3d4e5f60718293a4b5c6d7e8f9"

	require.NoError(t, c.markStagingRequested(ctx, hash))

	st, err := c.getStagingState(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, st, "a staging_state row must exist after a request")
	assert.Equal(t, stagingStatusRequested, st.Status)
	assert.NotNil(t, st.RequestedAt)

	// Idempotent: a second request must not error or duplicate.
	require.NoError(t, c.markStagingRequested(ctx, hash))

	count, err := c.dbClient.Ent().StagingState.Query().
		Where(stagingstate.HashEQ(hash)).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "duplicate requests must collapse to one row")
}

// TestStagingState_AdvancePartsAndReset verifies the holder can advance the
// parts-available marker + compression and move to staging, and that a takeover
// reset clears the progress back to a fresh request.
func TestStagingState_AdvancePartsAndReset(t *testing.T) {
	t.Parallel()

	c, _, _, _, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	ctx := context.Background()

	const hash = "ffeeddccbbaa99887766554433221100"

	require.NoError(t, c.markStagingRequested(ctx, hash))

	require.NoError(t, c.advanceStagingParts(ctx, hash, 3, nar.CompressionTypeXz.String()))

	st, err := c.getStagingState(ctx, hash)
	require.NoError(t, err)
	assert.Equal(t, int64(3), st.PartsAvailable)
	assert.Equal(t, nar.CompressionTypeXz.String(), st.Compression)
	assert.Equal(t, stagingStatusStaging, st.Status)

	require.NoError(t, c.resetStagingState(ctx, hash))

	got, err := c.getStagingState(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, got, "reset must preserve the row so a persisting waiter's request survives takeover")
	assert.Equal(t, int64(0), got.PartsAvailable, "reset must rewind progress to zero")
	assert.Equal(t, stagingStatusRequested, got.Status, "reset must move status back to requested for a clean re-stage")
}
