package cache_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/chunker"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// TestGetNar_InFlightEagerCDC_CompressedRequestNotMislabeled reproduces GitHub
// issue #1398 ("input compression not recognized on 0.10.0-rc13").
//
// Topology (matches the reporter's single-node raspi5 deployment): eager CDC is
// enabled and in-flight staging is OFF (the local/sqlite locker is not
// distributed). A narinfo prefetch starts the eager-CDC download, which streams
// the upstream .nar.xz through a decompressor into a temp file holding the raw
// UNCOMPRESSED NAR (ds.tempFileCompression == none) and then chunks it.
//
// While that download/chunk window is open, a client whose narinfo still
// advertises Compression: xz requests `/nar/<hash>.nar.xz`. GetNar piggybacks on
// the in-flight ds and, at cache.go:1459, overwrites the requested compression
// with ds.tempFileCompression (none), then streams the decompressed temp file.
// The HTTP layer (server.go:897) sees Compression==none and may further wrap the
// body in transport zstd. Either way the client — which expects xz per its
// narinfo — receives bytes it cannot xz-decode and fails with
// "input compression not recognized".
//
// Correct behavior is EITHER:
//   - serve the request as real xz (nu.Compression == xz, body is valid xz), OR
//   - return storage.ErrNotFound so the client falls back to an upstream that
//     still has the original .nar.xz (the same graceful fallback the post-chunk
//     window already produces — the 404s in the issue log).
//
// The bug is that ncps does NEITHER: it answers 200 with the body relabeled to
// Compression: none. This test asserts the request is never served mislabeled.
func TestGetNar_InFlightEagerCDC_CompressedRequestNotMislabeled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	ts := testdata.NewTestServer(t, 40)
	t.Cleanup(ts.Close)

	c, _, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	// Eager CDC: chunk store present, CDC enabled, lazy chunking off.
	chunkStore, err := chunk.NewLocalStore(filepath.Join(dir, "chunks-store"))
	require.NoError(t, err)

	c.SetChunkStore(chunkStore)
	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

	// In-flight staging deliberately left OFF (single-node locker is not
	// distributed), so the staging path that already 404s a compressed request
	// from uncompressed staged bytes never engages — exactly the reporter's setup.

	// Keep the eager-CDC chunking window open long enough that the concurrent
	// .nar.xz request deterministically lands while the NAR is only present as the
	// decompressed temp file. The assertions are happens-before style, not
	// wall-clock based, so the generous delay only bounds a regression's wait.
	const chunkingDelay = 30 * time.Second

	realChunker, err := chunker.NewCDCChunker(1024, 4096, 8192)
	require.NoError(t, err)

	c.SetChunker(&slowChunker{real: realChunker, delay: chunkingDelay})

	// Serve real xz-compressed bytes for Nar2 so the streamed body can be checked
	// for genuine xz framing, and signal when the upstream NAR fetch begins so the
	// test can fire the concurrent request inside the in-flight window.
	originalContent := testhelper.MustRandString(50160)
	xzContent := compressXz(t, originalContent)

	narServing := make(chan struct{})

	var narServingOnce sync.Once

	nar2NARPath := "/nar/" + testdata.Nar2.NarHash + ".nar.xz"
	idx := ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != nar2NARPath {
			return false
		}

		narServingOnce.Do(func() { close(narServing) })

		_, _ = io.WriteString(w, xzContent)

		return true
	})

	t.Cleanup(func() { ts.RemoveMaybeHandler(idx) })

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	<-c.GetHealthChecker().Trigger()

	// Pull the narinfo. Under eager CDC this kicks off the background NAR prefetch
	// (the holder): it downloads the .nar.xz, decompresses into a none temp file,
	// and begins the (slow) chunking — the in-flight window the bug lives in.
	_, err = c.GetNarInfo(ctx, testdata.Nar2.NarInfoHash)
	require.NoError(t, err)

	// Wait until the holder is actually fetching the NAR upstream, guaranteeing the
	// concurrent request below piggybacks on the in-flight (none-temp) download
	// rather than starting its own.
	select {
	case <-narServing:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for the eager-CDC holder to begin fetching the NAR")
	}

	// A client whose narinfo still advertises xz requests the compressed NAR while
	// the holder is mid-window. THIS is the request that 404s or, when it hits the
	// bug, is served mislabeled as Compression: none.
	reqURL := nar.URL{Hash: testdata.Nar2.NarHash, Compression: nar.CompressionTypeXz}

	nu, _, rc, err := c.GetNar(ctx, reqURL)

	// Acceptable correct behavior #1: 404 -> client falls back to an upstream that
	// still has the real .nar.xz.
	if errors.Is(err, storage.ErrNotFound) {
		return
	}

	require.NoError(t, err, "GetNar(.nar.xz) returned an unexpected error")
	require.NotNil(t, rc)

	defer rc.Close()

	body, err := io.ReadAll(rc)
	require.NoError(t, err)

	// ncps answered the .nar.xz request with a body. Acceptable correct behavior
	// #2: it is served as real xz. The BUG relabels it to Compression: none and
	// streams the decompressed temp file instead.
	assert.Equalf(t, nar.CompressionTypeXz, nu.Compression,
		"BUG #1398: GetNar served a .nar.xz request as Compression:%s — it relabeled the "+
			"in-flight eager-CDC temp file (uncompressed) to none. A client that requested "+
			".nar.xz (its narinfo says xz) receives bytes it cannot xz-decode and fails with "+
			"'input compression not recognized'.", nu.Compression)

	// The served bytes must genuinely be xz that decompresses to the canonical NAR;
	// under the bug the body is the raw decompressed NAR, so xz-decoding it fails —
	// the client-visible "input compression not recognized".
	dr, err := nar.DecompressReader(ctx, bytes.NewReader(body), nar.CompressionTypeXz)
	require.NoError(t, err, "served .nar.xz body must be valid xz (input compression must be recognized)")

	defer dr.Close()

	got, err := io.ReadAll(dr)
	require.NoError(t, err, "served .nar.xz body must xz-decompress without 'input compression not recognized'")
	assert.Equal(t, originalContent, string(got), "decompressed .nar.xz body must equal the canonical NAR")
}
