package cache

import (
	"context"
	"database/sql"
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

	// all nar records are in the database
	for _, narEntry := range allEntries {
		_, err := c.db.GetNarByHash(context.Background(), narEntry.NarHash)
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
	require.ErrorIs(t, sql.ErrNoRows, err)

	// all nar records except the last one are in the database

	for _, narEntry := range entries {
		_, err := c.db.GetNarByHash(context.Background(), narEntry.NarHash)
		require.NoError(t, err)
	}

	_, err = c.db.GetNarByHash(context.Background(), lastEntry.NarHash)
	require.ErrorIs(t, sql.ErrNoRows, err)
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

	// Second insert of the same narinfo should return ErrAlreadyExists
	err = c.storeInDatabase(newContext(), testdata.Nar1.NarInfoHash, narInfo)
	require.ErrorIs(
		t,
		err,
		ErrAlreadyExists,
		"duplicate insert should return ErrAlreadyExists to allow caller to distinguish from successful insert",
	)
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

// TestPutNarInfoWithOrphanedNar tests the scenario where a Nar record exists in the database
// but a new NarInfo is being added that references the same nar hash.
// This can happen if:
// 1. A previous NarInfo was stored with its Nar
// 2. Now we're storing a different NarInfo (different store path) that happens to have the same Nar
//
// Expected behavior: The transaction should fail and roll back cleanly, returning an
// ErrInconsistentState to the caller, preventing a silent failure.
// A more advanced implementation might allow reusing the existing NAR, but that would
// likely require schema changes and is out of scope for the current fix.
//
// Bug: Previously, this scenario caused a silent failure where:
// - CreateNarInfo succeeded (new narinfo hash)
// - CreateNar failed with a duplicate key error (same nar hash)
// - The transaction rolled back (removing the narinfo we just created)
// - But the function returned success (ErrAlreadyExists was treated as success by the caller).
func TestPutNarInfoWithOrphanedNar(t *testing.T) {
	t.Parallel()

	c, cleanup := setupTestCache(t)
	defer cleanup()

	ctx := newContext()

	// Step 1: Store the first NarInfo (Nar1) - this creates both narinfo and nar records
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

	// Step 3: Try to store the second NarInfo
	// This should return ErrInconsistentState because the nar already exists with a different narinfo
	err = c.PutNarInfo(ctx, secondNarInfoHash, io.NopCloser(strings.NewReader(secondNarInfoText)))
	require.Error(t, err, "second PutNarInfo should return an error due to nar conflict")
	require.ErrorIs(t, err, ErrInconsistentState, "error should be ErrInconsistentState")

	// Step 4: Verify the second narinfo does NOT exist in database (since the operation failed)
	_, err = c.db.GetNarInfoByHash(ctx, secondNarInfoHash)
	require.Error(t, err, "second narinfo should not exist in database since PutNarInfo failed")
	require.ErrorIs(t, err, sql.ErrNoRows, "should get sql.ErrNoRows for non-existent narinfo")
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

		var mu sync.Mutex

		var wg sync.WaitGroup

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)

			go func() {
				defer wg.Done()

				err := c.withWriteLock(ctx, "test", "shared-key", func() error {
					// Read the counter
					mu.Lock()

					current := counter

					mu.Unlock()

					// Simulate some work
					time.Sleep(time.Millisecond)

					// Increment the counter
					mu.Lock()

					counter = current + 1

					mu.Unlock()

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
