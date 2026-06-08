package cache

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/local"
)

// newCompletedStagingDownloadState writes content to a temp file under dir and
// returns a downloadState that looks like a holder which has finished writing the
// whole NAR to its temp file (finalSize set), so a producer tailing it reads the
// full content and then sees EOF.
func newCompletedStagingDownloadState(
	t *testing.T,
	dir, content string,
	comp nar.CompressionType,
) *downloadState {
	t.Helper()

	f, err := os.CreateTemp(dir, "staging-src-*.nar")
	require.NoError(t, err)

	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	ds := newDownloadState()
	ds.assetPath = f.Name()
	ds.tempFileCompression = comp
	ds.bytesWritten = int64(len(content))
	ds.finalSize = int64(len(content))
	ds.startOnce.Do(func() { close(ds.start) })

	return ds
}

// readStagingParts reassembles part-objects 0..n-1 for hash into a single string.
func readStagingParts(t *testing.T, store *local.Store, hash string, n int64) string {
	t.Helper()

	var out []byte

	for i := int64(0); i < n; i++ {
		rc, err := store.GetStagingPart(context.Background(), hash, i)
		require.NoError(t, err, "part %d must be readable", i)

		b, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())

		out = append(out, b...)
	}

	return string(out)
}

// TestStageInflightNar_ActivatesOnRequest verifies that, with the feature enabled
// and a cross-pod waiter's staging request already recorded, the holder stages the
// in-flight NAR as ordered part-objects and advances parts_available so the bytes
// reassemble exactly.
func TestStageInflightNar_ActivatesOnRequest(t *testing.T) {
	t.Parallel()

	c, _, store, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	// Distributed locker + small part size so the NAR spans multiple parts.
	c.SetInflightStaging(true, 5*time.Minute, 4, true)

	ctx := context.Background()

	const (
		hash    = "0123456789abcdef0123456789abcdef"
		content = "abcdefghij" // 10 bytes -> parts of 4, 4, 2
	)

	ds := newCompletedStagingDownloadState(t, dir, content, nar.CompressionTypeNone)

	// A waiter recorded a staging request before the holder runs.
	require.NoError(t, c.markStagingRequested(ctx, hash))

	c.stageInflightNar(ctx, hash, ds)

	st, err := c.getStagingState(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, st)
	assert.Equal(t, int64(3), st.PartsAvailable, "10 bytes at part size 4 = 3 parts")
	assert.Equal(t, stagingStatusComplete, st.Status,
		"a fully-staged NAR must end in the terminal 'complete' status")

	assert.Equal(t, content, readStagingParts(t, store, hash, st.PartsAvailable))
}

// TestStageInflightNar_BackfillsThenAppends verifies activation mid-download: the
// holder backfills the already-written prefix as parts from index zero, then
// appends parts as new bytes arrive, recording the temp file's compression. The
// reassembled parts cover the full NAR byte range exactly once.
func TestStageInflightNar_BackfillsThenAppends(t *testing.T) {
	t.Parallel()

	c, _, store, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	c.SetInflightStaging(true, 5*time.Minute, 4, true)

	ctx := context.Background()

	const hash = "11112222333344445555666677778888"

	content := []byte("abcdefghijklmnopqrst") // 20 bytes -> 5 parts of 4

	f, err := os.CreateTemp(dir, "src-*.nar")
	require.NoError(t, err)

	ds := newDownloadState()
	ds.assetPath = f.Name()
	ds.tempFileCompression = nar.CompressionTypeXz

	// The holder has already written an 8-byte prefix before activation.
	_, err = f.Write(content[:8])
	require.NoError(t, err)

	ds.bytesWritten = 8
	ds.startOnce.Do(func() { close(ds.start) })

	// A waiter requests staging mid-download.
	require.NoError(t, c.markStagingRequested(ctx, hash))

	done := make(chan struct{})

	go func() {
		defer close(done)

		c.stageInflightNar(ctx, hash, ds)
	}()

	// Drive the rest of the download one byte at a time, broadcasting progress.
	for i := 8; i < len(content); i++ {
		_, werr := f.Write(content[i : i+1])
		require.NoError(t, werr)

		ds.mu.Lock()
		ds.bytesWritten++
		ds.mu.Unlock()
		ds.cond.Broadcast()
	}

	ds.mu.Lock()
	ds.finalSize = int64(len(content))
	ds.mu.Unlock()
	ds.cond.Broadcast()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("staging producer did not finish")
	}

	require.NoError(t, f.Close())

	st, err := c.getStagingState(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, st)
	assert.Equal(t, int64(5), st.PartsAvailable, "20 bytes at part size 4 = 5 parts")
	assert.Equal(t, stagingStatusComplete, st.Status)
	assert.Equal(t, nar.CompressionTypeXz.String(), st.Compression,
		"staged compression must match the temp file's native compression")
	assert.Equal(t, string(content), readStagingParts(t, store, hash, st.PartsAvailable),
		"reassembled parts must cover the full NAR byte range exactly once")
}

// TestStageInflightNar_NoRequestNoStaging verifies the zero-overhead path: when no
// waiter ever records a request and the download ends, the holder stages nothing.
func TestStageInflightNar_NoRequestNoStaging(t *testing.T) {
	t.Parallel()

	c, _, store, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	c.SetInflightStaging(true, 5*time.Minute, 4, true)

	ctx := context.Background()

	const hash = "ffffffffffffffffffffffffffffffff"

	ds := newCompletedStagingDownloadState(t, dir, "abcdefghij", nar.CompressionTypeNone)
	// Download already finished and no waiter ever appeared.
	ds.doneOnce.Do(func() { close(ds.done) })

	c.stageInflightNar(ctx, hash, ds)

	st, err := c.getStagingState(ctx, hash)
	require.NoError(t, err)
	assert.Nil(t, st, "no staging_state should be created without a waiter")

	_, err = store.GetStagingPart(ctx, hash, 0)
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

// TestStageInflightNar_DisabledDoesNothing verifies that when the feature is
// disabled, the holder never stages even if a request marker is present.
func TestStageInflightNar_DisabledDoesNothing(t *testing.T) {
	t.Parallel()

	c, _, store, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	// Feature left disabled (default).
	ctx := context.Background()

	const hash = "aaaabbbbccccddddeeeeffff00001111"

	ds := newCompletedStagingDownloadState(t, dir, "abcdefghij", nar.CompressionTypeNone)

	require.NoError(t, c.markStagingRequested(ctx, hash))

	c.stageInflightNar(ctx, hash, ds)

	st, err := c.getStagingState(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, st)
	assert.Equal(t, stagingStatusRequested, st.Status, "status must stay 'requested' when disabled")
	assert.Equal(t, int64(0), st.PartsAvailable)

	_, err = store.GetStagingPart(ctx, hash, 0)
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

// TestProduceStagingParts_NoOpWhenDownloadAlreadyCompleted verifies that when the
// holder's temp file is already gone — the post-completion cleanup goroutine
// (cache.go) removes ds.assetPath once the download and all readers finish — the
// producer treats the missing temp file as a clean no-op rather than erroring.
// This is the fast-download case the contention e2e surfaced: a cross-pod request
// observed at completion led the producer to open an already-removed temp file.
func TestProduceStagingParts_NoOpWhenDownloadAlreadyCompleted(t *testing.T) {
	t.Parallel()

	c, _, store, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	c.SetInflightStaging(true, 5*time.Minute, 4, true)

	ctx := context.Background()

	const hash = "aaaabbbbccccddddeeeeffff22223333"

	// Create then remove a temp file so ds.assetPath points at a path that no
	// longer exists — exactly what the post-completion cleanup leaves behind.
	f, err := os.CreateTemp(dir, "gone-*.nar")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, os.Remove(f.Name()))

	ds := newDownloadState()
	ds.assetPath = f.Name()
	ds.finalSize = 10
	ds.startOnce.Do(func() { close(ds.start) })
	ds.doneOnce.Do(func() { close(ds.done) })

	err = c.produceStagingParts(ctx, hash, ds)
	require.NoError(t, err,
		"a missing temp file means the download already completed; staging must "+
			"no-op, not error")

	_, err = store.GetStagingPart(ctx, hash, 0)
	assert.ErrorIs(t, err, storage.ErrNotFound, "no parts should be written on the no-op path")
}
