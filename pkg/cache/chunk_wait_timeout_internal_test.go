package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testdata"
)

// TestStreamProgressiveChunks_ChunkWaitTimeoutIsConfigurable verifies that the
// per-chunk wait bound is operator-configurable: with a short SetChunkWaitTimeout
// an in-progress chunking that never produces chunks fails fast, rather than
// blocking for the 30s default (which on slow storage accumulates past the
// gateway timeout and surfaces to clients as a 504).
func TestStreamProgressiveChunks_ChunkWaitTimeoutIsConfigurable(t *testing.T) {
	t.Parallel()

	c, ctx := newCDCCacheForStreaming(t)

	c.SetChunkWaitTimeout(200 * time.Millisecond)

	// A nar_file that is legitimately "chunking in progress" (ChunkingStartedAt
	// set, TotalChunks=0) but for which no chunk ever arrives — the producer is
	// stalled, e.g. a slow network filesystem on another replica.
	_, err := c.dbClient.Ent().NarFile.Create().
		SetHash(testdata.Nar1.NarHash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(12345).
		SetChunkingStartedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeNone}

	_, rc, err := c.getNarFromChunks(ctx, &narURL)
	require.NoError(t, err)

	defer rc.Close()

	start := time.Now()
	body, readErr := readAllWithin(t, rc, 10*time.Second)
	elapsed := time.Since(start)

	require.Error(t, readErr,
		"an in-progress chunking that never produces a chunk must time out, not stream a short body")
	assert.Less(t, elapsed, 5*time.Second,
		"must honor the short configured chunk-wait timeout (200ms), not the 30s default")
	assert.Empty(t, body, "no bytes should be delivered when the chunk never arrives")
}
