package cache

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/testdata"
)

// readAllWithin reads rc to completion, failing the test if that takes longer
// than d (so a streaming path that hangs instead of erroring is caught).
func readAllWithin(t *testing.T, rc io.ReadCloser, d time.Duration) ([]byte, error) {
	t.Helper()

	type result struct {
		body []byte
		err  error
	}

	done := make(chan result, 1)

	go func() {
		body, err := io.ReadAll(rc)
		done <- result{body: body, err: err}
	}()

	select {
	case r := <-done:
		return r.body, r.err
	case <-time.After(d):
		t.Fatalf("reading the progressive-chunk stream did not finish within %s (it hung instead of erroring)", d)

		return nil, nil
	}
}

func newCDCCacheForStreaming(t *testing.T) (*Cache, context.Context) {
	t.Helper()

	c, _, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	chunkStore, err := chunk.NewLocalStore(filepath.Join(dir, "chunks-store"))
	require.NoError(t, err)
	c.SetChunkStore(chunkStore)

	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

	return c, context.Background()
}

// TestGetNarFromChunks_AbortedChunkingErrorsNotShortBody verifies that a
// nar_file in the aborted state (total_chunks=0, chunking_started_at=NULL) makes
// progressive streaming surface an error rather than closing a short, successful
// body.
func TestGetNarFromChunks_AbortedChunkingErrorsNotShortBody(t *testing.T) {
	t.Parallel()

	c, ctx := newCDCCacheForStreaming(t)

	_, err := c.dbClient.Ent().NarFile.Create().
		SetHash(testdata.Nar1.NarHash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(12345).
		Save(ctx)
	require.NoError(t, err)

	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeNone}

	_, rc, err := c.getNarFromChunks(ctx, &narURL)
	require.NoError(t, err)

	defer rc.Close()

	body, readErr := readAllWithin(t, rc, 10*time.Second)
	require.ErrorIs(t, readErr, storage.ErrNotFound,
		"aborted chunking must surface a not-found read error, not a successful short body")
	assert.Empty(t, body, "no bytes should be delivered for an aborted NAR")
}

// TestGetNarFromChunks_StalledChunkingFailsFast verifies that a nar_file whose
// chunking lock is stale (chunking_started_at older than cdcChunkingLockTTL with
// total_chunks still 0 and no chunks) fails fast with an error instead of
// blocking on the per-chunk wait, and never yields a short successful body.
func TestGetNarFromChunks_StalledChunkingFailsFast(t *testing.T) {
	t.Parallel()

	c, ctx := newCDCCacheForStreaming(t)

	stale := time.Now().Add(-2 * cdcChunkingLockTTL)
	_, err := c.dbClient.Ent().NarFile.Create().
		SetHash(testdata.Nar1.NarHash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(12345).
		SetChunkingStartedAt(stale).
		Save(ctx)
	require.NoError(t, err)

	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeNone}

	_, rc, err := c.getNarFromChunks(ctx, &narURL)
	require.NoError(t, err)

	defer rc.Close()

	// A stale lock must fail well within the per-chunk wait (30s); the read must
	// not block until that timeout.
	body, readErr := readAllWithin(t, rc, 10*time.Second)
	require.ErrorIs(t, readErr, storage.ErrNotFound,
		"a stale stalled stream must surface a not-found-style error, not a successful short body")
	assert.Empty(t, body, "no bytes should be delivered for a stalled NAR")
}
