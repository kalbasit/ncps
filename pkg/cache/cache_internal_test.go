package cache

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

//nolint:gochecknoglobals
var logger = zerolog.New(io.Discard)

func TestAddUpstreamCaches(t *testing.T) {
	t.Run("upstream caches added at once", func(t *testing.T) {
		t.Parallel()

		testServers := make(map[int]*testdata.Server)

		for i := 1; i < 10; i++ {
			ts := testdata.NewTestServer(t, i)
			defer ts.Close()
			testServers[i] = ts
		}

		randomOrder := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
		rand.Shuffle(len(randomOrder), func(i, j int) { randomOrder[i], randomOrder[j] = randomOrder[j], randomOrder[i] })

		t.Logf("random order established: %v", randomOrder)

		ucs := make([]upstream.Cache, 0, len(testServers))

		for _, idx := range randomOrder {
			ts := testServers[idx]

			uc, err := upstream.New(logger, testhelper.MustParseURL(t, ts.URL), nil)
			require.NoError(t, err)

			ucs = append(ucs, uc)
		}

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
		testhelper.CreateMigrateDatabase(t, dbFile)

		db, err := database.Open("sqlite:" + dbFile)
		require.NoError(t, err)

		c, err := New(logger, "cache.example.com", dir, db)
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

		testServers := make(map[int]*testdata.Server)

		for i := 1; i < 10; i++ {
			ts := testdata.NewTestServer(t, i)
			defer ts.Close()
			testServers[i] = ts
		}

		randomOrder := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
		rand.Shuffle(len(randomOrder), func(i, j int) { randomOrder[i], randomOrder[j] = randomOrder[j], randomOrder[i] })

		t.Logf("random order established: %v", randomOrder)

		ucs := make([]upstream.Cache, 0, len(testServers))

		for _, idx := range randomOrder {
			ts := testServers[idx]

			uc, err := upstream.New(logger, testhelper.MustParseURL(t, ts.URL), nil)
			require.NoError(t, err)

			ucs = append(ucs, uc)
		}

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
		testhelper.CreateMigrateDatabase(t, dbFile)

		db, err := database.Open("sqlite:" + dbFile)
		require.NoError(t, err)

		c, err := New(logger, "cache.example.com", dir, db)
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

	db, err := database.Open("sqlite:" + dbFile)
	require.NoError(t, err)

	c, err := New(logger, "cache.example.com", dir, db)
	require.NoError(t, err)

	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	uc, err := upstream.New(logger, testhelper.MustParseURL(t, ts.URL), nil)
	require.NoError(t, err)

	c.AddUpstreamCaches(uc)
	c.SetRecordAgeIgnoreTouch(0)

	// NOTE: For this test, any nar that's explicitly testing the zstd
	// transparent compression support will not be included because its size will
	// not be known and so the test will be more complex.
	var allEntries []testdata.Entry

	for _, narEntry := range testdata.Entries {
		expectedCompression := fmt.Sprintf("Compression: %s", narEntry.NarCompression)
		if strings.Contains(narEntry.NarInfoText, expectedCompression) {
			allEntries = append(allEntries, narEntry)
		}
	}

	entries := allEntries[:len(allEntries)-1]
	lastEntry := allEntries[len(allEntries)-1]

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

	for i, narEntry := range allEntries {
		_, err := c.GetNarInfo(context.Background(), narEntry.NarInfoHash)
		require.NoErrorf(t, err, "unable to get narinfo for idx %d", i)

		nu := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}
		size, _, err := c.GetNar(context.Background(), nu)
		require.NoError(t, err, "unable to get nar for idx %d", i)

		sizePulled += size
	}

	//nolint:gosec
	expectedSize := int64(maxSize) + int64(len(lastEntry.NarText))

	assert.Equal(t, expectedSize, sizePulled, "size pulled is less than maxSize by exactly the last one")

	for _, narEntry := range allEntries {
		nu := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}
		assert.True(t, c.hasNarInStore(logger, &nu), "confirm all nars are in the store")
	}

	// ensure time has moved by one sec for the last_accessed_at work
	time.Sleep(time.Second)

	// pull the nars except for the last entry to get their last_accessed_at updated
	sizePulled = 0

	for _, narEntry := range entries {
		_, err := c.GetNarInfo(context.Background(), narEntry.NarInfoHash)
		require.NoError(t, err)

		nu := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}
		size, _, err := c.GetNar(context.Background(), nu)
		require.NoError(t, err)

		sizePulled += size
	}

	//nolint:gosec
	assert.Equal(t, int64(maxSize), sizePulled, "confirm size pulled is exactly maxSize")

	// all narinfo records are in the database
	for _, narEntry := range allEntries {
		_, err := c.db.GetNarInfoByHash(context.Background(), narEntry.NarInfoHash)
		require.NoError(t, err)
	}

	// all nar records are in the database
	for _, narEntry := range allEntries {
		_, err := c.db.GetNarByHash(context.Background(), narEntry.NarHash)
		require.NoError(t, err)
	}

	c.runLRU()

	// confirm all narinfos except the last one are in the store
	for _, nar := range entries {
		assert.True(t, c.hasNarInfoInStore(logger, nar.NarInfoHash))
	}

	assert.False(t, c.hasNarInfoInStore(logger, lastEntry.NarInfoHash))

	// confirm all nars except the last one are in the store
	for _, narEntry := range entries {
		nu := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}
		assert.True(t, c.hasNarInStore(logger, &nu))
	}

	nu := nar.URL{Hash: lastEntry.NarHash, Compression: lastEntry.NarCompression}
	assert.False(t, c.hasNarInStore(logger, &nu))

	// all narinfo records except the last one are in the database
	for _, narEntry := range entries {
		_, err := c.db.GetNarInfoByHash(context.Background(), narEntry.NarInfoHash)
		require.NoError(t, err)
	}

	_, err = c.db.GetNarInfoByHash(context.Background(), lastEntry.NarInfoHash)
	require.ErrorIs(t, sql.ErrNoRows, err)

	// all nar records except the last one are in the database

	for _, narEntry := range entries {
		_, err := c.db.GetNarByHash(context.Background(), narEntry.NarHash)
		require.NoError(t, err)
	}

	_, err = c.db.GetNarByHash(context.Background(), lastEntry.NarHash)
	require.ErrorIs(t, sql.ErrNoRows, err)
}
