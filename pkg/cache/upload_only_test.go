package cache_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

func TestGetNar_UploadOnly(t *testing.T) {
	t.Parallel()

	// Setup necessary components
	ts := testdata.NewTestServer(t, 40)
	t.Cleanup(ts.Close)

	db, localStore, _, _, cleanup := setupTestComponents(t)
	t.Cleanup(cleanup)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	// Wait for upstream caches to become available
	<-c.GetHealthChecker().Trigger()

	// Use Nar1 which exists in the upstream
	nu := nar.URL{
		Hash:        testdata.Nar1.NarHash,
		Compression: nar.CompressionTypeXz,
	}

	// First verify we can fetch it normally (sanity check)
	// We use a separate context for this to avoid any interference
	t.Run("sanity check - normal fetch works", func(t *testing.T) {
		t.Parallel()

		size, reader, err := c.GetNar(context.Background(), nu)
		require.NoError(t, err)
		require.NotNil(t, reader)
		reader.Close()
		// size can be -1 if streaming from upstream, or > 0 if known
		if size != -1 {
			assert.Positive(t, size)
		}
	})

	// Now try with UploadOnly
	t.Run("upload only - should fail if not in local store", func(t *testing.T) {
		t.Parallel()

		// Ensure it's not in the local store first (might have been pulled by sanity check)
		// Since we share the cache instance and store in the test setup, we need to be careful.
		// Actually, the sanity check pulled it into the store.
		// So let's pick another NAR, say Nar2, for this specific test case.
		nu2 := nar.URL{
			Hash:        testdata.Nar2.NarHash,
			Compression: nar.CompressionTypeXz,
		}

		ctx := cache.WithUploadOnly(context.Background())
		_, _, err := c.GetNar(ctx, nu2)
		assert.ErrorIs(t, err, storage.ErrNotFound,
			"should return ErrNotFound when item is only upstream and UploadOnly is set")
	})
}

func TestGetNarInfo_UploadOnly(t *testing.T) {
	t.Parallel()

	// Setup necessary components
	ts := testdata.NewTestServer(t, 40)
	t.Cleanup(ts.Close)

	db, localStore, _, _, cleanup := setupTestComponents(t)
	t.Cleanup(cleanup)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	// Wait for upstream caches to become available
	<-c.GetHealthChecker().Trigger()

	// Use Nar2 which exists upstream but not locally yet
	hash := testdata.Nar2.NarInfoHash

	// Try with UploadOnly
	t.Run("upload only - should fail if not in local store", func(t *testing.T) {
		t.Parallel()

		ctx := cache.WithUploadOnly(context.Background())
		_, err := c.GetNarInfo(ctx, hash)
		assert.ErrorIs(t, err, storage.ErrNotFound,
			"should return ErrNotFound when item is only upstream and UploadOnly is set")
	})
}

// TestPutNarInfo_DoesNotTriggerNARStreaming verifies that calling PutNarInfo when the
// corresponding NAR is already in the local store does not trigger a NAR streaming
// pipeline (pipe + background goroutine), which would log a spurious "pipe closed"
// error when the reader is immediately closed after getting the file size.
//
// Regression test for: PUT /upload/<hash>.narinfo logging "pipe closed during NAR
// copy (client likely disconnected)" for the same trace_id as the upload operation.
//
// The bug manifests as a data race: the background goroutine spawned by GetNar()
// writes "pipe closed" to the log buffer concurrently with the test reading it.
// With -race, this causes the test to fail. After the fix, no goroutine is spawned,
// no race occurs, and the "pipe closed" message is never logged.
func TestPutNarInfo_DoesNotTriggerNARStreaming(t *testing.T) {
	t.Parallel()

	db, localStore, _, _, cleanup := setupTestComponents(t)
	t.Cleanup(cleanup)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

	// Step 1: Store the NAR locally so checkAndFixNarInfo finds it in the store.
	narURL := nar.URL{
		Hash:        testdata.Nar1.NarHash,
		Compression: testdata.Nar1.NarCompression,
	}
	require.NoError(t, c.PutNar(context.Background(), narURL, io.NopCloser(strings.NewReader(testdata.Nar1.NarText))))

	// Step 2: Call PutNarInfo with a context whose logger writes to an unprotected
	// buffer. If the buggy code path runs (GetNar → serveNarFromStorageViaPipe →
	// background goroutine), that goroutine will write to this buffer concurrently,
	// causing a data race detected by -race.
	//
	// After the fix, no goroutine is spawned, no concurrent write occurs, and the
	// buffer read below is safe.
	var logBuf bytes.Buffer

	logger := zerolog.New(&logBuf)
	ctx := logger.WithContext(context.Background())

	err = c.PutNarInfo(ctx, testdata.Nar1.NarInfoHash, io.NopCloser(strings.NewReader(testdata.Nar1.NarInfoText)))
	require.NoError(t, err)

	// Wait for all background work spawned by PutNarInfo to complete.
	// Note: this waits for backgroundWG goroutines (e.g. checkAndFixNarInfo's
	// detached context), but not for SafeGo goroutines spawned by the buggy
	// GetNar → serveNarFromStorageViaPipe path.
	c.Close()

	// Step 3: Read the buffer. If the bug is present, the background goroutine
	// from serveNarFromStorageViaPipe is still running and writing to logBuf
	// concurrently — the race detector will flag this read as a data race,
	// failing the test. After the fix, this read is safe.
	assert.NotContains(t, logBuf.String(), "pipe closed during NAR copy",
		"PutNarInfo should not trigger NAR streaming when checking file size")
}
