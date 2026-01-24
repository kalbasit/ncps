package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	locklocal "github.com/kalbasit/ncps/pkg/lock/local"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

const (
	cacheName       = "cache.example.com"
	downloadLockTTL = 5 * time.Minute
	cacheLockTTL    = 30 * time.Minute
)

var errTest = errors.New("test error")

// buildInsertNarInfoSQL constructs a database-agnostic SQL INSERT statement
// for inserting a minimal narinfo record (hash and created_at only).
// It detects the database type by probing the database and uses appropriate parameter placeholders.
func buildInsertNarInfoSQL(db database.Querier) string {
	// Detect database type using a probe query
	// PostgreSQL responds to this query with a version string starting with "PostgreSQL"
	var version string

	err := db.DB().QueryRowContext(context.Background(), "SELECT version()").Scan(&version)

	if err == nil && strings.Contains(version, "PostgreSQL") {
		// PostgreSQL uses $1, $2, etc. placeholders
		return "INSERT INTO narinfos (hash, created_at) VALUES ($1, $2)"
	}

	// MySQL and SQLite use ? placeholders
	return "INSERT INTO narinfos (hash, created_at) VALUES (?, ?)"
}

func setupTestCache(t *testing.T) (*Cache, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	if err != nil {
		os.RemoveAll(dir)
	}

	require.NoError(t, err)

	cleanup := func() {
		db.DB().Close()
		os.RemoveAll(dir)
	}

	localStore, err := local.New(newContext(), dir)
	if err != nil {
		cleanup()
	}

	require.NoError(t, err)

	// Use local locks for tests
	downloadLocker := locklocal.NewLocker()
	cacheLocker := locklocal.NewRWLocker()

	c, err := New(newContext(), cacheName, db, localStore, localStore, localStore, "",
		downloadLocker, cacheLocker, downloadLockTTL, cacheLockTTL)
	if err != nil {
		cleanup()
	}

	require.NoError(t, err)

	return c, cleanup
}

func TestAddUpstreamCaches(t *testing.T) {
	t.Run("upstream caches added at once", func(t *testing.T) {
		t.Parallel()

		testServers := make(map[int]*testdata.Server)

		for i := 1; i < 10; i++ {
			ts := testdata.NewTestServer(t, i)
			defer ts.Close()

			testServers[i] = ts
		}

		randomOrder := make([]int, 0, len(testServers))
		for idx := range testServers {
			randomOrder = append(randomOrder, idx)
		}

		rand.Shuffle(len(randomOrder), func(i, j int) {
			randomOrder[i], randomOrder[j] = randomOrder[j], randomOrder[i]
		})

		t.Logf("random order established: %v", randomOrder)

		ucs := make([]*upstream.Cache, 0, len(testServers))

		for _, idx := range randomOrder {
			ts := testServers[idx]

			uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), nil)
			require.NoError(t, err)

			ucs = append(ucs, uc)
		}

		c, cleanup := setupTestCache(t)
		defer cleanup()

		c.AddUpstreamCaches(newContext(), ucs...)

		// Wait for upstream caches to become available
		<-c.GetHealthChecker().Trigger()

		for idx, uc := range c.getHealthyUpstreams() {
			assert.EqualValues(t, idx+1, uc.GetPriority())
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

		randomOrder := make([]int, 0, len(testServers))
		for idx := range testServers {
			randomOrder = append(randomOrder, idx)
		}

		rand.Shuffle(len(randomOrder), func(i, j int) {
			randomOrder[i], randomOrder[j] = randomOrder[j], randomOrder[i]
		})

		t.Logf("random order established: %v", randomOrder)

		ucs := make([]*upstream.Cache, 0, len(testServers))

		for _, idx := range randomOrder {
			ts := testServers[idx]

			uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), nil)
			require.NoError(t, err)

			ucs = append(ucs, uc)
		}

		c, cleanup := setupTestCache(t)
		defer cleanup()

		for _, uc := range ucs {
			c.AddUpstreamCaches(newContext(), uc)
		}

		// Wait for upstream caches to become available
		<-c.GetHealthChecker().Trigger()

		for idx, uc := range c.getHealthyUpstreams() {
			assert.EqualValues(t, idx+1, uc.GetPriority())
		}
	})
}

// runLRU is not exposed function but it's a functionality that's triggered by
// a cronjob.
func TestRunLRU(t *testing.T) {
	t.Parallel()

	c, cleanup := setupTestCache(t)
	defer cleanup()

	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), nil)
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	c.SetRecordAgeIgnoreTouch(0)

	// Wait for upstream caches to become available
	<-c.GetHealthChecker().Trigger()

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

	assert.Len(t, entries, len(allEntries)-1, "confirm entries length is correct")
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
		size, reader, err := c.GetNar(context.Background(), nu)
		require.NoError(t, err, "unable to get nar for idx %d", i)

		// If the size is zero (likely) then the download is in progress so
		// compute the size by reading it fully first.
		if size < 0 {
			var err error

			size, err = io.Copy(io.Discard, reader)
			require.NoError(t, err)
		}

		sizePulled += size
	}

	//nolint:gosec
	expectedSize := int64(maxSize) + int64(len(lastEntry.NarText))

	assert.Equal(t, expectedSize, sizePulled, "size pulled is less than maxSize by exactly the last one")

	for _, narEntry := range allEntries {
		nu := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}

		var found bool

		for i := 1; i < 100; i++ {
			// NOTE: I tried runtime.Gosched() but it makes the test flaky
			time.Sleep(time.Duration(i) * time.Millisecond)

			found = c.narStore.HasNar(newContext(), nu)
			if found {
				break
			}
		}

		assert.True(t, found, nu.String()+" should exist in the store")
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

	// all nar_file records are in the database
	for _, narEntry := range allEntries {
		_, err := c.db.GetNarFileByHash(context.Background(), narEntry.NarHash)
		require.NoError(t, err)
	}

	c.runLRU(newContext())()

	// confirm all narinfos except the last one are in the store
	for _, nar := range entries {
		assert.True(t, c.narInfoStore.HasNarInfo(newContext(), nar.NarInfoHash))
	}

	assert.False(t, c.narInfoStore.HasNarInfo(newContext(), lastEntry.NarInfoHash))

	// confirm all nars except the last one are in the store
	for _, narEntry := range entries {
		nu := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}
		assert.True(t, c.narStore.HasNar(newContext(), nu))
	}

	nu := nar.URL{Hash: lastEntry.NarHash, Compression: lastEntry.NarCompression}
	assert.False(t, c.narStore.HasNar(newContext(), nu))

	// all narinfo records except the last one are in the database
	for _, narEntry := range entries {
		_, err := c.db.GetNarInfoByHash(context.Background(), narEntry.NarInfoHash)
		require.NoError(t, err)
	}

	_, err = c.db.GetNarInfoByHash(context.Background(), lastEntry.NarInfoHash)
	require.ErrorIs(t, err, database.ErrNotFound)

	// all nar_file records except the last one are in the database

	for _, narEntry := range entries {
		_, err := c.db.GetNarFileByHash(context.Background(), narEntry.NarHash)
		require.NoError(t, err)
	}

	_, err = c.db.GetNarFileByHash(context.Background(), lastEntry.NarHash)
	require.ErrorIs(t, err, database.ErrNotFound)
}

func TestRunLRUCleanupInconsistentNarInfoState(t *testing.T) {
	t.Parallel()

	c, cleanup := setupTestCache(t)
	defer cleanup()

	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), nil)
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	c.SetRecordAgeIgnoreTouch(0)

	// Wait for upstream caches to become available
	<-c.GetHealthChecker().Trigger()

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

	// create a dup of the last entry and change its hash and swap it so the rest
	// of my test work as before.
	{
		b := allEntries[len(allEntries)-1]
		a := b
		a.NarInfoHash = "7lwdzpsma6xz5678blcqr6f5q1caxjw2"
		allEntries = append(allEntries[:len(allEntries)-1], a, b)

		ts.AddEntry(a)
	}

	entries := allEntries[:len(allEntries)-1]
	lastEntry := allEntries[len(allEntries)-1]

	assert.Len(t, entries, len(allEntries)-1, "confirm entries length is correct")
	assert.Equal(t, allEntries, append(entries, lastEntry), "confirm my vars are correct")

	// define the maximum size of our store based on responses of our testdata
	// minus the last one
	var maxSize uint64
	for _, nar := range entries {
		maxSize += uint64(len(nar.NarText))
	}

	// allow LRU to remove the last entry so we can then assert that it was not
	// actually removed.
	c.SetMaxSize(maxSize - uint64(len(lastEntry.NarText)))

	var sizePulled int64

	for i, narEntry := range allEntries {
		_, err := c.GetNarInfo(context.Background(), narEntry.NarInfoHash)
		require.NoErrorf(t, err, "unable to get narinfo for idx %d", i)

		nu := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}
		size, reader, err := c.GetNar(context.Background(), nu)
		require.NoError(t, err, "unable to get nar for idx %d", i)

		// If the size is zero (likely) then the download is in progress so
		// compute the size by reading it fully first.
		if size < 0 {
			var err error

			size, err = io.Copy(io.Discard, reader)
			require.NoError(t, err)
		}

		sizePulled += size
	}

	//nolint:gosec
	expectedSize := int64(maxSize) + int64(len(lastEntry.NarText))

	assert.Equal(t, expectedSize, sizePulled, "size pulled is less than maxSize by exactly the last one")

	for _, narEntry := range allEntries {
		nu := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}

		var found bool

		for i := 1; i < 100; i++ {
			// NOTE: I tried runtime.Gosched() but it makes the test flaky
			time.Sleep(time.Duration(i) * time.Millisecond)

			found = c.narStore.HasNar(newContext(), nu)
			if found {
				break
			}
		}

		assert.True(t, found, nu.String()+" should exist in the store")
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

	// all nar_file records are in the database
	for _, narEntry := range allEntries {
		_, err := c.db.GetNarFileByHash(context.Background(), narEntry.NarHash)
		require.NoError(t, err)
	}

	c.runLRU(newContext())()

	// confirm all narinfos except the last one are in the store
	for _, nar := range entries {
		assert.True(t, c.narInfoStore.HasNarInfo(newContext(), nar.NarInfoHash))
	}

	assert.False(t, c.narInfoStore.HasNarInfo(newContext(), lastEntry.NarInfoHash))

	// confirm all nars are in the store, the last one should not be deleted
	// because it has another narinfo referring to it that was indeed pulled.
	for _, narEntry := range allEntries {
		nu := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}
		assert.True(t, c.narStore.HasNar(newContext(), nu))
	}

	// all narinfo records except the last one are in the database
	for _, narEntry := range entries {
		_, err := c.db.GetNarInfoByHash(context.Background(), narEntry.NarInfoHash)
		require.NoError(t, err)
	}

	_, err = c.db.GetNarInfoByHash(context.Background(), lastEntry.NarInfoHash)
	require.ErrorIs(t, err, database.ErrNotFound)

	// confirm all nar_file records are in the database, the last one should not
	// be deleted because it has another narinfo referring to it that was indeed
	// pulled.
	for _, narEntry := range allEntries {
		_, err := c.db.GetNarFileByHash(context.Background(), narEntry.NarHash)
		require.NoError(t, err)
	}
}

func TestRunLRUWithSharedNar(t *testing.T) {
	t.Parallel()

	c, cleanup := setupTestCache(t)
	defer cleanup()

	ctx := newContext()

	// Initial State:
	// ni4 (50 bytes) -> NarFile B
	// ni1 (100 bytes) -> NarFile A
	// ni2 (100 bytes) -> NarFile A
	// Total unique size: 150 bytes.

	// NarFile B (50 bytes), NarInfo 4 (oldest)
	narFileB, err := c.db.CreateNarFile(ctx, database.CreateNarFileParams{
		Hash:        "nar-file-b",
		Compression: "xz",
		FileSize:    50,
	})
	require.NoError(t, err)
	ni4, err := c.db.CreateNarInfo(ctx, database.CreateNarInfoParams{Hash: "nar-info-4"})
	require.NoError(t, err)
	require.NoError(t, c.db.LinkNarInfoToNarFile(ctx, database.LinkNarInfoToNarFileParams{
		NarInfoID: ni4.ID,
		NarFileID: narFileB.ID,
	}))

	// NarFile A (100 bytes), NarInfo 1
	narFileA, err := c.db.CreateNarFile(ctx, database.CreateNarFileParams{
		Hash:        "nar-file-a",
		Compression: "xz",
		FileSize:    100,
	})
	require.NoError(t, err)
	ni1, err := c.db.CreateNarInfo(ctx, database.CreateNarInfoParams{Hash: "nar-info-1"})
	require.NoError(t, err)
	require.NoError(t, c.db.LinkNarInfoToNarFile(ctx, database.LinkNarInfoToNarFileParams{
		NarInfoID: ni1.ID,
		NarFileID: narFileA.ID,
	}))

	// NarFile A (100 bytes), NarInfo 2
	ni2, err := c.db.CreateNarInfo(ctx, database.CreateNarInfoParams{Hash: "nar-info-2"})
	require.NoError(t, err)
	require.NoError(t, c.db.LinkNarInfoToNarFile(ctx, database.LinkNarInfoToNarFileParams{
		NarInfoID: ni2.ID,
		NarFileID: narFileA.ID,
	}))

	// Set deterministic timestamps to avoid time.Sleep and flaky tests.
	// We set ni4 (oldest), then ni1, then ni2 (newest).
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = c.db.DB().ExecContext(ctx,
		"UPDATE narinfos SET last_accessed_at = ? WHERE hash = ?", baseTime.Add(-3*time.Hour), "nar-info-4")
	require.NoError(t, err)
	_, err = c.db.DB().ExecContext(ctx,
		"UPDATE narinfos SET last_accessed_at = ? WHERE hash = ?", baseTime.Add(-2*time.Hour), "nar-info-1")
	require.NoError(t, err)
	_, err = c.db.DB().ExecContext(ctx,
		"UPDATE narinfos SET last_accessed_at = ? WHERE hash = ?", baseTime.Add(-1*time.Hour), "nar-info-2")
	require.NoError(t, err)

	// Set MaxSize to 0 to trigger eviction of all reclaimable records.
	// If the query double-counts, it selects ni such that sum <= 0.
	// With double-counting, sums are: ni4: 50, ni1: 150, ni2: 250.
	// without double-counting, sums are: ni4: 50, ni1: 150, ni2: 150.
	// We use maxSize = 0 to reclaim all 150 unique bytes.
	c.SetMaxSize(0)

	c.runLRU(ctx)()

	// Verify that all narinfos were deleted.
	_, err = c.db.GetNarInfoByHash(ctx, "nar-info-4")
	require.ErrorIs(t, err, database.ErrNotFound, "ni4 should have been deleted")
	_, err = c.db.GetNarInfoByHash(ctx, "nar-info-1")
	require.ErrorIs(t, err, database.ErrNotFound, "ni1 should have been deleted")
	_, err = c.db.GetNarInfoByHash(ctx, "nar-info-2")
	require.ErrorIs(t, err, database.ErrNotFound, "ni2 should have been deleted")

	// Verify that all nar files were deleted as they are now orphaned.
	_, err = c.db.GetNarFileByHash(ctx, "nar-file-a")
	require.ErrorIs(t, err, database.ErrNotFound, "nar-file-a should have been deleted")
	_, err = c.db.GetNarFileByHash(ctx, "nar-file-b")
	require.ErrorIs(t, err, database.ErrNotFound, "nar-file-b should have been deleted")
}

func TestStoreInDatabaseDuplicateDetection(t *testing.T) {
	t.Parallel()

	c, cleanup := setupTestCache(t)
	defer cleanup()

	// Parse narinfo from testdata
	narInfoReader := strings.NewReader(testdata.Nar1.NarInfoText)
	narInfo, err := narinfo.Parse(narInfoReader)
	require.NoError(t, err)

	// First insert should succeed
	err = c.storeInDatabase(newContext(), testdata.Nar1.NarInfoHash, narInfo)
	require.NoError(t, err, "first insert should succeed")

	// Verify the record was created
	_, err = c.db.GetNarInfoByHash(newContext(), testdata.Nar1.NarInfoHash)
	require.NoError(t, err, "record should exist in database")

	// Second insert of the same narinfo should succeed (UPSERT)
	err = c.storeInDatabase(newContext(), testdata.Nar1.NarInfoHash, narInfo)
	require.NoError(t, err, "duplicate insert should succeed with UPSERT")

	// Verify the record persists and ID is consistent
	ni2, err := c.db.GetNarInfoByHash(newContext(), testdata.Nar1.NarInfoHash)
	require.NoError(t, err, "record should exist in database")

	require.NotEmpty(t, ni2.ID)
}

func TestPutNarInfoConcurrentSameHash(t *testing.T) {
	t.Parallel()

	c, cleanup := setupTestCache(t)
	defer cleanup()

	// Test concurrent PutNarInfo calls for the same hash
	// This tests hash-specific locking - multiple goroutines trying to write the same narinfo
	// should be properly synchronized with only one succeeding
	const numGoroutines = 10

	type result struct {
		err error
	}

	results := make(chan result, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			// Each goroutine gets its own reader
			r := io.NopCloser(strings.NewReader(testdata.Nar1.NarInfoText))

			err := c.PutNarInfo(newContext(), testdata.Nar1.NarInfoHash, r)
			results <- result{err: err}
		}()
	}

	// Collect results
	var successCount int

	for i := 0; i < numGoroutines; i++ {
		res := <-results
		if res.err == nil {
			successCount++
		} else {
			t.Logf("goroutine error: %v", res.err)
		}
	}

	// All PutNarInfo calls should succeed (PUT should be idempotent)
	// Bug: without proper ErrAlreadyExists handling in PutNarInfo, some may return errors
	require.Equal(t, numGoroutines, successCount, "all PutNarInfo calls should succeed (PUT should be idempotent)")

	// Verify the narinfo exists in storage and database
	ni, err := c.narInfoStore.GetNarInfo(newContext(), testdata.Nar1.NarInfoHash)
	require.NoError(t, err, "narinfo should exist in storage")
	require.NotNil(t, ni)

	_, err = c.db.GetNarInfoByHash(newContext(), testdata.Nar1.NarInfoHash)
	require.NoError(t, err, "narinfo should exist in database")
}

// TestPutNarInfoWithSharedNar verifies that multiple narinfos can share the same nar_file.
//
// Scenario:
// 1. Store a NarInfo (Nar1) - this creates both narinfo and nar_file records
// 2. Store a different NarInfo (different store path) that happens to have the same nar URL
//
// Expected behavior: Both narinfos should be stored successfully and share the same nar_file.
// This is the correct behavior with the many-to-many relationship between narinfos and nar_files.
func TestPutNarInfoWithSharedNar(t *testing.T) {
	t.Parallel()

	c, cleanup := setupTestCache(t)
	defer cleanup()

	ctx := newContext()

	// Step 1: Store the first NarInfo (Nar1) - this creates both narinfo and nar_file records
	err := c.PutNarInfo(ctx, testdata.Nar1.NarInfoHash, io.NopCloser(strings.NewReader(testdata.Nar1.NarInfoText)))
	require.NoError(t, err, "first PutNarInfo should succeed")

	// Verify first narinfo exists in database
	narInfo1, err := c.db.GetNarInfoByHash(ctx, testdata.Nar1.NarInfoHash)
	require.NoError(t, err, "first narinfo should exist in database")
	require.NotNil(t, narInfo1)

	// Step 2: Create a second NarInfo with a different hash but same nar URL
	// This simulates a different store path that produces the same nar
	secondNarInfoHash := "different1234567890abcdefghijklmno" // Different from Nar1.NarInfoHash
	secondNarInfoText := `StorePath: /nix/store/different1234567890abcdefghijklmno-hello-2.12.1
URL: nar/1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri.nar.xz
Compression: xz
FileHash: sha256:1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri
FileSize: 50160
NarHash: sha256:07kc6swib31psygpmwi8952lvywlpqn474059yxl7grwsvr6k0fj
NarSize: 226552
References: different1234567890abcdefghijklmno-hello-2.12.1 qdcbgcj27x2kpxj2sf9yfvva7qsgg64g-glibc-2.38-77
Deriver: 9zpqmcicrg8smi9jlqv6dmd7v20d2fsn-hello-2.12.1.drv
Sig: cache.nixos.org-1:MadTCU1OSFCGUw4aqCKpLCZJpqBc7AbLvO7wgdlls0eq1DwaSnF/82SZE+wJGEiwlHbnZR+14daSaec0W3XoBQ==`

	// Step 3: Store the second NarInfo
	// This should succeed and reuse the existing nar_file
	err = c.PutNarInfo(ctx, secondNarInfoHash, io.NopCloser(strings.NewReader(secondNarInfoText)))
	require.NoError(t, err, "second PutNarInfo should succeed and reuse existing nar_file")

	// Step 4: Verify both narinfos exist in database
	narInfo2, err := c.db.GetNarInfoByHash(ctx, secondNarInfoHash)
	require.NoError(t, err, "second narinfo should exist in database")
	require.NotNil(t, narInfo2)

	// Step 5: Verify both narinfos share the same nar_file
	narFile1, err := c.db.GetNarFileByNarInfoID(ctx, narInfo1.ID)
	require.NoError(t, err, "should be able to get nar_file for first narinfo")

	narFile2, err := c.db.GetNarFileByNarInfoID(ctx, narInfo2.ID)
	require.NoError(t, err, "should be able to get nar_file for second narinfo")

	// Both should reference the same nar_file
	require.Equal(t, narFile1.ID, narFile2.ID, "both narinfos should share the same nar_file")
	require.Equal(t, narFile1.Hash, narFile2.Hash, "nar_file hashes should match")
}

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}

// TestWithReadLock tests the withReadLock helper function.
func TestWithReadLock(t *testing.T) {
	t.Parallel()

	c, cleanup := setupTestCache(t)
	defer cleanup()

	ctx := newContext()

	t.Run("successful lock acquisition and release", func(t *testing.T) {
		t.Parallel()

		executed := false
		err := c.withReadLock(ctx, "test", func() error {
			executed = true

			return nil
		})

		require.NoError(t, err)
		assert.True(t, executed, "function should have been executed")
	})

	t.Run("function error is propagated", func(t *testing.T) {
		t.Parallel()

		err := c.withReadLock(ctx, "test", func() error {
			return errTest
		})

		require.ErrorIs(t, err, errTest)
	})
}

// TestWithWriteLock tests the withWriteLock helper function.
func TestWithWriteLock(t *testing.T) {
	t.Parallel()

	c, cleanup := setupTestCache(t)
	defer cleanup()

	ctx := newContext()

	t.Run("successful lock acquisition and release", func(t *testing.T) {
		t.Parallel()

		executed := false
		err := c.withWriteLock(ctx, "test", "test-key", func() error {
			executed = true

			return nil
		})

		require.NoError(t, err)
		assert.True(t, executed, "function should have been executed")
	})

	t.Run("function error is propagated", func(t *testing.T) {
		t.Parallel()

		err := c.withWriteLock(ctx, "test", "test-key", func() error {
			return errTest
		})

		require.ErrorIs(t, err, errTest)
	})

	t.Run("concurrent writes are serialized", func(t *testing.T) {
		t.Parallel()

		const numGoroutines = 10

		var counter int

		var wg sync.WaitGroup

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)

			go func() {
				defer wg.Done()

				err := c.withWriteLock(ctx, "test", "shared-key", func() error {
					// This critical section is now correctly protected only by withWriteLock.
					// A temporary variable is used to simulate a read-modify-write data race.
					current := counter
					// Simulate work to increase the chance of a race if the lock is not held.
					time.Sleep(time.Millisecond)

					counter = current + 1

					return nil
				})
				assert.NoError(t, err)
			}()
		}

		wg.Wait()

		assert.Equal(t, numGoroutines, counter, "all increments should have been performed")
	})
}

// TestWithTryLock tests the withTryLock helper function.
func TestWithTryLock(t *testing.T) {
	t.Parallel()

	c, cleanup := setupTestCache(t)
	defer cleanup()

	ctx := newContext()

	//nolint:paralleltest
	t.Run("successful lock acquisition and release", func(t *testing.T) {
		executed := false
		acquired, err := c.withTryLock(ctx, "test", "test-key", func() error {
			executed = true

			return nil
		})

		require.NoError(t, err)
		assert.True(t, acquired, "lock should have been acquired")
		assert.True(t, executed, "function should have been executed")
	})

	//nolint:paralleltest
	t.Run("function error is propagated", func(t *testing.T) {
		acquired, err := c.withTryLock(ctx, "test", "test-key", func() error {
			return errTest
		})

		require.ErrorIs(t, err, errTest)
		assert.True(t, acquired, "lock should have been acquired even though function failed")
	})

	//nolint:paralleltest
	t.Run("lock not acquired if already held", func(t *testing.T) {
		lockKey := "contended-key"

		// First goroutine acquires the lock and holds it
		firstAcquired := make(chan struct{})
		firstDone := make(chan struct{})

		go func() {
			acquired, err := c.withTryLock(ctx, "test", lockKey, func() error {
				close(firstAcquired)
				<-firstDone

				return nil
			})
			assert.NoError(t, err)
			assert.True(t, acquired)
		}()

		// Wait for the first goroutine to acquire the lock
		<-firstAcquired

		// Second goroutine tries to acquire the lock (should fail)
		secondExecuted := false
		acquired, err := c.withTryLock(ctx, "test", lockKey, func() error {
			secondExecuted = true

			return nil
		})

		require.NoError(t, err)
		assert.False(t, acquired, "lock should not have been acquired")
		assert.False(t, secondExecuted, "function should not have been executed")

		// Release the first lock
		close(firstDone)

		// Wait a bit to ensure the lock is released
		time.Sleep(100 * time.Millisecond)

		// Third goroutine should now be able to acquire the lock
		thirdExecuted := false
		acquired, err = c.withTryLock(ctx, "test", lockKey, func() error {
			thirdExecuted = true

			return nil
		})

		require.NoError(t, err)
		assert.True(t, acquired, "lock should have been acquired after release")
		assert.True(t, thirdExecuted, "function should have been executed")
	})
}

func TestMigration_DataIntegrity(t *testing.T) {
	t.Parallel()

	c, cleanup := setupTestCache(t)
	defer cleanup()

	ctx := newContext()

	// 1. Setup: Insert a "finished" record (simulating an already migrated or valid record)
	// We use the exact data from testdata.Nar1
	narInfo, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
	require.NoError(t, err)

	err = c.storeInDatabase(ctx, testdata.Nar1.NarInfoHash, narInfo)
	require.NoError(t, err)

	// Verify it exists and has the correct URL
	niOriginal, err := c.db.GetNarInfoByHash(ctx, testdata.Nar1.NarInfoHash)
	require.NoError(t, err)
	require.True(t, niOriginal.URL.Valid)
	require.Equal(t, "nar/1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri.nar.xz", niOriginal.URL.String)

	// 2. Action: Attempt to "migrate" (insert) different data for the same hash
	// We create a modified narinfo that would damage the record if overwritten
	modifiedNarInfo := *narInfo
	modifiedNarInfo.Deriver = "damaging-change-deriver"

	// This call should succeed (idempotent) but NOT update the DB record because it's already valid
	err = c.storeInDatabase(ctx, testdata.Nar1.NarInfoHash, &modifiedNarInfo)
	require.NoError(t, err)

	// 3. Verification: Verify the DB record is UNTOUCHED
	niAfter, err := c.db.GetNarInfoByHash(ctx, testdata.Nar1.NarInfoHash)
	require.NoError(t, err)
	assert.Equal(t, niOriginal.Deriver.String, niAfter.Deriver.String, "Existing valid record should NOT be overwritten")
	assert.NotEqual(t, modifiedNarInfo.Deriver, niAfter.Deriver.String, "Bad Deriver should not be present")
}

func TestMigration_Success(t *testing.T) {
	t.Parallel()

	c, cleanup := setupTestCache(t)
	defer cleanup()

	ctx := newContext()

	// 1. Setup: Insert a "partial" record (URL is NULL), simulating an unmigrated state
	// We manually insert this to bypass storeInDatabase's logic
	insertSQL := buildInsertNarInfoSQL(c.db)
	_, err := c.db.DB().ExecContext(
		ctx,
		insertSQL,
		testdata.Nar1.NarInfoHash,
		time.Now(),
	)
	require.NoError(t, err)

	// Verify it is indeed partial
	niPartial, err := c.db.GetNarInfoByHash(ctx, testdata.Nar1.NarInfoHash)
	require.NoError(t, err)
	require.False(t, niPartial.URL.Valid, "URL should be NULL initially")

	// 2. Action: Run storeInDatabase with the full valid data
	narInfo, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
	require.NoError(t, err)

	err = c.storeInDatabase(ctx, testdata.Nar1.NarInfoHash, narInfo)
	require.NoError(t, err)

	// 3. Verification: Verify the DB record IS updated
	niAfter, err := c.db.GetNarInfoByHash(ctx, testdata.Nar1.NarInfoHash)
	require.NoError(t, err)
	require.True(t, niAfter.URL.Valid, "URL should be valid after migration")
	assert.Equal(t, "nar/1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri.nar.xz", niAfter.URL.String)
}

func TestMigration_UpsertIdempotency(t *testing.T) {
	t.Parallel()

	// This test verifies that UPSERT operations are idempotent and transaction-safe.
	// With the ON CONFLICT DO UPDATE/NOTHING approach, duplicate inserts should not
	// abort transactions or cause errors when attempting to store existing records.

	c, cleanup := setupTestCache(t)
	defer cleanup()

	ctx := newContext()

	// 1. Setup: Create a record
	narInfo, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
	require.NoError(t, err)

	err = c.storeInDatabase(ctx, testdata.Nar1.NarInfoHash, narInfo)
	require.NoError(t, err)

	// 2. Action: concurrent writes to trigger potential race/locking issues
	// We use a transaction to wrap multiple operations to ensure the "abort" behavior would be caught if present
	err = c.withTransaction(ctx, "test_transaction_safety", func(qtx database.Querier) error {
		// Attempt to store the same record again within a transaction
		// If the logic is "try insert, fail, delete, insert", the "fail" part aborts the transaction in Postgres

		// Note: we can't easily call storeInDatabase here because it starts its own transaction.
		// Instead, we manually call the CreateNarInfo which is what storeInDatabase does.
		createNarInfoParams := database.CreateNarInfoParams{
			Hash: testdata.Nar1.NarInfoHash,
			// ... other params irrelevant for the crash, it fails on Hash unique constraint
		}

		// In the *incorrect* impl, this returns an error, which aborts the tx.
		// In the *correct* impl (UPSERT), this returns success (or 0 rows affected), guarding the tx.
		_, err := qtx.CreateNarInfo(ctx, createNarInfoParams)

		// With conditional upsert, if no update is performed, SQLite/Postgres might return ErrNoRows
		// (if using RETURNING). This is NOT a transaction aborting error.
		if err != nil && !database.IsNotFoundError(err) {
			return err
		}

		// If we are using Postgres, and CreateNarInfo failed (aborted tx), this next query would fail
		// with "current transaction is aborted"
		_, _ = qtx.GetNarInfoByHash(ctx, testdata.Nar1.NarInfoHash)

		return nil
	})
	require.NoError(t, err)

	// With UPSERT, we expect NO error here.
	// With the original bug, we might get an error or not depending on how CreateNarInfo was implemented.
	// But `storeInDatabase` (the high level function) specifically failed because it tried to recover.

	// Let's test `storeInDatabase` directly as that's what we care about.
	err = c.storeInDatabase(ctx, testdata.Nar1.NarInfoHash, narInfo)
	assert.NoError(t, err, "storeInDatabase should allow re-storing existing records safely")
}

func TestMigration_PartialRecordWithExistingReferences(t *testing.T) {
	t.Parallel()

	c, cleanup := setupTestCache(t)
	defer cleanup()

	ctx := newContext()

	// 1. Parse the narinfo to get the full data
	narInfo, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
	require.NoError(t, err)

	// 2. Manually insert a partial record with NULL URL
	insertSQL := buildInsertNarInfoSQL(c.db)
	_, err = c.db.DB().ExecContext(
		ctx,
		insertSQL,
		testdata.Nar1.NarInfoHash,
		time.Now(),
	)
	require.NoError(t, err)

	// 3. Get the narinfo ID
	niPartial, err := c.db.GetNarInfoByHash(ctx, testdata.Nar1.NarInfoHash)
	require.NoError(t, err)
	require.False(t, niPartial.URL.Valid, "URL should be NULL initially")

	// 4. Add some references to the partial record (simulating a partial migration)
	if len(narInfo.References) > 0 {
		// Add only the first reference
		err = c.db.AddNarInfoReference(ctx, database.AddNarInfoReferenceParams{
			NarInfoID: niPartial.ID,
			Reference: narInfo.References[0],
		})
		require.NoError(t, err)
	}

	// 5. Now attempt full migration via storeInDatabase (which includes all references)
	// This should handle duplicate references gracefully
	err = c.storeInDatabase(ctx, testdata.Nar1.NarInfoHash, narInfo)
	require.NoError(t, err, "Migration should succeed even with existing references")

	// 6. Verify the record is now complete
	niAfter, err := c.db.GetNarInfoByHash(ctx, testdata.Nar1.NarInfoHash)
	require.NoError(t, err)
	require.True(t, niAfter.URL.Valid, "URL should be valid after migration")

	// 7. Verify all references exist (no duplicates, no missing)
	refs, err := c.db.GetNarInfoReferences(ctx, niAfter.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, narInfo.References, refs, "All references should be present exactly once")
}

func TestDeleteNarInfo_WithNullURL(t *testing.T) {
	t.Parallel()

	c, cleanup := setupTestCache(t)
	defer cleanup()

	ctx := newContext()

	// 1. Create a partial record with NULL URL (simulating pre-migration state)
	insertSQL := buildInsertNarInfoSQL(c.db)
	_, err := c.db.DB().ExecContext(
		ctx,
		insertSQL,
		testdata.Nar1.NarInfoHash,
		time.Now(),
	)
	require.NoError(t, err)

	// 2. Add some references and signatures
	niPartial, err := c.db.GetNarInfoByHash(ctx, testdata.Nar1.NarInfoHash)
	require.NoError(t, err)

	err = c.db.AddNarInfoReference(ctx, database.AddNarInfoReferenceParams{
		NarInfoID: niPartial.ID,
		Reference: "/nix/store/test-ref1",
	})
	require.NoError(t, err)

	err = c.db.AddNarInfoSignature(ctx, database.AddNarInfoSignatureParams{
		NarInfoID: niPartial.ID,
		Signature: "test-signature:1234567890abcdef",
	})
	require.NoError(t, err)

	// 3. Verify the record exists
	_, err = c.db.GetNarInfoByHash(ctx, testdata.Nar1.NarInfoHash)
	require.NoError(t, err)

	// 4. Delete the narinfo
	err = c.DeleteNarInfo(ctx, testdata.Nar1.NarInfoHash)
	require.NoError(t, err, "Should be able to delete narinfo with NULL URL")

	// 5. Verify the record is gone from database
	_, err = c.db.GetNarInfoByHash(ctx, testdata.Nar1.NarInfoHash)
	require.ErrorIs(t, err, database.ErrNotFound, "Record should be deleted from database")

	// 6. Verify references are also gone (cascade delete)
	refs, err := c.db.GetNarInfoReferences(ctx, niPartial.ID)
	if err == nil {
		assert.Empty(t, refs, "References should be deleted via cascade")
	}

	// 7. Verify signatures are also gone (cascade delete)
	sigs, err := c.db.GetNarInfoSignatures(ctx, niPartial.ID)
	if err == nil {
		assert.Empty(t, sigs, "Signatures should be deleted via cascade")
	}
}

func TestMigration_DatabaseBehaviorConsistency(t *testing.T) {
	t.Parallel()

	// This test verifies that the UPSERT behavior is consistent across all database engines.
	// It focuses on the two critical scenarios:
	// 1. Updating a record with NULL URL (migration)
	// 2. Not updating a record with valid URL (data protection)

	testCases := []struct {
		name           string
		setupFn        func(t *testing.T, c *Cache, ctx context.Context, hash string)
		attemptInsert  func(t *testing.T, c *Cache, ctx context.Context, hash string, narInfo *narinfo.NarInfo)
		validateResult func(t *testing.T, c *Cache, ctx context.Context, hash string, expectedURL string)
	}{
		{
			name: "NULL URL should be updated",
			setupFn: func(t *testing.T, c *Cache, ctx context.Context, hash string) {
				t.Helper()
				// Insert partial record with NULL URL
				insertSQL := buildInsertNarInfoSQL(c.db)
				_, err := c.db.DB().ExecContext(ctx, insertSQL, hash, time.Now())
				require.NoError(t, err)
			},
			attemptInsert: func(t *testing.T, c *Cache, ctx context.Context, hash string, narInfo *narinfo.NarInfo) {
				t.Helper()

				err := c.storeInDatabase(ctx, hash, narInfo)
				require.NoError(t, err)
			},
			validateResult: func(t *testing.T, c *Cache, ctx context.Context, hash string, expectedURL string) {
				t.Helper()

				ni, err := c.db.GetNarInfoByHash(ctx, hash)
				require.NoError(t, err)
				require.True(t, ni.URL.Valid, "URL should be valid after update")
				assert.Equal(t, expectedURL, ni.URL.String, "URL should match the inserted value")
			},
		},
		{
			name: "Valid URL should NOT be overwritten",
			setupFn: func(t *testing.T, c *Cache, ctx context.Context, hash string) {
				t.Helper()
				// Insert full record first
				originalNarInfo, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
				require.NoError(t, err)
				err = c.storeInDatabase(ctx, hash, originalNarInfo)
				require.NoError(t, err)
			},
			attemptInsert: func(t *testing.T, c *Cache, ctx context.Context, hash string, narInfo *narinfo.NarInfo) {
				t.Helper()
				// Try to insert different data
				modifiedNarInfo := *narInfo
				modifiedNarInfo.Deriver = "should-not-appear"
				err := c.storeInDatabase(ctx, hash, &modifiedNarInfo)
				require.NoError(t, err) // Should succeed but not update
			},
			validateResult: func(t *testing.T, c *Cache, ctx context.Context, hash string, expectedURL string) {
				t.Helper()

				ni, err := c.db.GetNarInfoByHash(ctx, hash)
				require.NoError(t, err)
				require.True(t, ni.URL.Valid, "URL should still be valid")
				assert.Equal(t, expectedURL, ni.URL.String, "URL should be unchanged")
				// Verify the attempted modification didn't apply
				assert.NotEqual(t, "should-not-appear", ni.Deriver.String, "Deriver should not be overwritten")
			},
		},
	}

	// Helper function to run tests against a specific database backend
	runTestsWithDB := func(t *testing.T, setupDB func(*testing.T) (database.Querier, func())) {
		t.Helper()

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				ctx := newContext()

				// Setup database
				db, dbCleanup := setupDB(t)
				defer dbCleanup()

				// Setup storage
				dir, err := os.MkdirTemp("", "cache-path-")
				require.NoError(t, err)

				defer os.RemoveAll(dir)

				localStore, err := local.New(ctx, dir)
				require.NoError(t, err)

				// Use local locks for tests
				downloadLocker := locklocal.NewLocker()
				cacheLocker := locklocal.NewRWLocker()

				c, err := New(ctx, cacheName, db, localStore, localStore, localStore, "",
					downloadLocker, cacheLocker, downloadLockTTL, cacheLockTTL)
				require.NoError(t, err)

				// Parse test narinfo
				narInfo, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
				require.NoError(t, err)

				hash := testdata.Nar1.NarInfoHash
				expectedURL := "nar/1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri.nar.xz"

				// Setup
				tc.setupFn(t, c, ctx, hash)

				// Attempt insert/update
				tc.attemptInsert(t, c, ctx, hash, narInfo)

				// Validate
				tc.validateResult(t, c, ctx, hash, expectedURL)
			})
		}
	}

	// Test with SQLite (always runs)
	t.Run("SQLite", func(t *testing.T) {
		t.Parallel()
		runTestsWithDB(t, testhelper.SetupSQLite)
	})

	// Test with PostgreSQL (only if enabled via environment variable)
	t.Run("PostgreSQL", func(t *testing.T) {
		t.Parallel()
		runTestsWithDB(t, testhelper.SetupPostgres)
	})

	// Test with MySQL (only if enabled via environment variable)
	t.Run("MySQL", func(t *testing.T) {
		t.Parallel()
		runTestsWithDB(t, testhelper.SetupMySQL)
	})
}
