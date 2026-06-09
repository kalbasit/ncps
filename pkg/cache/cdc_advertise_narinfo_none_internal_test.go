package cache

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/testdata"
)

// TestIsEagerCDC pins the gate predicate that scopes predictive narinfo-none
// normalization to eager CDC only: CDC writes enabled AND a chunk store present
// AND lazy chunking disabled. Lazy mode retains the whole upstream-compressed
// file and must keep serving .nar.xz, so it is NOT eager.
func TestIsEagerCDC(t *testing.T) {
	t.Parallel()

	newCacheWithChunkStore := func(t *testing.T) *Cache {
		t.Helper()

		cs, err := chunk.NewLocalStore(filepath.Join(t.TempDir(), "chunks"))
		require.NoError(t, err)

		c := &Cache{}
		c.SetChunkStore(cs)

		return c
	}

	t.Run("CDC disabled is not eager", func(t *testing.T) {
		t.Parallel()

		c := &Cache{}
		require.False(t, c.isEagerCDC())
	})

	t.Run("eager CDC (enabled, lazy off) is eager", func(t *testing.T) {
		t.Parallel()

		c := newCacheWithChunkStore(t)
		require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))
		require.False(t, c.GetCDCLazyChunkingEnabled(), "precondition: lazy disabled")
		require.True(t, c.isEagerCDC())
	})

	t.Run("lazy CDC is not eager", func(t *testing.T) {
		t.Parallel()

		c := newCacheWithChunkStore(t)
		require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))
		c.SetCDCLazyChunking(true, 1)
		require.False(t, c.isEagerCDC())
	})

	t.Run("CDC enabled but no chunk store is not eager", func(t *testing.T) {
		t.Parallel()

		c := &Cache{}
		require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))
		require.False(t, c.isEagerCDC())
	})
}

// storeLegacyXzNarInfo stores an xz NAR whole-file and its xz narinfo while CDC
// is disabled, reproducing a "legacy" row persisted before predictive-none
// shipped: Compression: xz, URL nar/<hash>.nar.xz.
func storeLegacyXzNarInfo(t *testing.T, c *Cache) {
	t.Helper()

	require.False(t, c.isCDCEnabled(), "precondition: store the legacy row with CDC off")

	ctx := newContext()
	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: testdata.Nar1.NarCompression}
	require.NoError(t, c.PutNar(ctx, narURL, io.NopCloser(strings.NewReader(testdata.Nar1.NarText))))
	require.NoError(t, c.PutNarInfo(ctx, testdata.Nar1.NarInfoHash,
		io.NopCloser(strings.NewReader(testdata.Nar1.NarInfoText))))
}

// TestGetNarInfo_EagerCDC_NormalizesLegacyXzRowToNone is the serve-time backstop:
// a narinfo persisted as xz before predictive-none shipped must be normalized to
// none in-memory under eager CDC even when the NAR is NOT yet chunked
// (HasNarInChunks false) — so legacy rows benefit without a migration/backfill.
func TestGetNarInfo_EagerCDC_NormalizesLegacyXzRowToNone(t *testing.T) {
	t.Parallel()

	c, _ := buildPullCache(t)
	storeLegacyXzNarInfo(t, c)

	// Enable eager CDC AFTER the legacy row exists.
	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))
	require.True(t, c.isEagerCDC())

	ni, err := c.GetNarInfo(newContext(), testdata.Nar1.NarInfoHash)
	require.NoError(t, err)
	assert.Equal(t, nar.CompressionTypeNone.String(), ni.Compression,
		"eager CDC must normalize a legacy xz narinfo to none at serve time")
	assert.True(t, strings.HasSuffix(ni.URL, ".nar"), "URL must be nar/<hash>.nar; got %q", ni.URL)
	assert.False(t, strings.HasSuffix(ni.URL, ".nar.xz"), "URL must not be compressed; got %q", ni.URL)
}

// TestGetNarInfo_LazyCDC_LegacyXzRowNotChunkedStaysXz is the lazy gate guard: a
// legacy xz narinfo that is NOT yet chunked must stay xz under lazy CDC, because
// the whole xz file is still servable as .nar.xz.
func TestGetNarInfo_LazyCDC_LegacyXzRowNotChunkedStaysXz(t *testing.T) {
	t.Parallel()

	c, _ := buildPullCache(t)
	storeLegacyXzNarInfo(t, c)

	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))
	c.SetCDCLazyChunking(true, 1)
	require.False(t, c.isEagerCDC())

	ni, err := c.GetNarInfo(newContext(), testdata.Nar1.NarInfoHash)
	require.NoError(t, err)
	assert.Equal(t, nar.CompressionTypeXz.String(), ni.Compression,
		"lazy CDC must keep a not-yet-chunked legacy narinfo as xz")
	assert.True(t, strings.HasSuffix(ni.URL, ".nar.xz"), "URL must stay compressed; got %q", ni.URL)
}

// TestGetNarInfo_CDCDisabled_XzRowStaysXz guards the CDC-disabled path: even with
// a chunk store present (drain-capable), a non-chunked xz narinfo is not
// normalized when CDC writes are off.
func TestGetNarInfo_CDCDisabled_XzRowStaysXz(t *testing.T) {
	t.Parallel()

	c, _ := buildPullCache(t) // chunk store present, CDC never enabled
	storeLegacyXzNarInfo(t, c)

	ni, err := c.GetNarInfo(newContext(), testdata.Nar1.NarInfoHash)
	require.NoError(t, err)
	assert.Equal(t, nar.CompressionTypeXz.String(), ni.Compression,
		"CDC disabled must not normalize a non-chunked xz narinfo")
}

// TestGetNarInfo_EagerCDC_ColdClientReceivesNone is the cold/triggering-client
// end-to-end path: a fresh GetNarInfo (no pre-existing nar_file row) under eager
// CDC returns a narinfo advertising none, so the client requests .nar.
func TestGetNarInfo_EagerCDC_ColdClientReceivesNone(t *testing.T) {
	t.Parallel()

	c, _ := setupCDCPullCache(t, false) // eager, working upstream, prefetch enabled

	ni, err := c.GetNarInfo(newContext(), testdata.Nar1.NarInfoHash)
	require.NoError(t, err)
	assert.Equal(t, nar.CompressionTypeNone.String(), ni.Compression,
		"cold eager-CDC client must receive Compression: none")
	assert.True(t, strings.HasSuffix(ni.URL, ".nar"), "URL must be nar/<hash>.nar; got %q", ni.URL)
}

// The predictive-none re-download safety invariant (a .nar request whose bytes are
// absent re-downloads and serves the correct DECOMPRESSED content rather than a
// terminal 404) is covered end-to-end, with real xz fixtures, by
// testCDCBackingLessRecordRecoversAfterTransientFailure in phantom_recovery_test.go:
// with predictive-none persisted, GetNar(.nar) recovers via lookupPreferredUpstreamURL
// (re-fetch upstream narinfo → xz URL → re-download → decompress) and serves the full
// payload. That mechanism is verified there, so it is not duplicated here.
