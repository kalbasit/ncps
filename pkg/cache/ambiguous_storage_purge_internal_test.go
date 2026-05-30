package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	locklocal "github.com/kalbasit/ncps/pkg/lock/local"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// errSimulatedStorageTimeout models an undeterminable storage result — e.g. a
// timed-out or stale stat on a network filesystem — as opposed to a confirmed
// absence (which StatNar reports as (false, nil)).
var errSimulatedStorageTimeout = errors.New("simulated storage timeout")

// ambiguousNarStore wraps a real local store but reports an undeterminable
// result (false, err) from StatNar for a chosen NAR hash, simulating a flaky
// network filesystem that errors on stat instead of returning ENOENT.
type ambiguousNarStore struct {
	*local.Store

	failHash string
}

func (s *ambiguousNarStore) StatNar(ctx context.Context, narURL nar.URL) (bool, error) {
	if narURL.Hash == s.failHash {
		return false, errSimulatedStorageTimeout
	}

	return s.Store.StatNar(ctx, narURL)
}

// TestGetNarInfoFromDatabase_AmbiguousStorageErrorDoesNotPurge reproduces the
// production "requesting a purge" cascade on a shared network filesystem: a
// narinfo is present in the DB, but a stat of its backing NAR fails ambiguously
// (not a confirmed absence). The purge guard MUST treat that as "could not
// determine" — keep the narinfo and let the next request re-evaluate — rather
// than destructively purging it (which then surfaces to clients as a 404).
func TestGetNarInfoFromDatabase_AmbiguousStorageErrorDoesNotPurge(t *testing.T) {
	t.Parallel()

	ctx := newContext()

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

	narStore := &ambiguousNarStore{Store: localStore, failHash: testdata.Nar1.NarHash}

	c, err := New(ctx, cacheName, dbClient, localStore, localStore, narStore, "",
		locklocal.NewLocker(), locklocal.NewRWLocker(), downloadLockTTL, downloadPollTimeout, cacheLockTTL)
	require.NoError(t, err)
	t.Cleanup(c.Close)

	// A narinfo whose backing NAR the store will report ambiguously.
	_, err = dbClient.Ent().NarInfo.Create().
		SetHash(testdata.Nar1.NarInfoHash).
		SetURL("nar/" + testdata.Nar1.NarHash + ".nar").
		Save(ctx)
	require.NoError(t, err)

	ni, err := c.getNarInfoFromDatabase(ctx, testdata.Nar1.NarInfoHash)
	require.NoError(t, err,
		"an ambiguous storage error must not purge the narinfo nor surface as an error")
	require.NotNil(t, ni)

	// The narinfo row must still exist — it was not purged.
	_, err = fetchNarInfo(ctx, dbClient, testdata.Nar1.NarInfoHash)
	require.NoError(t, err, "narinfo must not be purged on an ambiguous storage error")
}
