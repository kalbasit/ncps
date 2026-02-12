package cache_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	"github.com/kalbasit/ncps/pkg/storage/chunk"
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

// pollWithBackoff polls a condition with linear backoff until it succeeds or times out.
// It starts with a 1ms delay and increases linearly (1ms, 2ms, 3ms, ...).
// This replaces arbitrary time.Sleep calls with structured polling that respects timeouts.
func pollWithBackoff(t *testing.T, maxIterations int, condition func() bool) bool {
	t.Helper()

	for i := 1; i < maxIterations; i++ {
		time.Sleep(time.Duration(i) * time.Millisecond)

		if condition() {
			return true
		}
	}

	return false
}

// distributedDBFactory creates a shared database for distributed testing.
// Unlike other factories, this returns a SHARED database that multiple cache instances will use.
type distributedDBFactory func(t *testing.T) (database.Querier, string, func())

// setupDistributedSQLite creates a shared SQLite database for distributed testing.
func setupDistributedSQLite(t *testing.T) (database.Querier, string, func()) {
	t.Helper()

	sharedDir, err := os.MkdirTemp("", "cache-distributed-")
	require.NoError(t, err)

	dbFile := filepath.Join(sharedDir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	cleanup := func() {
		db.DB().Close()
		os.RemoveAll(sharedDir)
	}

	return db, sharedDir, cleanup
}

// setupDistributedPostgres creates a shared PostgreSQL database for distributed testing.
func setupDistributedPostgres(t *testing.T) (database.Querier, string, func()) {
	t.Helper()

	sharedDir, err := os.MkdirTemp("", "cache-distributed-")
	require.NoError(t, err)

	db, _, dbCleanup := testhelper.SetupPostgres(t)

	cleanup := func() {
		dbCleanup()
		os.RemoveAll(sharedDir)
	}

	return db, sharedDir, cleanup
}

// setupDistributedMySQL creates a shared MySQL database for distributed testing.
func setupDistributedMySQL(t *testing.T) (database.Querier, string, func()) {
	t.Helper()

	sharedDir, err := os.MkdirTemp("", "cache-distributed-")
	require.NoError(t, err)

	db, _, dbCleanup := testhelper.SetupMySQL(t)

	cleanup := func() {
		dbCleanup()
		os.RemoveAll(sharedDir)
	}

	return db, sharedDir, cleanup
}

// TestDistributedBackends runs the distributed test suite across multiple database backends.
func TestDistributedBackends(t *testing.T) {
	t.Parallel()
	skipIfRedisNotAvailable(t)

	backends := []struct {
		name   string
		envVar string
		setup  distributedDBFactory
	}{
		{
			name:  "SQLite",
			setup: setupDistributedSQLite,
		},
		{
			name:   "PostgreSQL",
			envVar: "NCPS_TEST_ADMIN_POSTGRES_URL",
			setup:  setupDistributedPostgres,
		},
		{
			name:   "MySQL",
			envVar: "NCPS_TEST_ADMIN_MYSQL_URL",
			setup:  setupDistributedMySQL,
		},
	}

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			if b.envVar != "" && os.Getenv(b.envVar) == "" {
				t.Skipf("Skipping %s: %s not set", b.name, b.envVar)
			}

			runDistributedTestSuite(t, b.setup)
		})
	}
}

func runDistributedTestSuite(t *testing.T, factory distributedDBFactory) {
	t.Helper()

	t.Run("DownloadDeduplication", testDistributedDownloadDeduplication(factory))
	t.Run("ConcurrentReads", testDistributedConcurrentReads(factory))
	t.Run("LockFailover", testDistributedLockFailover(factory))
	t.Run("PutNarInfoConcurrentSharedNar", testPutNarInfoConcurrentSharedNar(factory))
	t.Run("LargeNARConcurrentDownload", testDistributedLargeNARConcurrentDownload(factory))
	t.Run("CDCProgressiveStreamingDuringChunking", testCDCProgressiveStreamingDuringChunking(factory))
}

// testDistributedDownloadDeduplication verifies that when multiple cache instances
// request the same package, only one instance downloads it from the upstream source.
func testDistributedDownloadDeduplication(factory distributedDBFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := newContext()

		// Get shared database and directory from factory
		db, sharedDir, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Start test server
		ts := testdata.NewTestServer(t, 40)
		t.Cleanup(ts.Close)

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

		t.Cleanup(func() { ts.RemoveMaybeHandler(handlerID) })

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
			uc, err := upstream.New(ctx, testhelper.MustParseURL(t, ts.URL), nil)
			// Don't use public keys for distributed tests with generated entries
			// since they don't have valid signatures (we don't have the private key to sign them)
			require.NoError(t, err)

			c, err := cache.New(
				ctx,
				cacheName,
				db,
				sharedStore,
				sharedStore,
				sharedStore,
				"",
				downloadLocker,
				cacheLocker,
				5*time.Minute,
				30*time.Second,
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
}

// testDistributedConcurrentReads verifies that multiple instances can
// concurrently read the same cached files.
func testDistributedConcurrentReads(factory distributedDBFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := newContext()

		// Get shared database and directory from factory
		db, sharedDir, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Start test server
		ts := testdata.NewTestServer(t, 40)
		t.Cleanup(ts.Close)

		// Create upstream cache
		uc, err := upstream.New(ctx, testhelper.MustParseURL(t, ts.URL), nil)
		// Don't use public keys for distributed tests with generated entries
		// since they don't have valid signatures (we don't have the private key to sign them)
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
			"",
			downloadLocker,
			cacheLocker,
			5*time.Minute,
			30*time.Second, // downloadPollTimeout
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

		// Wait for the NAR to be fully written to the local store
		// by polling until it exists in the storage backend
		pollWithBackoff(t, 100, func() bool {
			return sharedStore.HasNar(ctx, narURL)
		})

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
				"",
				downloadLocker,
				cacheLocker,
				5*time.Minute,
				30*time.Second, // downloadPollTimeout
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
}

// testDistributedLockFailover tests that if one instance fails while holding
// a lock, other instances can still acquire it after TTL expires.
func testDistributedLockFailover(_ distributedDBFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

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
			KeyPrefix: fmt.Sprintf("ncps:test:failover:%s:", t.Name()),
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
		time.Sleep(shortTTL + 2*time.Second)

		// Now locker2 should be able to acquire the lock
		err = locker2.Lock(ctx, testKey, shortTTL)
		require.NoError(t, err, "locker2 should acquire lock after TTL expiry")

		// Clean up
		err = locker2.Unlock(ctx, testKey)
		assert.NoError(t, err)
	}
}

// testPutNarInfoConcurrentSharedNar tests concurrent writes of two different narinfos
// that reference the same NAR file, to ensure proper handling of shared NAR files.
func testPutNarInfoConcurrentSharedNar(factory distributedDBFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		// We run this loop to increase chance of hitting the race condition.
		for run := 0; run < 50; run++ {
			func() {
				ctx := newContext()

				// Get shared database and directory from factory
				db, sharedDir, cleanup := factory(t)
				defer cleanup()

				// Redis setup
				redisAddrs := []string{"localhost:6379"}
				if envAddrs := os.Getenv("NCPS_TEST_REDIS_ADDRS"); envAddrs != "" {
					redisAddrs = []string{envAddrs}
				}

				// Use a unique prefix per run to ensure isolation
				keyPrefix := fmt.Sprintf("ncps:test:race:%d:%d:", run, time.Now().UnixNano())

				redisCfg := redis.Config{
					Addrs:     redisAddrs,
					KeyPrefix: keyPrefix,
				}
				retryCfg := lock.RetryConfig{MaxAttempts: 3, InitialDelay: 10 * time.Millisecond, MaxDelay: 100 * time.Millisecond}

				downloadLocker, err := redis.NewLocker(ctx, redisCfg, retryCfg, false)
				require.NoError(t, err)

				cacheLocker := locklocal.NewRWLocker() // Local cache lock is fine for single-process test

				// Create shared storage
				sharedStore, err := local.New(ctx, sharedDir)
				require.NoError(t, err)

				c, err := cache.New(
					ctx,
					cacheName,
					db,
					sharedStore,
					sharedStore,
					sharedStore,
					"",
					downloadLocker,
					cacheLocker,
					5*time.Minute,
					30*time.Second, // downloadPollTimeout
					30*time.Minute,
				)
				require.NoError(t, err)

				// Define Data
				narInfo1Hash := testdata.Nar1.NarInfoHash
				narInfo1Text := testdata.Nar1.NarInfoText
				narInfo2Hash := "different1234567890abcdefghijklmno"
				narInfo2Text := `StorePath: /nix/store/different1234567890abcdefghijklmno-hello-2.12.1
URL: nar/1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri.nar.xz
Compression: xz
FileHash: sha256:1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri
FileSize: 50160
NarHash: sha256:07kc6swib31psygpmwi8952lvywlpqn474059yxl7grwsvr6k0fj
NarSize: 226552
References: different1234567890abcdefghijklmno-hello-2.12.1 qdcbgcj27x2kpxj2sf9yfvva7qsgg64g-glibc-2.38-77
Deriver: 9zpqmcicrg8smi9jlqv6dmd7v20d2fsn-hello-2.12.1.drv
Sig: cache.nixos.org-1:MadTCU1OSFCGUw4aqCKpLCZJpqBc7AbLvO7wgdlls0eq1DwaSnF/82SZE+wJGEiwlHbnZR+14daSaec0W3XoBQ==`

				var wg sync.WaitGroup
				wg.Add(2)

				startCh := make(chan struct{})
				errCh := make(chan error, 2)

				go func() {
					defer wg.Done()

					<-startCh

					errCh <- c.PutNarInfo(ctx, narInfo1Hash, io.NopCloser(strings.NewReader(narInfo1Text)))
				}()

				go func() {
					defer wg.Done()

					<-startCh

					errCh <- c.PutNarInfo(ctx, narInfo2Hash, io.NopCloser(strings.NewReader(narInfo2Text)))
				}()

				close(startCh)
				wg.Wait()
				close(errCh)

				var errs []error

				for err := range errCh {
					if err != nil {
						errs = append(errs, err)
					}
				}

				if len(errs) > 0 {
					for _, err := range errs {
						if strings.Contains(err.Error(), "duplicate key value violates unique constraint") ||
							strings.Contains(err.Error(), "Duplicate entry") { // MySQL error
							t.Logf("Hit race condition on run %d: %v", run, err)
							t.Fail()

							return
						}

						require.NoError(t, err)
					}
				}
			}()
		}
	}
}

// testDistributedLargeNARConcurrentDownload tests that when multiple cache instances
// request the same large NAR concurrently (while one is downloading), all instances
// successfully serve the NAR without errors. This replicates the issue where servers
// fail with "failed to acquire download lock" when making concurrent requests.
//
// Tests both CDC enabled and disabled scenarios.
func testDistributedLargeNARConcurrentDownload(factory distributedDBFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		// Test with CDC disabled
		t.Run("CDC_Disabled", func(t *testing.T) {
			t.Parallel()
			testLargeNARConcurrentDownloadScenario(t, factory, false)
		})

		// Test with CDC enabled
		// TODO: CDC test currently fails with partial data reconstruction (instances get ~100-200KB instead of 12MB)
		// This appears to be a separate CDC-specific issue with concurrent chunk reconstruction.
		// Investigation needed: check if HasNarInChunks returns true before all chunks are fully stored,
		// or if there's a race condition in concurrent chunk reading.
		t.Run("CDC_Enabled", func(t *testing.T) {
			t.Parallel()
			testLargeNARConcurrentDownloadScenario(t, factory, true)
		})
	}
}

func testLargeNARConcurrentDownloadScenario(t *testing.T, factory distributedDBFactory, cdcEnabled bool) {
	t.Helper()

	ctx := newContext()

	// Get shared database and directory from factory
	db, sharedDir, cleanup := factory(t)
	t.Cleanup(cleanup)

	// Generate a large NAR (12MB) to ensure CDC chunking can happen
	const largeNARSize = 12 * 1024 * 1024

	narData := make([]byte, largeNARSize)
	_, err := rand.Read(narData)
	require.NoError(t, err)

	// Start test server
	ts := testdata.NewTestServer(t, 40)
	t.Cleanup(ts.Close)

	// Generate a custom entry for the large NAR
	largeNarEntry, err := testdata.GenerateEntry(t, narData)
	require.NoError(t, err)

	// Add the entry to the server
	ts.AddEntry(largeNarEntry)

	// Add handler to simulate slow downloads (ensures concurrent requests arrive during download)
	narPath := "/nar/" + largeNarEntry.NarHash + ".nar"
	if largeNarEntry.NarCompression != nar.CompressionTypeNone {
		narPath += "." + largeNarEntry.NarCompression.ToFileExtension()
	}

	handlerID := ts.AddMaybeHandler(func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == narPath && r.Method == http.MethodGet {
			// Add minimal delay to ensure concurrent requests arrive while download is in progress
			// Reduced from 2s to 50ms - still exercises concurrency without slow tests
			time.Sleep(50 * time.Millisecond)
		}

		return false // Let default handler process request
	})

	t.Cleanup(func() { ts.RemoveMaybeHandler(handlerID) })

	// Create shared storage
	sharedStore, err := local.New(ctx, sharedDir)
	require.NoError(t, err)

	// Setup optional CDC chunk store if enabled
	var chunkStore chunk.Store

	if cdcEnabled {
		chunksDir := filepath.Join(sharedDir, "chunks")
		require.NoError(t, os.MkdirAll(chunksDir, 0o755))

		chunkStore, err = chunk.NewLocalStore(chunksDir)
		require.NoError(t, err)
	}

	// Create Redis locks with unique prefix for this test
	redisAddrs := []string{"localhost:6379"}
	if envAddrs := os.Getenv("NCPS_TEST_REDIS_ADDRS"); envAddrs != "" {
		redisAddrs = []string{envAddrs}
	}

	testPrefix := fmt.Sprintf("ncps:test:large-nar:%s:", t.Name())
	redisCfg := redis.Config{
		Addrs:     redisAddrs,
		KeyPrefix: testPrefix,
	}

	// Use default retry config (3 attempts, 100ms initial, 2s max)
	retryCfg := lock.RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     2 * time.Second,
		Jitter:       true,
	}

	// Create multiple cache instances
	var (
		caches         []*cache.Cache
		downloadErrors []error
		mu             sync.Mutex
	)

	for i := 0; i < numInstances; i++ {
		downloadLocker, err := redis.NewLocker(ctx, redisCfg, retryCfg, false)
		require.NoError(t, err)

		cacheLocker, err := redis.NewRWLocker(ctx, redisCfg, retryCfg, false)
		require.NoError(t, err)

		uc, err := upstream.New(ctx, testhelper.MustParseURL(t, ts.URL), nil)
		// Don't use public keys for distributed tests with generated entries
		// since they don't have valid signatures (we don't have the private key to sign them)
		require.NoError(t, err)

		c, err := cache.New(
			ctx,
			fmt.Sprintf("cache-instance-%d", i),
			db,
			sharedStore,
			sharedStore,
			sharedStore,
			"",
			downloadLocker,
			cacheLocker,
			5*time.Minute,
			60*time.Second, // downloadPollTimeout
			30*time.Minute,
		)
		require.NoError(t, err)

		if cdcEnabled {
			require.NotNil(t, chunkStore, "chunk store must be configured for CDC")
			c.SetChunkStore(chunkStore)
			// Use smaller chunk sizes for testing
			err := c.SetCDCConfiguration(true, 1024, 4096, 8192)
			require.NoError(t, err)
		}

		c.AddUpstreamCaches(ctx, uc)
		c.SetRecordAgeIgnoreTouch(0)

		// Wait for upstream to become healthy
		<-c.GetHealthChecker().Trigger()

		caches = append(caches, c)
	}

	// Track successful GetNar operations
	var (
		successfulGets atomic.Int32
		failedGets     atomic.Int32
	)

	// Simulate concurrent requests from all instances for the same large NAR
	// This replicates the user's test scenario with ./tmp/ttfb.py
	var wg sync.WaitGroup

	for i, c := range caches {
		wg.Add(1)

		go func(instanceNum int, cacheInstance *cache.Cache) {
			defer wg.Done()

			// Create a context with timeout for each request (simulates HTTP request timeout)
			// This should be longer than the download delay (2s) plus chunking time
			// For CDC-enabled tests, progressive streaming may take longer
			requestCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
			defer cancel()

			// All instances request the same NAR concurrently
			narURL := nar.URL{
				Hash:        largeNarEntry.NarHash,
				Compression: largeNarEntry.NarCompression,
			}

			_, reader, err := cacheInstance.GetNar(requestCtx, narURL)
			if err != nil {
				mu.Lock()

				downloadErrors = append(downloadErrors, fmt.Errorf("instance %d: %w", instanceNum, err))

				mu.Unlock()

				failedGets.Add(1)

				t.Logf("Instance %d failed to get NAR: %v", instanceNum, err)

				return
			}

			// Read the entire NAR to verify it's complete
			data, err := io.ReadAll(reader)
			if err != nil {
				mu.Lock()

				downloadErrors = append(downloadErrors, fmt.Errorf("instance %d read error: %w", instanceNum, err))

				mu.Unlock()

				failedGets.Add(1)

				return
			}

			// Verify we got the complete data
			if len(data) != largeNARSize {
				mu.Lock()

				downloadErrors = append(downloadErrors,
					fmt.Errorf("instance %d: size mismatch: expected %d bytes, got %d: %w",
						instanceNum, largeNARSize, len(data), io.ErrUnexpectedEOF))

				mu.Unlock()

				failedGets.Add(1)

				return
			}

			successfulGets.Add(1)
			t.Logf("Instance %d successfully retrieved NAR (%d bytes)", instanceNum, len(data))
		}(i, c)
	}

	wg.Wait()

	// Report any errors
	if len(downloadErrors) > 0 {
		t.Logf("Download errors encountered:")

		for _, err := range downloadErrors {
			t.Logf("  - %v", err)
		}
	}

	// CRITICAL ASSERTION: All instances should successfully retrieve the NAR
	// This will FAIL without the fix because instances 2 and 3 get "failed to acquire lock" errors
	successful := successfulGets.Load()
	failed := failedGets.Load()

	assert.Equal(t, int32(numInstances), successful,
		"all %d instances should successfully retrieve the NAR (got %d successes, %d failures)",
		numInstances, successful, failed)

	assert.Equal(t, int32(0), failed,
		"no instances should fail (got %d failures)", failed)

	// Success! All instances retrieved the NAR, even though only one downloaded from upstream.
	// The coordination fix allows instances that fail to acquire the download lock to poll
	// storage and serve the NAR once another instance completes the download.
}

// testCDCProgressiveStreamingDuringChunking verifies that instances can start progressive
// streaming while chunking is in progress, rather than waiting for chunking to complete.
// This tests the fix where HasNarFileRecord allows polling to complete early so instances
// can enter progressive streaming mode.
func testCDCProgressiveStreamingDuringChunking(factory distributedDBFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := newContext()

		// Get shared database and directory from factory
		db, sharedDir, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Generate a large NAR (12MB) to ensure CDC chunking takes time
		// Using testdata.Entries[0] (50KB) is too small and causes flaky tests
		const largeNARSize = 12 * 1024 * 1024

		narData := make([]byte, largeNARSize)
		_, err := rand.Read(narData)
		require.NoError(t, err)

		// Start test server
		ts := testdata.NewTestServer(t, 40)
		t.Cleanup(ts.Close)

		// Generate a custom entry for the large NAR
		largeNarEntry, err := testdata.GenerateEntry(t, narData)
		require.NoError(t, err)

		// Add the entry to the server
		ts.AddEntry(largeNarEntry)

		narURL := nar.URL{
			Hash:        largeNarEntry.NarHash,
			Compression: largeNarEntry.NarCompression,
		}

		// Build the NAR path based on compression
		narPath := "/nar/" + largeNarEntry.NarHash + ".nar"
		if largeNarEntry.NarCompression != nar.CompressionTypeNone {
			narPath += "." + largeNarEntry.NarCompression.ToFileExtension()
		}

		// Add handler with 2-second delay to simulate slow download
		// This ensures concurrent requests arrive during chunking
		handlerID := ts.AddMaybeHandler(func(_ http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == narPath && r.Method == http.MethodGet {
				// Minimal delay to test concurrent chunking (reduced from 2s to 50ms)
				time.Sleep(50 * time.Millisecond)
			}

			return false // Let default handler process request
		})

		t.Cleanup(func() { ts.RemoveMaybeHandler(handlerID) })

		// Create shared storage
		sharedStore, err := local.New(ctx, sharedDir)
		require.NoError(t, err)

		// Setup CDC chunk store
		chunksDir := filepath.Join(sharedDir, "chunks")
		require.NoError(t, os.MkdirAll(chunksDir, 0o755))

		chunkStore, err := chunk.NewLocalStore(chunksDir)
		require.NoError(t, err)

		// Create Redis locks with unique prefix for this test
		redisAddrs := []string{"localhost:6379"}
		if envAddrs := os.Getenv("NCPS_TEST_REDIS_ADDRS"); envAddrs != "" {
			redisAddrs = []string{envAddrs}
		}

		redisCfg := redis.Config{
			Addrs:     redisAddrs,
			KeyPrefix: "ncps:test:progressive:",
		}

		retryCfg := lock.RetryConfig{
			MaxAttempts:  3,
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     2 * time.Second,
			Jitter:       true,
		}

		// Create multiple cache instances with CDC enabled
		var caches []*cache.Cache

		for i := 0; i < numInstances; i++ {
			downloadLocker, err := redis.NewLocker(ctx, redisCfg, retryCfg, false)
			require.NoError(t, err)

			cacheLocker, err := redis.NewRWLocker(ctx, redisCfg, retryCfg, false)
			require.NoError(t, err)

			// Create separate upstream cache for each instance to avoid data races
			uc, err := upstream.New(ctx, testhelper.MustParseURL(t, ts.URL), nil)
			// Don't use public keys for distributed tests with generated entries
			// since they don't have valid signatures (we don't have the private key to sign them)
			require.NoError(t, err)

			c, err := cache.New(
				ctx,
				cacheName,
				db,
				sharedStore,
				sharedStore,
				sharedStore,
				"",
				downloadLocker,
				cacheLocker,
				5*time.Minute,
				60*time.Second, // downloadPollTimeout
				30*time.Minute,
			)
			require.NoError(t, err)

			// Enable CDC with small chunk sizes for testing
			c.SetChunkStore(chunkStore)
			err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
			require.NoError(t, err)

			c.AddUpstreamCaches(ctx, uc)

			// Wait for this instance's upstream to become healthy
			<-c.GetHealthChecker().Trigger()

			caches = append(caches, c)
		}

		// Launch concurrent GetNar requests and measure TTFB
		var wg sync.WaitGroup

		type result struct {
			instanceNum int
			ttfb        time.Duration
			totalTime   time.Duration
			size        int64
			err         error
		}

		results := make(chan result, numInstances)

		for i, c := range caches {
			wg.Add(1)

			go func(instanceNum int, cache *cache.Cache) {
				defer wg.Done()

				// Create a context with timeout (give enough time for CDC chunking)
				requestCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
				defer cancel()

				// Measure TTFB (time to first byte)
				ttfbStart := time.Now()
				size, rc, err := cache.GetNar(requestCtx, narURL)
				ttfb := time.Since(ttfbStart)

				if err != nil {
					results <- result{instanceNum: instanceNum, err: err}

					return
				}

				defer rc.Close()

				// Read data to measure total time
				data, err := io.ReadAll(rc)
				totalTime := time.Since(ttfbStart)

				if err != nil {
					results <- result{instanceNum: instanceNum, ttfb: ttfb, err: err}

					return
				}

				results <- result{
					instanceNum: instanceNum,
					ttfb:        ttfb,
					totalTime:   totalTime,
					size:        size,
					err:         nil,
				}

				t.Logf("Instance %d: TTFB=%s, TotalTime=%s, Size=%d bytes",
					instanceNum, ttfb, totalTime, len(data))
			}(i, c)
		}

		wg.Wait()
		close(results)

		// Verify results
		var (
			successCount, failCount int
			ttfbs                   []time.Duration
		)

		for res := range results {
			if res.err != nil {
				failCount++

				t.Errorf("Instance %d failed: %v", res.instanceNum, res.err)

				continue
			}

			successCount++

			ttfbs = append(ttfbs, res.ttfb)

			// Size may be -1 during progressive streaming (unknown until complete)
			// Just verify that we actually read data
			// The io.ReadAll succeeded, so we know data was received
		}

		// All instances should succeed
		assert.Equal(t, numInstances, successCount,
			"all instances should succeed")
		assert.Equal(t, 0, failCount,
			"no instances should fail")

		// CRITICAL: TTFB should be close to the upstream download time (2-4s)
		// Without the fix, waiting instances would wait for download + full chunking time (6+ seconds)
		// With the fix, they start progressive streaming during chunking (TTFB ~= download time)
		for i, ttfb := range ttfbs {
			assert.Less(t, ttfb, 5*time.Second,
				"Instance %d TTFB should be close to upstream download time (got %s), "+
					"indicating progressive streaming is working (not waiting for chunking to complete)", i, ttfb)
		}

		t.Logf("SUCCESS: All instances started streaming with TTFB close to download time (progressive streaming working)")
	}
}
