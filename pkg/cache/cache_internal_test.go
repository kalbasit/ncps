package cache

import (
	"fmt"
	"math/rand/v2"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/inconshreveable/log15/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/testdata"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

func TestNew(t *testing.T) {
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

		cachePath := os.TempDir()

		c, err := New(logger, "cache.example.com", cachePath)
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

		cachePath := os.TempDir()

		c, err := New(logger, "cache.example.com", cachePath)
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
	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)
	// defer os.RemoveAll(dir) // clean up

	fmt.Printf("dir: %s\n", dir)

	c, err := New(logger, "cache.example.com", dir)
	require.NoError(t, err)

	ts := testdata.HTTPTestServer(t, 40)
	defer ts.Close()

	tu, err := url.Parse(ts.URL)
	require.NoError(t, err)

	uc, err := upstream.New(logger, tu.Host, testdata.PublicKeys())
	require.NoError(t, err)

	c.AddUpstreamCaches(uc)

	allEntries := testdata.Entries
	entries := testdata.Entries[:len(testdata.Entries)-1]
	lastEntry := testdata.Entries[len(testdata.Entries)-1]

	t.Run("confirm my vars are correct", func(t *testing.T) {
		assert.Equal(t, allEntries, append(entries, lastEntry))
	})

	// define the maximum size of our store based on responses of our testdata
	// minus the last one
	var maxSize uint64
	for _, nar := range entries {
		maxSize += uint64(len(nar.NarText))
	}
	c.SetMaxSize(maxSize)

	t.Run("confirm the maxSize is set correctly", func(t *testing.T) {
		assert.Equal(t, maxSize, c.maxSize)
	})

	var sizePulled int64
	for _, nar := range allEntries {
		_, err := c.GetNarInfo(nar.NarInfoHash)
		require.NoError(t, err)

		size, _, err := c.GetNar(nar.NarHash, "")
		require.NoError(t, err)

		sizePulled += size
	}

	t.Run("confirm size pulled is less than maxSize by exactly the last one", func(t *testing.T) {
		expectedSize := int64(maxSize) + int64(len(lastEntry.NarText))
		assert.Equal(t, expectedSize, sizePulled)
	})

	t.Run("confirm all nars are in the store", func(t *testing.T) {
		for _, nar := range allEntries {
			assert.True(t, c.hasNarInStore(logger, nar.NarHash, ""))
		}
	})

	t.Run("pull the nars except for the last entry to get their last_accessed_at updated", func(t *testing.T) {
		var sizePulled int64

		for _, nar := range entries {
			_, err := c.GetNarInfo(nar.NarInfoHash)
			require.NoError(t, err)

			size, _, err := c.GetNar(nar.NarHash, "")
			require.NoError(t, err)

			sizePulled += size
		}

		t.Run("confirm size pulled is exactly maxSize", func(t *testing.T) {
			assert.Equal(t, int64(maxSize), sizePulled)
		})
	})

	t.Run("all narinfo records are in the database", func(t *testing.T) {
		tx, err := c.db.Begin()
		require.NoError(t, err)

		defer tx.Rollback()

		for _, nar := range allEntries {
			_, err := c.db.GetNarInfoRecord(tx, nar.NarInfoHash)
			assert.NoError(t, err)
		}
	})

	t.Run("all nar records are in the database", func(t *testing.T) {
		tx, err := c.db.Begin()
		require.NoError(t, err)

		defer tx.Rollback()

		for _, nar := range allEntries {
			_, err := c.db.GetNarRecord(tx, nar.NarHash)
			assert.NoError(t, err)
		}
	})

	c.runLRU()

	t.Run("confirm all narinfos except the last one are in the store", func(t *testing.T) {
		for _, nar := range entries {
			assert.True(t, c.hasNarInfoInStore(logger, nar.NarInfoHash))
		}

		assert.False(t, c.hasNarInfoInStore(logger, lastEntry.NarInfoHash))
	})

	t.Run("confirm all nars except the last one are in the store", func(t *testing.T) {
		for _, nar := range entries {
			assert.True(t, c.hasNarInStore(logger, nar.NarHash, ""))
		}

		assert.False(t, c.hasNarInStore(logger, lastEntry.NarHash, ""))
	})

	t.Run("all narinfo records except the last one are in the database", func(t *testing.T) {
		tx, err := c.db.Begin()
		require.NoError(t, err)

		defer tx.Rollback()

		for _, nar := range entries {
			_, err := c.db.GetNarInfoRecord(tx, nar.NarInfoHash)
			assert.NoError(t, err)
		}

		_, err = c.db.GetNarInfoRecord(tx, lastEntry.NarInfoHash)
		assert.ErrorIs(t, database.ErrNotFound, err)
	})

	t.Run("all nar records except the last one are in the database", func(t *testing.T) {
		tx, err := c.db.Begin()
		require.NoError(t, err)

		defer tx.Rollback()

		for _, nar := range entries {
			_, err := c.db.GetNarRecord(tx, nar.NarHash)
			assert.NoError(t, err)
		}

		_, err = c.db.GetNarRecord(tx, lastEntry.NarHash)
		assert.ErrorIs(t, database.ErrNotFound, err)
	})
}
