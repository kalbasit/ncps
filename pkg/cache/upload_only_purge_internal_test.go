package cache

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	locklocal "github.com/kalbasit/ncps/pkg/lock/local"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// newUploadOnlyPurgeCacheNoSeed wires a cache backed by a plain local store (so
// a missing NAR is a *confirmed* absence, not an ambiguous storage error) with
// no narinfo seeded.
func newUploadOnlyPurgeCacheNoSeed(t *testing.T) (*Cache, *database.Client) {
	t.Helper()

	ctx := newContext()

	dir := t.TempDir()

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

	return c, dbClient
}

// newUploadOnlyPurgeCache returns a cache seeded with an orphan narinfo: a DB row
// whose backing NAR is absent from storage. This is the phantom-narinfo state
// that the purge guard reacts to.
func newUploadOnlyPurgeCache(t *testing.T) (*Cache, *database.Client) {
	t.Helper()

	c, dbClient := newUploadOnlyPurgeCacheNoSeed(t)

	_, err := dbClient.Ent().NarInfo.Create().
		SetHash(testdata.Nar1.NarInfoHash).
		SetURL("nar/" + testdata.Nar1.NarHash + ".nar").
		Save(newContext())
	require.NoError(t, err)

	return c, dbClient
}

// TestGetNarInfoFromDatabase_UploadOnlyDoesNotPurge verifies that on the
// upload-only (/upload) read path, a missing-NAR narinfo is reported as a cache
// miss (the internal ErrNarInfoPurged sentinel, which GetNarInfo maps to
// storage.ErrNotFound → HTTP 404) WITHOUT destructively purging the DB row. The
// client is about to re-PUT the narinfo; purging here makes the cache's
// path-validity answer non-monotonic within a single `nix copy` and aborts the
// client's reference-verification step.
func TestGetNarInfoFromDatabase_UploadOnlyDoesNotPurge(t *testing.T) {
	t.Parallel()

	c, dbClient := newUploadOnlyPurgeCache(t)

	ctx := WithUploadOnly(newContext())

	ni, err := c.getNarInfoFromDatabase(ctx, testdata.Nar1.NarInfoHash)
	require.ErrorIs(t, err, ErrNarInfoPurged,
		"a missing-NAR narinfo on the upload-only path must resolve to the purge sentinel (→ 404)")
	require.Nil(t, ni)

	// The narinfo row MUST still exist — the upload-only read must be
	// non-destructive so the subsequent client PUT can overwrite it.
	_, err = fetchNarInfo(ctx, dbClient, testdata.Nar1.NarInfoHash)
	require.NoError(t, err, "upload-only read must NOT purge the narinfo row")
}

// TestGetNarInfo_UploadOnlyPhantomIsMonotonic exercises the public GetNarInfo
// entry point: an upload-only read of a phantom narinfo (DB row present, NAR
// absent) resolves to storage.ErrNotFound (→ HTTP 404) and never leaks the purge
// sentinel. Repeating the read yields the same answer and never mutates the DB —
// the path-validity answer is monotonic, which is what `nix copy`'s
// reference-verification step relies on.
func TestGetNarInfo_UploadOnlyPhantomIsMonotonic(t *testing.T) {
	t.Parallel()

	c, dbClient := newUploadOnlyPurgeCache(t)

	ctx := WithUploadOnly(newContext())

	for i := range 2 {
		ni, err := c.GetNarInfo(ctx, testdata.Nar1.NarInfoHash)
		require.ErrorIs(t, err, storage.ErrNotFound,
			"read %d: upload-only phantom narinfo must resolve to ErrNotFound (HTTP 404)", i)
		require.NotErrorIs(t, err, ErrNarInfoPurged,
			"read %d: the purge sentinel must never escape GetNarInfo", i)
		require.Nil(t, ni)

		// Every read leaves the narinfo row intact — reads are non-mutating.
		_, err = fetchNarInfo(ctx, dbClient, testdata.Nar1.NarInfoHash)
		require.NoError(t, err, "read %d: upload-only read must not purge the narinfo row", i)
	}
}

// TestGetNarInfoFromDatabase_NonUploadOnlyDoesNotPurge asserts the substituter
// (root) read path is now NON-DESTRUCTIVE too: a missing-NAR narinfo reports the
// cache-miss sentinel (which GetNarInfo turns into an upstream re-fetch) WITHOUT
// deleting the narinfo row. Deleting here would race a concurrent `nix copy`
// reference check across replicas that share one database, flipping a verified
// reference 200->404 mid-copy. The record is healed in place by the upstream
// re-fetch, not by a destructive purge-then-recreate.
func TestGetNarInfoFromDatabase_NonUploadOnlyDoesNotPurge(t *testing.T) {
	t.Parallel()

	c, dbClient := newUploadOnlyPurgeCache(t)

	// No WithUploadOnly: this is the normal/substituter read path.
	ctx := newContext()

	ni, err := c.getNarInfoFromDatabase(ctx, testdata.Nar1.NarInfoHash)
	require.ErrorIs(t, err, ErrNarInfoPurged)
	require.Nil(t, ni)

	// The narinfo row MUST still exist — the substituter read is non-destructive
	// and relies on the upstream re-fetch to overwrite it in place.
	_, err = fetchNarInfo(ctx, dbClient, testdata.Nar1.NarInfoHash)
	require.NoError(t, err, "substituter read must NOT purge the narinfo row")
}

// TestGetNarInfo_UploadOnlyPhantomRepairedByPut covers the spec scenario "a
// subsequent upload PUT repairs the stale record": because the upload-only read
// left the narinfo row in place (instead of purging it), uploading the missing
// NAR bytes repairs the phantom, and a later read serves the narinfo. This is the
// payoff of the non-destructive behavior — the path becomes valid again rather
// than churning.
func TestGetNarInfo_UploadOnlyPhantomRepairedByPut(t *testing.T) {
	t.Parallel()

	c, dbClient := newUploadOnlyPurgeCacheNoSeed(t)

	// Seed a complete phantom the way production does: a full narinfo record (all
	// fields) created by an upload PUT, but with no NAR bytes in storage.
	require.NoError(t, c.PutNarInfo(newContext(), testdata.Nar1.NarInfoHash,
		io.NopCloser(strings.NewReader(testdata.Nar1.NarInfoText))))

	ctx := WithUploadOnly(newContext())

	// 1. Phantom read: the upload-only read reports a miss without purging.
	_, err := c.GetNarInfo(ctx, testdata.Nar1.NarInfoHash)
	require.ErrorIs(t, err, storage.ErrNotFound)

	_, err = fetchNarInfo(ctx, dbClient, testdata.Nar1.NarInfoHash)
	require.NoError(t, err, "the narinfo row must survive the upload-only read")

	// 2. The client re-uploads the missing NAR bytes at the URL the narinfo points
	//    to. Because the narinfo row was left intact, this repairs the record.
	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: testdata.Nar1.NarCompression}
	require.NoError(t, c.PutNar(newContext(), narURL, io.NopCloser(strings.NewReader(testdata.Nar1.NarText))))

	// 3. The narinfo is now served — the phantom was repaired, not stuck in a churn.
	ni, err := c.GetNarInfo(newContext(), testdata.Nar1.NarInfoHash)
	require.NoError(t, err)
	require.NotNil(t, ni)
}
