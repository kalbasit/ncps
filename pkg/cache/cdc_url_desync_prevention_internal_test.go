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

// TestPullNarInfo_EagerCDC_DoesNotPrematurelyNormalizeXzURL is the Fix B
// prevention guard. Under eager CDC (CDC enabled, lazy chunking disabled),
// pulling an xz upstream narinfo must persist the narinfo with its TRUTHFUL xz
// URL — NOT a predicted compression=none URL.
//
// The previous behavior rewrote the persisted URL to nar/<hash>.nar (none) and
// nulled FileHash synchronously, BEFORE the background CDC chunking ran. If
// chunking never completed (process crash/restart between the whole-file write
// and SetTotalChunks), the narinfo was left permanently advertising a none URL
// while the NAR was stored xz-only at /nar/<hash>.nar.xz — so a client GET of
// /nar/<hash>.nar 404s and a `nix copy` reference check aborts. Serve-time
// maybeCDCNormalizeNarInfoURL already presents url=none once the NAR is ACTUALLY
// chunked (HasNarInChunks), so deferring normalization to serve time loses
// nothing in the happy path while closing the desync window.
//
// NAR prefetch is disabled so chunking never runs — the exact window the old
// premature normalization mishandled.
func TestPullNarInfo_EagerCDC_DoesNotPrematurelyNormalizeXzURL(t *testing.T) {
	t.Parallel()

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

	// Eager CDC: chunk store present, CDC enabled, lazy chunking left disabled.
	chunkStore, err := chunk.NewLocalStore(filepath.Join(dir, "chunks-store"))
	require.NoError(t, err)
	c.SetChunkStore(chunkStore)
	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))
	require.False(t, c.GetCDCLazyChunkingEnabled(), "precondition: eager (non-lazy) CDC")

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	c.SetRecordAgeIgnoreTouch(0)

	<-c.GetHealthChecker().Trigger()

	// Disable the background NAR prefetch so chunking never runs.
	reqCtx := withNarPrefetchDisabled(ctx)

	// The pull persists the narinfo; the post-pull re-read fires the missing-NAR
	// guard and returns an error. We don't care about that here — we assert on the
	// PERSISTED narinfo row, which the non-destructive purge leaves intact.
	_, _ = c.GetNarInfo(reqCtx, testdata.Nar1.NarInfoHash)

	row, err := dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).
		Only(ctx)
	require.NoError(t, err, "the pull must persist the narinfo row")

	require.NotNil(t, row.URL, "persisted narinfo must have a URL")
	require.True(t, strings.HasSuffix(*row.URL, ".nar.xz"),
		"eager-CDC pull of an xz narinfo must persist the truthful xz URL (serve-time "+
			"normalization presents none once actually chunked); got %q", *row.URL)
	require.NotNil(t, row.Compression, "persisted narinfo must record a compression")
	require.Equal(t, nar.CompressionTypeXz.String(), *row.Compression,
		"persisted compression must remain xz, not be predicted as none")
}
