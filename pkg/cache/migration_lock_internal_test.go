package cache

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/testdata"
)

// TestMigration_SharedLockKey_SerializesBothDirections verifies that the forward
// (MigrateNarToChunks) and reverse (MigrateChunksToNar) migrations contend on the
// SAME per-hash lock key, so an exit-CDC run cannot race a concurrent background
// re-chunk of the same NAR.
func TestMigration_SharedLockKey_SerializesBothDirections(t *testing.T) {
	t.Parallel()

	ctx := newContext()

	c, _, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	chunkStore, err := chunk.NewLocalStore(filepath.Join(dir, "chunks-store"))
	require.NoError(t, err)

	c.SetChunkStore(chunkStore)
	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

	hash := testdata.Nar1.NarHash

	// Hold the shared migration lock for this hash.
	acquired, err := c.downloadLocker.TryLock(ctx, migrationLockKey(hash), c.downloadLockTTL)
	require.NoError(t, err)
	require.True(t, acquired)
	t.Cleanup(func() { _ = c.downloadLocker.Unlock(ctx, migrationLockKey(hash)) })

	narURL := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}

	// Both directions must observe the held lock and back off — proving they share a key.
	require.ErrorIs(t, c.MigrateChunksToNar(ctx, &narURL, false), ErrMigrationInProgress,
		"reverse migration must honor the shared migration lock")
	require.ErrorIs(t, c.MigrateNarToChunks(ctx, &narURL), ErrMigrationInProgress,
		"forward migration must honor the shared migration lock")
}
