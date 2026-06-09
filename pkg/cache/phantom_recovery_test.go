package cache_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"

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

// testCDCBackingLessRecordRecoversAfterTransientFailure reproduces the production
// "phantom nar_file" defect: a nar_file row is created at narinfo-fetch time
// (total_chunks=0, chunking_started_at=NULL) before any bytes exist; when the
// background NAR download then fails transiently, the row survives backing-less.
// A subsequent GET /nar MUST treat that row as a cache miss and re-download from
// upstream — never return a terminal 404 — and the successful re-download must
// heal the record so later requests are served from cache.
func testCDCBackingLessRecordRecoversAfterTransientFailure(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ts := testdata.NewTestServer(t, 40)
		t.Cleanup(ts.Close)

		c, _, _, dir, _, cleanup := factory(t)
		t.Cleanup(cleanup)

		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

		realChunker, err := chunker.NewCDCChunker(1024, 4096, 8192)
		require.NoError(t, err)
		c.SetChunker(realChunker)

		// Serve real xz bytes upstream. Under eager CDC the narinfo advertises a
		// predictive none URL, so GetNar re-downloads the xz, decompresses it, and
		// serves the uncompressed payload — originalContent is the full reference.
		originalContent := testhelper.MustRandString(50160)
		xzContent := compressXz(t, originalContent)
		narXzPath := "/nar/" + testdata.Nar2.NarHash + ".nar.xz"

		// failUpstream toggles the NAR endpoint between a transient 500 and serving
		// the real bytes. The narinfo endpoint always succeeds (handled by default).
		var failUpstream atomic.Bool

		failUpstream.Store(true)

		idx := ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != narXzPath {
				return false
			}

			if failUpstream.Load() {
				w.WriteHeader(http.StatusInternalServerError)

				return true
			}

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

		// Fetch the narinfo. This synchronously creates the placeholder nar_file row
		// and kicks off a background NAR download that fails (upstream returns 500).
		ni, err := c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
		require.NoError(t, err)

		// Address the NAR via exactly what the narinfo advertises. Under eager CDC,
		// store-time normalization advertises a predictive none URL, so this is
		// nar/<hash>.nar. The phantom-recovery invariant (a backing-less row
		// re-downloads from upstream and never terminal-404s) holds regardless of
		// which compression the URL names.
		narURL, err := nar.ParseURL(ni.URL)
		require.NoError(t, err)

		// While upstream is broken, the backing-less row must NOT short-circuit to a
		// terminal 404: GetNar attempts an upstream download and surfaces the transient
		// failure instead.
		_, _, rc, err := c.GetNar(context.Background(), narURL)
		if err == nil {
			rc.Close()
			t.Fatal("expected an error while upstream is failing, got success")
		}

		require.NotErrorIs(t, err, storage.ErrNotFound,
			"a transient upstream failure must not surface as a terminal 404 (got %v)", err)

		// Upstream recovers.
		failUpstream.Store(false)

		// The next request must re-download and serve the full, non-truncated NAR.
		_, _, rc, err = c.GetNar(context.Background(), narURL)
		require.NoError(t, err, "backing-less record must re-download once upstream recovers")

		// The narinfo advertises the predictive none URL, so GetNar decompresses the
		// re-downloaded xz and serves the full uncompressed payload. Assert the body
		// matches the complete originalContent (exact bytes and length) so a
		// truncated re-download cannot pass.
		body, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		require.Len(t, body, len(originalContent),
			"recovered NAR must be the full, non-truncated decompressed payload")
		assert.Equal(t, originalContent, string(body),
			"recovered NAR body must be the full decompressed payload")

		// The record is now healed: a subsequent request is served from cache.
		_, _, rc, err = c.GetNar(context.Background(), narURL)
		require.NoError(t, err)

		body, err = io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		require.Len(t, body, len(originalContent),
			"healed NAR must continue to serve the full, non-truncated payload")
		assert.Equal(t, originalContent, string(body),
			"healed NAR must continue to serve the full content from cache")
	}
}

// testCDCBackingLessRecordGenuine404ReturnsNotFound verifies the inverse: when the
// NAR is genuinely absent upstream (404), GetNar must surface storage.ErrNotFound
// rather than re-trying forever or hanging.
func testCDCBackingLessRecordGenuine404ReturnsNotFound(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ts := testdata.NewTestServer(t, 40)
		t.Cleanup(ts.Close)

		c, _, _, dir, _, cleanup := factory(t)
		t.Cleanup(cleanup)

		chunkStoreDir := filepath.Join(dir, "chunks-store")
		chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
		require.NoError(t, err)

		c.SetChunkStore(chunkStore)
		require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

		narXzPath := "/nar/" + testdata.Nar2.NarHash + ".nar.xz"
		narPath := "/nar/" + testdata.Nar2.NarHash + ".nar"

		// The NAR is genuinely absent: every variant of its path returns 404. The
		// narinfo endpoint is left to the default handler so the narinfo itself exists.
		idx := ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == narXzPath || r.URL.Path == narPath {
				w.WriteHeader(http.StatusNotFound)

				return true
			}

			return false
		})

		t.Cleanup(func() { ts.RemoveMaybeHandler(idx) })

		uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
			PublicKeys: testdata.PublicKeys(),
		})
		require.NoError(t, err)

		c.AddUpstreamCaches(newContext(), uc)
		<-c.GetHealthChecker().Trigger()

		_, err = c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
		require.NoError(t, err)

		narURL := nar.URL{Hash: testdata.Nar2.NarHash, Compression: nar.CompressionTypeNone}

		_, _, rc, err := c.GetNar(context.Background(), narURL)
		if err == nil {
			rc.Close()
			t.Fatal("expected ErrNotFound for a genuinely absent NAR, got success")
		}

		// The server NAR handler maps both sentinels to HTTP 404 (server.go), so a
		// genuinely absent NAR must surface as one of them — never a generic error
		// that would become a 500.
		assert.True(t,
			errors.Is(err, storage.ErrNotFound) || errors.Is(err, upstream.ErrNotFound),
			"a genuinely absent NAR must surface a not-found sentinel (got %v)", err)
	}
}
