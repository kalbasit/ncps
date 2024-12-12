package cache

import (
	"context"
	"database/sql"
	"math/rand/v2"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inconshreveable/log15/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

func TestAddUpstreamCaches(t *testing.T) {
	t.Run("upstream caches added at once", func(t *testing.T) {
		t.Parallel()

		testServers := make(map[int]*httptest.Server)

		for i := 1; i < 10; i++ {
			ts := testdata.HTTPTestServer(t, i)
			defer ts.Close()
			testServers[i] = ts
		}

		randomOrder := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
		rand.Shuffle(len(randomOrder), func(i, j int) { randomOrder[i], randomOrder[j] = randomOrder[j], randomOrder[i] })

		t.Logf("random order established: %v", randomOrder)

		ucs := make([]upstream.Cache, 0, len(testServers))

		for _, idx := range randomOrder {
			ts := testServers[idx]

			u, err := url.Parse(ts.URL)
			require.NoError(t, err)

			uc, err := upstream.New(logger, u.Host, nil)
			require.NoError(t, err)

			ucs = append(ucs, uc)
		}

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
		testhelper.CreateMigrateDatabase(t, dbFile)

		c, err := New(logger, "cache.example.com", dir)
		require.NoError(t, err)

		c.AddUpstreamCaches(ucs...)

		for idx, uc := range c.upstreamCaches {
			//nolint:gosec
			if want, got := uint64(idx+1), uc.GetPriority(); want != got {
				t.Errorf("expected the priority at index %d to be %d but got %d", idx, want, got)
			}
		}
	})

	t.Run("upstream caches added one by one", func(t *testing.T) {
		t.Parallel()

		testServers := make(map[int]*httptest.Server)

		for i := 1; i < 10; i++ {
			ts := testdata.HTTPTestServer(t, i)
			defer ts.Close()
			testServers[i] = ts
		}

		randomOrder := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
		rand.Shuffle(len(randomOrder), func(i, j int) { randomOrder[i], randomOrder[j] = randomOrder[j], randomOrder[i] })

		t.Logf("random order established: %v", randomOrder)

		ucs := make([]upstream.Cache, 0, len(testServers))

		for _, idx := range randomOrder {
			ts := testServers[idx]

			u, err := url.Parse(ts.URL)
			require.NoError(t, err)

			uc, err := upstream.New(logger, u.Host, nil)
			require.NoError(t, err)

			ucs = append(ucs, uc)
		}

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
		testhelper.CreateMigrateDatabase(t, dbFile)

		c, err := New(logger, "cache.example.com", dir)
		require.NoError(t, err)

		for _, uc := range ucs {
			c.AddUpstreamCaches(uc)
		}

		for idx, uc := range c.upstreamCaches {
			assert.EqualValues(t, idx+1, uc.GetPriority())
		}
	})
}

// runLRU is not exposed function but it's a functionality that's triggered by
// a cronjob.
func TestRunLRU(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	c, err := New(logger, "cache.example.com", dir)
	require.NoError(t, err)

	ts := testdata.HTTPTestServer(t, 40)
	defer ts.Close()

	tu, err := url.Parse(ts.URL)
	require.NoError(t, err)

	uc, err := upstream.New(logger, tu.Host, testdata.PublicKeys())
	require.NoError(t, err)

	c.AddUpstreamCaches(uc)
	c.SetRecordAgeIgnoreTouch(0)

	allEntries := testdata.Entries
	entries := testdata.Entries[:len(testdata.Entries)-1]
	lastEntry := testdata.Entries[len(testdata.Entries)-1]

	assert.Equal(t, allEntries, append(entries, lastEntry), "confirm my vars are correct")

	// define the maximum size of our store based on responses of our testdata
	// minus the last one
	var maxSize uint64
	for _, nar := range entries {
		maxSize += uint64(len(nar.NarText))
	}

	c.SetMaxSize(maxSize)

	assert.Equal(t, maxSize, c.maxSize, "confirm the maxSize is set correctly")

	var sizePulled int64

	for _, nar := range allEntries {
		_, err := c.GetNarInfo(context.Background(), nar.NarInfoHash)
		require.NoError(t, err)

		size, _, err := c.GetNar(context.Background(), nar.NarHash, "xz")
		require.NoError(t, err)

		sizePulled += size
	}

	//nolint:gosec
	expectedSize := int64(maxSize) + int64(len(lastEntry.NarText))

	assert.Equal(t, expectedSize, sizePulled, "size pulled is less than maxSize by exactly the last one")

	for _, nar := range allEntries {
		assert.True(t, c.hasNarInStore(logger, nar.NarHash, "xz"), "confirm all nars are in the store")
	}

	// ensure time has moved by one sec for the last_accessed_at work
	time.Sleep(time.Second)

	// pull the nars except for the last entry to get their last_accessed_at updated
	sizePulled = 0

	for _, nar := range entries {
		_, err := c.GetNarInfo(context.Background(), nar.NarInfoHash)
		require.NoError(t, err)

		size, _, err := c.GetNar(context.Background(), nar.NarHash, "xz")
		require.NoError(t, err)

		sizePulled += size
	}

	//nolint:gosec
	assert.Equal(t, int64(maxSize), sizePulled, "confirm size pulled is exactly maxSize")

	// all narinfo records are in the database
	for _, nar := range allEntries {
		_, err := c.db.GetNarInfoByHash(context.Background(), nar.NarInfoHash)
		require.NoError(t, err)
	}

	// all nar records are in the database
	for _, nar := range allEntries {
		_, err := c.db.GetNarByHash(context.Background(), nar.NarHash)
		require.NoError(t, err)
	}

	c.runLRU()

	// confirm all narinfos except the last one are in the store
	for _, nar := range entries {
		assert.True(t, c.hasNarInfoInStore(logger, nar.NarInfoHash))
	}

	assert.False(t, c.hasNarInfoInStore(logger, lastEntry.NarInfoHash))

	// confirm all nars except the last one are in the store
	for _, nar := range entries {
		assert.True(t, c.hasNarInStore(logger, nar.NarHash, "xz"))
	}

	assert.False(t, c.hasNarInStore(logger, lastEntry.NarHash, "xz"))

	// all narinfo records except the last one are in the database
	for _, nar := range entries {
		_, err := c.db.GetNarInfoByHash(context.Background(), nar.NarInfoHash)
		require.NoError(t, err)
	}

	_, err = c.db.GetNarInfoByHash(context.Background(), lastEntry.NarInfoHash)
	require.ErrorIs(t, sql.ErrNoRows, err)

	// all nar records except the last one are in the database

	for _, nar := range entries {
		_, err := c.db.GetNarByHash(context.Background(), nar.NarHash)
		require.NoError(t, err)
	}

	_, err = c.db.GetNarByHash(context.Background(), lastEntry.NarHash)
	require.ErrorIs(t, sql.ErrNoRows, err)
}
