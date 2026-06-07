package cache

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testhelper"
)

// A whole-file-backed placeholder skipped because lazy chunking is disabled must be
// counted with its own counter, NOT folded into stale_recovery_skip_count (which counts
// live-chunker lock-held skips). Conflating them makes the stale-skip metric misleading.
func TestRunCDCLazyRecoveryLazyDisabledSkipUsesDistinctCounter(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c := setupCDCRecoveryFixture(t)

	// Fixture leaves lazy chunking disabled; this is the precondition for the branch.
	require.False(t, c.GetCDCLazyChunkingEnabled())

	hash := testhelper.MustRandBase32NarHash()
	narURL := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}

	// A placeholder row (chunking_started_at NULL) — a lazy candidate, not a stale lock.
	placeholder, err := c.dbClient.Ent().NarFile.Create().
		SetHash(hash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(4).
		SetTotalChunks(0).
		Save(ctx)
	require.NoError(t, err)

	// Back the placeholder with a whole-file so recovery does not GC it as backing-less,
	// and so it reaches the lazy-chunking-disabled skip branch.
	_, err = c.narStore.PutNar(ctx, narURL, strings.NewReader("data"), -1)
	require.NoError(t, err)
	require.True(t, c.HasNarInStore(ctx, narURL))

	// Age the row past the recovery cutoff so the placeholder branch selects it.
	_, err = c.dbClient.DB().ExecContext(ctx,
		"UPDATE nar_files SET created_at = ? WHERE id = ?",
		time.Now().Add(-10*time.Minute), placeholder.ID)
	require.NoError(t, err)

	var logBuf bytes.Buffer

	logCtx := zerolog.New(&logBuf).WithContext(ctx)

	schedule, err := cron.ParseStandard("@every 5m")
	require.NoError(t, err)

	c.runCDCLazyRecovery(logCtx, schedule, 10)()

	out := logBuf.String()
	assert.Contains(t, out, `"lazy_chunking_disabled_skip_count":1`,
		"lazy-disabled skip must increment its own counter")
	assert.Contains(t, out, `"stale_recovery_skip_count":0`,
		"a lazy-disabled skip must NOT inflate the stale-lock skip counter")
}
