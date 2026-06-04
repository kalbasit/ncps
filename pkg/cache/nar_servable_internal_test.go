package cache

import (
	"io"
	"os"
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

// newServableTestCache builds a cache backed by the given NarStore (so tests can
// inject an ambiguous store). The localStore also serves as config + narinfo store.
func newServableTestCache(t *testing.T, makeNarStore func(*local.Store) storage.NarStore) *Cache {
	t.Helper()

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

	narStore := makeNarStore(localStore)

	c, err := New(ctx, cacheName, dbClient, localStore, localStore, narStore, "",
		locklocal.NewLocker(), locklocal.NewRWLocker(), downloadLockTTL, downloadPollTimeout, cacheLockTTL)
	require.NoError(t, err)
	t.Cleanup(c.Close)

	return c
}

// TestIsNarServable_WholeFilePresent: bytes in the store → (true, nil).
func TestIsNarServable_WholeFilePresent(t *testing.T) {
	t.Parallel()

	c := newServableTestCache(t, func(s *local.Store) storage.NarStore { return s })

	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: testdata.Nar1.NarCompression}
	require.NoError(t, c.PutNar(newContext(), narURL, io.NopCloser(strings.NewReader(testdata.Nar1.NarText))))

	servable, err := c.IsNarServable(newContext(), narURL)
	require.NoError(t, err)
	require.True(t, servable, "a NAR whose bytes are in the store must be servable")
}

// TestIsNarServable_BytelessReturnsFalseNil: no bytes, no chunks → (false, nil),
// a *confirmed* not-servable (distinct from an ambiguous error).
func TestIsNarServable_BytelessReturnsFalseNil(t *testing.T) {
	t.Parallel()

	c := newServableTestCache(t, func(s *local.Store) storage.NarStore { return s })

	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: testdata.Nar1.NarCompression}

	servable, err := c.IsNarServable(newContext(), narURL)
	require.NoError(t, err, "a confirmed absence must be (false, nil), not an error")
	require.False(t, servable)
}

// TestIsNarServable_AmbiguousReturnsError: an undeterminable storage stat must
// surface as (false, err) so callers don't treat it as a confirmed absence.
func TestIsNarServable_AmbiguousReturnsError(t *testing.T) {
	t.Parallel()

	c := newServableTestCache(t, func(s *local.Store) storage.NarStore {
		return &ambiguousNarStore{Store: s, failHash: testdata.Nar1.NarHash}
	})

	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: testdata.Nar1.NarCompression}

	servable, err := c.IsNarServable(newContext(), narURL)
	require.Error(t, err, "an ambiguous storage error must surface, not be reported as absent")
	require.False(t, servable)
}
