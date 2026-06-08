package cache

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// TestGetNarFromChunks_PrefersStagingDuringChunkingWindow verifies that during the
// eager-CDC chunking window (total_chunks == 0), when in-flight staging parts are
// available, the read path serves the complete NAR from staging instead of the
// fragile progressive-chunk path (#1289).
func TestGetNarFromChunks_PrefersStagingDuringChunkingWindow(t *testing.T) {
	t.Parallel()

	c, ctx := newCDCCacheForStreaming(t)

	c.SetInflightStaging(true, 5*time.Minute, 4, true)
	// A short progressive wait so that, if staging were wrongly skipped, the test
	// would surface a fast timeout instead of the staged bytes.
	c.SetChunkWaitTimeout(200 * time.Millisecond)

	const content = "the complete staged nar bytes!!" // 31 bytes

	hash := testdata.Nar1.NarHash

	// A nar_file that is legitimately mid-chunking: total_chunks == 0 and no chunk
	// will ever arrive on the progressive path.
	_, err := c.dbClient.Ent().NarFile.Create().
		SetHash(hash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(uint64(len(content))).
		SetTotalChunks(0).
		SetChunkingStartedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	// Stage the whole NAR as part-objects (uncompressed, matching the request).
	nParts := putStagingParts(t, c.narStore, hash, content, 4)
	require.NoError(t, c.markStagingRequested(ctx, hash))
	require.NoError(t, c.advanceStagingParts(ctx, hash, nParts, nar.CompressionTypeNone.String()))
	require.NoError(t, c.markStagingComplete(ctx, hash))

	narURL := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}

	_, rc, err := c.getNarFromChunks(ctx, &narURL)
	require.NoError(t, err)

	defer rc.Close()

	body, readErr := readAllWithin(t, rc, 5*time.Second)
	require.NoError(t, readErr, "staging serve must complete, not stall on progressive chunks")
	assert.Equal(t, content, string(body))
}

// TestGetNarFromChunks_NoStagingFallsBackToProgressive verifies that with
// total_chunks == 0 and no staging parts available, the read path falls back to
// the progressive-chunk path as before (it does not divert into a broken staging
// serve).
func TestGetNarFromChunks_NoStagingFallsBackToProgressive(t *testing.T) {
	t.Parallel()

	c, ctx := newCDCCacheForStreaming(t)

	// Feature enabled, but no staging_state row will exist for the hash.
	c.SetInflightStaging(true, 5*time.Minute, 4, true)
	c.SetChunkWaitTimeout(200 * time.Millisecond)

	hash := testdata.Nar1.NarHash

	_, err := c.dbClient.Ent().NarFile.Create().
		SetHash(hash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(12345).
		SetTotalChunks(0).
		SetChunkingStartedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	narURL := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}

	_, rc, err := c.getNarFromChunks(ctx, &narURL)
	require.NoError(t, err)

	defer rc.Close()

	// The progressive path is taken: with no chunk ever arriving it times out
	// (proving the request was not served from a non-existent staging set).
	_, readErr := readAllWithin(t, rc, 5*time.Second)
	require.Error(t, readErr, "with no staging the progressive path must be used and time out")
	assert.NotErrorIs(t, readErr, io.EOF)
}

// TestGetNarFromChunks_SteadyStateIgnoresStaging verifies that once chunking is
// complete (total_chunks > 0), serving proceeds from chunks regardless of any
// staging parts that may still linger for the hash.
func TestGetNarFromChunks_SteadyStateIgnoresStaging(t *testing.T) {
	t.Parallel()

	c, ctx := newCDCCacheForStreaming(t)

	c.SetInflightStaging(true, 5*time.Minute, 4, true)

	const chunkContent = "the real chunked content served from chunks"

	hash := testhelper.MustRandBase32NarHash()

	// Store a fully-chunked NAR (total_chunks > 0).
	narURL := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}
	require.NoError(t, c.PutNar(ctx, narURL, io.NopCloser(strings.NewReader(chunkContent))))

	// Lingering staging parts with DIFFERENT bytes must be ignored in steady state.
	nParts := putStagingParts(t, c.narStore, hash, "STALE STAGING BYTES THAT MUST NOT BE SERVED", 4)
	require.NoError(t, c.markStagingRequested(ctx, hash))
	require.NoError(t, c.advanceStagingParts(ctx, hash, nParts, nar.CompressionTypeNone.String()))
	require.NoError(t, c.markStagingComplete(ctx, hash))

	_, rc, err := c.getNarFromChunks(ctx, &narURL)
	require.NoError(t, err)

	defer rc.Close()

	body, readErr := readAllWithin(t, rc, 5*time.Second)
	require.NoError(t, readErr)
	assert.Equal(t, chunkContent, string(body), "steady-state serving must come from chunks, not staging")
}
