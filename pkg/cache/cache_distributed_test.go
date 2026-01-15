package cache_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	locklocal "github.com/kalbasit/ncps/pkg/lock/local"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/redis"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

const (
	numInstances   = 3
	redisTestsFlag = "NCPS_ENABLE_REDIS_TESTS"
)

// skipIfRedisNotAvailable skips the test if Redis tests are not enabled.
func skipIfRedisNotAvailable(t *testing.T) {
	t.Helper()

	if os.Getenv(redisTestsFlag) != "1" {
		t.Skipf("Skipping Redis test: %s is not set to 1", redisTestsFlag)
	}
}

// TestDistributedDownloadDeduplication verifies that when multiple cache instances
// request the same package, only one instance downloads it from the upstream source.
func TestDistributedDownloadDeduplication(t *testing.T) {
	t.Parallel()
	skipIfRedisNotAvailable(t)

	ctx := newContext()

	// Start test server
	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	// Track actual upstream downloads by instrumenting the test server
	var upstreamDownloads atomic.Int32

	narEntry := testdata.Entries[0]
	narPath := "/nar/" + narEntry.NarHash + ".nar." + narEntry.NarCompression.ToFileExtension()

	// Add a handler to count NAR downloads from upstream
	// Only count GET requests (actual downloads), not HEAD requests
	handlerID := ts.AddMaybeHandler(func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == narPath && r.Method == http.MethodGet {
			upstreamDownloads.Add(1)
		}

		return false // Let the default handler process the request
	})
	defer ts.RemoveMaybeHandler(handlerID)

	// Create shared directory for all instances
	sharedDir, err := os.MkdirTemp("", "cache-distributed-")
	require.NoError(t, err)

	defer os.RemoveAll(sharedDir)

	// Create shared database
	dbFile := filepath.Join(sharedDir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	// Create shared storage
	sharedStore, err := local.New(ctx, sharedDir)
	require.NoError(t, err)

	// Create Redis locks with unique prefix for this test
	// Default to localhost:6379 for local development
	redisAddrs := []string{"localhost:6379"}
	// Use environment variable if set (for CI with dynamic ports)
	if envAddrs := os.Getenv("NCPS_TEST_REDIS_ADDRS"); envAddrs != "" {
		redisAddrs = []string{envAddrs}
	}

	redisCfg := redis.Config{
		Addrs:     redisAddrs,
		KeyPrefix: "ncps:test:dedup:",
	}

	retryCfg := lock.RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     2 * time.Second,
		Jitter:       true,
	}

	// Create multiple cache instances
	var caches []*cache.Cache

	for i := 0; i < numInstances; i++ {
		downloadLocker, err := redis.NewLocker(ctx, redisCfg, retryCfg, false)
		require.NoError(t, err)

		cacheLocker, err := redis.NewRWLocker(ctx, redisCfg, retryCfg, false)
		require.NoError(t, err)

		// Create separate upstream cache for each instance to avoid data races
		uc, err := upstream.New(ctx, testhelper.MustParseURL(t, ts.URL), &upstream.Options{
			PublicKeys: testdata.PublicKeys(),
		})
		require.NoError(t, err)

		c, err := cache.New(
			ctx,
			cacheName,
			db,
			sharedStore,
			sharedStore,
			sharedStore,
			sharedStore,
			"",
			downloadLocker,
			cacheLocker,
			5*time.Minute,
			30*time.Minute,
		)
		require.NoError(t, err)

		c.AddUpstreamCaches(ctx, uc)
		c.SetRecordAgeIgnoreTouch(0)

		// Wait for this instance's upstream to become healthy
		<-c.GetHealthChecker().Trigger()

		caches = append(caches, c)
	}

	// Track GetNar attempts from all instances
	var getNarAttempts atomic.Int32

	// Simulate concurrent requests from all instances for the same NAR
	var wg sync.WaitGroup

	for i, c := range caches {
		wg.Add(1)

		go func(instanceNum int, cacheInstance *cache.Cache) {
			defer wg.Done()

			getNarAttempts.Add(1)

			// All instances request the same NAR
			narURL := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}
			_, reader, err := cacheInstance.GetNar(ctx, narURL)
			assert.NoError(t, err, "instance %d read failed", instanceNum)

			if reader != nil {
				_, err := io.Copy(io.Discard, reader)
				assert.NoError(t, err, "instance %d discarding body failed", instanceNum)
			}
		}(i, c)
	}

	wg.Wait()

	// All instances should have attempted the GetNar operation
	attempts := getNarAttempts.Load()
	assert.Equal(t, int32(numInstances), attempts,
		"all instances should attempt GetNar")

	// Verify deduplication: only ONE instance should have downloaded from upstream
	upstreamCount := upstreamDownloads.Load()
	assert.Equal(t, int32(1), upstreamCount,
		"only one instance should download from upstream (deduplication)")

	// Verify all instances can now read the cached file
	for i, c := range caches {
		narURL := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}
		size, reader, err := c.GetNar(ctx, narURL)
		require.NoError(t, err, "instance %d should read cached NAR", i)
		assert.Positive(t, size, "instance %d should have positive size", i)

		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		assert.Len(t, data, len(narEntry.NarText),
			"instance %d should read complete NAR", i)
	}
}

// TestDistributedConcurrentReads verifies that multiple instances can
// concurrently read the same cached files.
func TestDistributedConcurrentReads(t *testing.T) {
	t.Parallel()
	skipIfRedisNotAvailable(t)

	ctx := newContext()

	// Start test server
	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	// Create upstream cache
	uc, err := upstream.New(ctx, testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	// Create shared directory for all instances
	sharedDir, err := os.MkdirTemp("", "cache-reads-")
	require.NoError(t, err)

	defer os.RemoveAll(sharedDir)

	// Create shared database
	dbFile := filepath.Join(sharedDir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	// Create shared storage
	sharedStore, err := local.New(ctx, sharedDir)
	require.NoError(t, err)

	// For this test, use local locks since we're testing read concurrency,
	// not distributed locking coordination
	downloadLocker := locklocal.NewLocker()
	cacheLocker := locklocal.NewRWLocker()

	// Create first instance to populate the cache
	c1, err := cache.New(
		ctx,
		cacheName,
		db,
		sharedStore,
		sharedStore,
		sharedStore,
		sharedStore,
		"",
		downloadLocker,
		cacheLocker,
		5*time.Minute,
		30*time.Minute,
	)
	require.NoError(t, err)

	c1.AddUpstreamCaches(ctx, uc)
	c1.SetRecordAgeIgnoreTouch(0)

	// Wait for upstream caches to become available
	<-c1.GetHealthChecker().Trigger()

	// Pre-populate cache with a NAR
	narEntry := testdata.Entries[0]
	narURL := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}

	_, reader, err := c1.GetNar(ctx, narURL)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, reader)
	require.NoError(t, err)

	// Give it a moment to fully cache
	time.Sleep(500 * time.Millisecond)

	// Now create multiple instances that will read concurrently
	var caches []*cache.Cache

	for i := 0; i < numInstances; i++ {
		downloadLocker := locklocal.NewLocker()
		cacheLocker := locklocal.NewRWLocker()

		// Don't create upstream cache for read-only instances
		c, err := cache.New(
			ctx,
			cacheName,
			db,
			sharedStore,
			sharedStore,
			sharedStore,
			sharedStore,
			"",
			downloadLocker,
			cacheLocker,
			5*time.Minute,
			30*time.Minute,
		)
		require.NoError(t, err)

		caches = append(caches, c)
	}

	// All instances read the same NAR concurrently
	var (
		wg        sync.WaitGroup
		readCount atomic.Int32
	)

	for i, c := range caches {
		wg.Add(1)

		go func(instanceNum int, cacheInstance *cache.Cache) {
			defer wg.Done()

			size, reader, err := cacheInstance.GetNar(ctx, narURL)
			assert.NoError(t, err, "instance %d read failed", instanceNum)
			assert.Positive(t, size, "instance %d got zero size", instanceNum)

			if reader != nil {
				data, err := io.ReadAll(reader)
				assert.NoError(t, err, "instance %d read body failed", instanceNum)
				assert.Len(t, data, len(narEntry.NarText),
					"instance %d read wrong size", instanceNum)
			}

			readCount.Add(1)
		}(i, c)
	}

	wg.Wait()

	// Verify all instances successfully read
	assert.Equal(t, int32(numInstances), readCount.Load(),
		"all instances should successfully read")
}

// TestDistributedLockFailover tests that if one instance fails while holding
// a lock, other instances can still acquire it after TTL expires.
func TestDistributedLockFailover(t *testing.T) {
	t.Parallel()
	skipIfRedisNotAvailable(t)

	ctx := newContext()

	// Create Redis locks with short TTL for faster test
	// Default to localhost:6379 for local development
	redisAddrs := []string{"localhost:6379"}
	// Use environment variable if set (for CI with dynamic ports)
	if envAddrs := os.Getenv("NCPS_TEST_REDIS_ADDRS"); envAddrs != "" {
		redisAddrs = []string{envAddrs}
	}

	redisCfg := redis.Config{
		Addrs:     redisAddrs,
		KeyPrefix: "ncps:test:failover:",
	}

	retryCfg := lock.RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Jitter:       true,
	}

	// Create first locker and acquire lock
	locker1, err := redis.NewLocker(ctx, redisCfg, retryCfg, false)
	require.NoError(t, err)

	testKey := "test-failover-key"
	shortTTL := 2 * time.Second

	// Locker 1 acquires the lock
	err = locker1.Lock(ctx, testKey, shortTTL)
	require.NoError(t, err)

	// Create second locker
	locker2, err := redis.NewLocker(ctx, redisCfg, retryCfg, false)
	require.NoError(t, err)

	// Locker 2 should initially fail to acquire (lock held by locker1)
	ctx2, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	err = locker2.Lock(ctx2, testKey, shortTTL)
	require.Error(t, err, "locker2 should fail to acquire lock held by locker1")

	// Simulate locker1 failure (don't unlock, let it expire)
	// Wait for TTL to expire
	time.Sleep(shortTTL + 500*time.Millisecond)

	// Now locker2 should be able to acquire the lock
	err = locker2.Lock(ctx, testKey, shortTTL)
	require.NoError(t, err, "locker2 should acquire lock after TTL expiry")

	// Clean up
	err = locker2.Unlock(ctx, testKey)
	assert.NoError(t, err)
}
