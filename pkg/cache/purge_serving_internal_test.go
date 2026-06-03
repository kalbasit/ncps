package cache

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	locklocal "github.com/kalbasit/ncps/pkg/lock/local"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// TestGetNarInfo_PostPullPurgeMapsToNotFoundNotSentinel reproduces the
// production "HTTP 500: the narinfo was purged" failure. The upstream serves the
// narinfo, but its backing NAR never lands in storage (here: NAR prefetch is
// disabled, modelling the production case where the background NAR download
// failed or was never tracked under distributed-lock contention). After the
// upstream pull, GetNarInfo re-reads the narinfo from the database, the purge
// guard fires (narinfo row present, backing NAR absent, no download in flight),
// and the internal ErrNarInfoPurged sentinel must NOT escape to the caller — it
// would surface to clients as an HTTP 500. A fired purge must instead resolve to
// storage.ErrNotFound (HTTP 404), so Nix falls back to its next substituter.
func TestGetNarInfo_PostPullPurgeMapsToNotFoundNotSentinel(t *testing.T) {
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

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	c.SetRecordAgeIgnoreTouch(0)

	// Wait for the upstream cache to become available.
	<-c.GetHealthChecker().Trigger()

	// Disable NAR prefetch so the post-pull re-read deterministically observes an
	// orphan narinfo (DB row present, backing NAR absent, no download job in
	// flight) and fires the purge guard.
	reqCtx := withNarPrefetchDisabled(ctx)

	_, err = c.GetNarInfo(reqCtx, testdata.Nar2.NarInfoHash)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNarInfoPurged,
		"the purge sentinel must never escape GetNarInfo (it would surface as HTTP 500)")
	require.ErrorIs(t, err, storage.ErrNotFound,
		"a fired purge must resolve to ErrNotFound (HTTP 404 → upstream fallback)")
}

// TestGetNarInfo_Stage1PurgeThenUpstreamUnavailableResolvesToNotFound verifies
// the stage-1 fallthrough: when the first database lookup fires the purge guard
// (orphan narinfo in the DB) and the subsequent upstream re-fetch cannot supply
// the narinfo, GetNarInfo resolves to storage.ErrNotFound (HTTP 404 → Nix falls
// back to its next substituter) and never leaks the internal purge sentinel.
func TestGetNarInfo_Stage1PurgeThenUpstreamUnavailableResolvesToNotFound(t *testing.T) {
	t.Parallel()

	ctx := newContext()

	ts := testdata.NewTestServer(t, 40)
	t.Cleanup(ts.Close)

	// Upstream answers /nix-cache-info (so it is considered healthy) but fails
	// every narinfo fetch with HTTP 500.
	ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, ".narinfo") {
			w.WriteHeader(http.StatusInternalServerError)

			return true
		}

		return false
	})

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

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	c.SetRecordAgeIgnoreTouch(0)

	<-c.GetHealthChecker().Trigger()

	// Seed an orphan narinfo (DB row present, backing NAR absent) so the first
	// database lookup fires the purge guard, then falls through to the upstream
	// re-fetch — which fails transiently.
	_, err = dbClient.Ent().NarInfo.Create().
		SetHash(testdata.Nar1.NarInfoHash).
		SetURL("nar/" + testdata.Nar1.NarHash + ".nar").
		Save(ctx)
	require.NoError(t, err)

	_, err = c.GetNarInfo(ctx, testdata.Nar1.NarInfoHash)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNarInfoPurged,
		"the purge sentinel must never escape GetNarInfo (it would surface as HTTP 500)")
	require.ErrorIs(t, err, storage.ErrNotFound,
		"a stage-1 purge with an unavailable upstream must resolve to ErrNotFound (HTTP 404)")
}
