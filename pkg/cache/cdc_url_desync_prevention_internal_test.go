package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"
	locklocal "github.com/kalbasit/ncps/pkg/lock/local"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// buildPullCache builds a cache wired to a test upstream with a chunk store
// present but CDC NOT yet enabled, so callers can choose the mode (or leave CDC
// off) per test. The chunk store is set so enabling CDC later is a one-liner.
func buildPullCache(t *testing.T) (*Cache, *database.Client) {
	t.Helper()

	ctx := newContext()

	ts := testdata.NewTestServer(t, 40)
	t.Cleanup(ts.Close)

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	dbClient, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbClient.Close() })

	localStore, err := local.New(ctx, dir)
	require.NoError(t, err)

	c, err := New(ctx, cacheName, dbClient, localStore, localStore, localStore, "",
		locklocal.NewLocker(), locklocal.NewRWLocker(), downloadLockTTL, downloadPollTimeout, cacheLockTTL)
	require.NoError(t, err)
	t.Cleanup(c.Close)

	chunkStore, err := chunk.NewLocalStore(filepath.Join(dir, "chunks-store"))
	require.NoError(t, err)
	c.SetChunkStore(chunkStore)

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	c.SetRecordAgeIgnoreTouch(0)

	<-c.GetHealthChecker().Trigger()

	return c, dbClient
}

// setupCDCPullCache builds a cache wired to a test upstream, with CDC enabled in
// either eager (lazy=false) or lazy (lazy=true) mode. The background NAR prefetch
// is left to the caller (disable it per-request to exercise the chunking-never-ran
// window that store-time narinfo normalization must handle).
func setupCDCPullCache(t *testing.T, lazy bool) (*Cache, *database.Client) {
	t.Helper()

	c, dbClient := buildPullCache(t)

	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

	if lazy {
		c.SetCDCLazyChunking(true, 1)
	}

	require.Equal(t, lazy, c.GetCDCLazyChunkingEnabled(), "precondition: CDC mode set as requested")

	return c, dbClient
}

// TestPullNarInfo_EagerCDC_AdvertisesNoneURL asserts the predictive-none policy:
// under eager CDC, pulling an xz upstream narinfo persists a Compression=none /
// nar/<hash>.nar narinfo (with FileHash/FileSize nulled) BEFORE any chunk exists,
// so clients always request the uncompressed .nar and never .nar.xz.
//
// This REVERSES the earlier "Fix B" stance (persist the truthful xz URL until the
// NAR is genuinely chunked). That stance guarded against an orphan-narinfo window:
// a none URL persisted while the NAR was stored xz-only would 404 a GET of
// /nar/<hash>.nar and abort a `nix copy` reference check. Two later developments
// close that window, making predictive-none safe and bringing the pull path into
// parity with PutNarInfo (which already normalizes CDC narinfos to none):
//
//   - narServability now routes a non-servable .nar request to an upstream
//     re-download rather than a terminal 404 (validated: a GetNar(none) on an
//     orphan eager-CDC narinfo re-downloads and serves the decompressed NAR).
//   - in-flight staging serves the uncompressed bytes across the pull+chunk
//     window for cross-pod readers.
//
// NAR prefetch is disabled so chunking never runs — the exact window the old
// stance protected and the new policy must serve correctly.
func TestPullNarInfo_EagerCDC_AdvertisesNoneURL(t *testing.T) {
	t.Parallel()

	c, dbClient := setupCDCPullCache(t, false)

	reqCtx := withNarPrefetchDisabled(newContext())
	_, _ = c.GetNarInfo(reqCtx, testdata.Nar1.NarInfoHash)

	row, err := dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).
		Only(newContext())
	require.NoError(t, err, "the pull must persist the narinfo row")

	require.NotNil(t, row.URL, "persisted narinfo must have a URL")
	require.True(t, strings.HasSuffix(*row.URL, ".nar"),
		"eager-CDC pull must persist a predictive none URL (nar/<hash>.nar); got %q", *row.URL)
	require.False(t, strings.HasSuffix(*row.URL, ".nar.xz"),
		"eager-CDC pull must NOT persist a compressed URL; got %q", *row.URL)
	require.NotNil(t, row.Compression, "persisted narinfo must record a compression")
	require.Equal(t, nar.CompressionTypeNone.String(), *row.Compression,
		"eager-CDC pull must advertise Compression: none predictively")
	require.Nil(t, row.FileHash, "predictive none narinfo must null FileHash")

	if row.FileSize != nil {
		require.Zero(t, *row.FileSize, "predictive none narinfo must null/zero FileSize")
	}
}

// TestPullNarInfo_LazyCDC_RetainsXzURL is the gate guard: lazy CDC is NOT
// predictively normalized. Lazy mode retains the whole upstream-compressed file
// and serves .nar.xz correctly, so the persisted narinfo must keep its truthful
// xz URL and compression. Only serve-time normalization (once HasNarInChunks)
// flips it to none for lazy.
func TestPullNarInfo_LazyCDC_RetainsXzURL(t *testing.T) {
	t.Parallel()

	c, dbClient := setupCDCPullCache(t, true)

	reqCtx := withNarPrefetchDisabled(newContext())
	_, _ = c.GetNarInfo(reqCtx, testdata.Nar1.NarInfoHash)

	row, err := dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).
		Only(newContext())
	require.NoError(t, err, "the pull must persist the narinfo row")

	require.NotNil(t, row.URL, "persisted narinfo must have a URL")
	require.True(t, strings.HasSuffix(*row.URL, ".nar.xz"),
		"lazy-CDC pull must persist the truthful xz URL; got %q", *row.URL)
	require.NotNil(t, row.Compression, "persisted narinfo must record a compression")
	require.Equal(t, nar.CompressionTypeXz.String(), *row.Compression,
		"lazy-CDC pull must retain xz compression, not predict none")
}
