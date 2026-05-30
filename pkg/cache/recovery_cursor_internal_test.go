package cache

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entconfigentry "github.com/kalbasit/ncps/ent/configentry"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
)

func readRecoveryCursor(ctx context.Context, t *testing.T, c *Cache) string {
	t.Helper()

	e, err := c.dbClient.Ent().ConfigEntry.Query().
		Where(entconfigentry.KeyEQ(cdcRecoveryCursorKey)).
		Only(ctx)
	if err != nil {
		return "" // not persisted
	}

	return e.Value
}

// TestRunCDCLazyRecoveryCursorPersists verifies the lazy-recovery keyset cursor is
// persisted in the DB (not just process memory), so it survives a pod restart or a
// lock handoff to another instance — otherwise the scan restarts at 0 and a low-id
// backlog can keep starving higher-id rows.
func TestRunCDCLazyRecoveryCursorPersists(t *testing.T) {
	t.Parallel()

	c, db, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	chunkStore, err := chunk.NewLocalStore(filepath.Join(dir, "chunks-store"))
	require.NoError(t, err)
	c.SetChunkStore(chunkStore)
	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

	ctx := context.Background()

	// Three backing-less rows with no narinfo and no upstream: the sweep skips them
	// (nothing to migrate, nothing to GC) but the cursor still advances over them.
	hashes := []string{
		"cursorrowaaaaaaaaaaaaaaaaaaaaaaaa",
		"cursorrowbbbbbbbbbbbbbbbbbbbbbbbb",
		"cursorrowcccccccccccccccccccccccc",
	}

	ids := make([]int, 0, len(hashes))

	for _, h := range hashes {
		nf, err := c.dbClient.Ent().NarFile.Create().
			SetHash(h).
			SetCompression(nar.CompressionTypeNone.String()).
			SetQuery("").
			SetFileSize(1).
			Save(ctx)
		require.NoError(t, err)

		ids = append(ids, nf.ID)
	}

	_, err = db.DB().ExecContext(ctx,
		"UPDATE nar_files SET total_chunks = 0, chunking_started_at = NULL, created_at = ?",
		time.Now().Add(-10*time.Minute))
	require.NoError(t, err)

	schedule, err := cron.ParseStandard("@every 5m")
	require.NoError(t, err)

	// First sweep (one process), batch size 2: processes the first two rows and must
	// persist the cursor at the second row's id.
	c.runCDCLazyRecovery(ctx, schedule, 2)()
	assert.Equal(t, strconv.Itoa(ids[1]), readRecoveryCursor(ctx, t, c),
		"cursor must be persisted after advancing past the first batch")

	// A FRESH closure (simulating a restart / different instance) must resume from the
	// persisted cursor, reach the last row, hit the end (short batch) and wrap to 0.
	c.runCDCLazyRecovery(ctx, schedule, 2)()
	assert.Equal(t, "0", readRecoveryCursor(ctx, t, c),
		"a fresh sweep must resume from the persisted cursor and wrap at the end")
}
