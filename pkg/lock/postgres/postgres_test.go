package postgres_test

import (
	"context"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/postgres"
	"github.com/kalbasit/ncps/testhelper"
)

// skipIfPostgresNotAvailable skips the test if PostgreSQL is not available for testing.
func skipIfPostgresNotAvailable(t *testing.T) {
	t.Helper()

	if os.Getenv("NCPS_TEST_ADMIN_POSTGRES_URL") == "" {
		t.Skip("PostgreSQL tests disabled (set NCPS_TEST_ADMIN_POSTGRES_URL to enable)")
	}
}

// getTestDatabase creates an ephemeral test database and returns a querier.
func getTestDatabase(t *testing.T) (database.Querier, func()) {
	t.Helper()

	adminDbURL := os.Getenv("NCPS_TEST_ADMIN_POSTGRES_URL")
	require.NotEmpty(t, adminDbURL, "NCPS_TEST_ADMIN_POSTGRES_URL must be set")

	// Connect to admin database
	adminDb, err := database.Open(adminDbURL, nil)
	require.NoError(t, err, "failed to connect to admin database")

	// Create ephemeral test database
	dbName := "test-lock-" + testhelper.MustRandString(40)

	_, err = adminDb.DB().ExecContext(context.Background(), "SELECT create_test_db($1)", dbName)
	require.NoError(t, err, "failed to create test database")

	// Connect to test database
	testDbURL := replaceDatabaseName(t, adminDbURL, dbName)
	testDb, err := database.Open(testDbURL, nil)
	require.NoError(t, err, "failed to connect to test database")

	cleanup := func() {
		if err := testDb.DB().Close(); err != nil {
			t.Logf("error closing test database connection: %s", err)
		}

		_, err := adminDb.DB().ExecContext(context.Background(), "SELECT drop_test_db($1)", dbName)
		if err != nil {
			t.Logf("error dropping test database: %s", err)
		}

		if err := adminDb.DB().Close(); err != nil {
			t.Logf("error closing admin database connection: %s", err)
		}
	}

	return testDb, cleanup
}

// replaceDatabaseName replaces the database name in a PostgreSQL URL.
func replaceDatabaseName(t *testing.T, dbURL, newName string) string {
	t.Helper()

	// Parse the URL
	parsed, err := url.Parse(dbURL)
	require.NoError(t, err, "failed to parse database URL")

	// Replace the path (database name)
	parsed.Path = "/" + newName

	return parsed.String()
}

// getTestConfig returns a PostgreSQL advisory lock configuration for testing.
func getTestConfig() postgres.Config {
	return postgres.Config{
		KeyPrefix: "test:ncps:lock:",
	}
}

// getTestRetryConfig returns a retry configuration for testing.
func getTestRetryConfig() lock.RetryConfig {
	return lock.RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 50 * time.Millisecond,
		MaxDelay:     500 * time.Millisecond,
		Jitter:       true,
	}
}

// getUniqueKey generates a unique test key to avoid conflicts in parallel tests.
func getUniqueKey(t *testing.T, prefix string) string {
	t.Helper()

	return prefix + "-" + t.Name() + "-" + time.Now().Format("20060102-150405.000000")
}

func TestLocker_BasicLockUnlock(t *testing.T) {
	t.Parallel()
	skipIfPostgresNotAvailable(t)

	ctx := context.Background()

	db, cleanup := getTestDatabase(t)
	t.Cleanup(cleanup)

	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	locker, err := postgres.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	key := getUniqueKey(t, "basic-lock")

	// Acquire lock
	err = locker.Lock(ctx, key, 10*time.Second)
	require.NoError(t, err)

	// Release lock
	err = locker.Unlock(ctx, key)
	require.NoError(t, err)
}

func TestLocker_ConcurrentLockContention(t *testing.T) {
	t.Parallel()
	skipIfPostgresNotAvailable(t)

	ctx := context.Background()

	db, cleanup := getTestDatabase(t)
	t.Cleanup(cleanup)

	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	// Create two separate locker instances using same database
	locker1, err := postgres.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	locker2, err := postgres.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	key := getUniqueKey(t, "contention")

	// First locker acquires lock
	err = locker1.Lock(ctx, key, 2*time.Second)
	require.NoError(t, err)

	// Second locker should fail to acquire (will retry and timeout)
	ctx2, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	err = locker2.Lock(ctx2, key, 2*time.Second)
	require.Error(t, err, "second locker should not acquire lock while first holds it")

	// Release first lock
	err = locker1.Unlock(ctx, key)
	require.NoError(t, err)

	// Now second locker can acquire
	err = locker2.Lock(ctx, key, 2*time.Second)
	require.NoError(t, err)

	err = locker2.Unlock(ctx, key)
	require.NoError(t, err)
}

func TestLocker_TryLock(t *testing.T) {
	t.Parallel()
	skipIfPostgresNotAvailable(t)

	ctx := context.Background()

	db, cleanup := getTestDatabase(t)
	t.Cleanup(cleanup)

	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	locker1, err := postgres.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	locker2, err := postgres.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	key := getUniqueKey(t, "trylock")

	// First locker tries to acquire
	acquired, err := locker1.TryLock(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired, "first TryLock should succeed")

	// Second locker tries to acquire (should fail)
	acquired2, err := locker2.TryLock(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.False(t, acquired2, "second TryLock should fail while first holds lock")

	// Release first lock
	err = locker1.Unlock(ctx, key)
	require.NoError(t, err)

	// Third TryLock should succeed
	acquired3, err := locker2.TryLock(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired3, "TryLock should succeed after lock released")

	// Cleanup
	err = locker2.Unlock(ctx, key)
	require.NoError(t, err)
}

func TestLocker_RetryWithBackoff(t *testing.T) {
	t.Parallel()
	skipIfPostgresNotAvailable(t)

	ctx := context.Background()

	db, cleanup := getTestDatabase(t)
	t.Cleanup(cleanup)

	cfg := getTestConfig()
	retryCfg := lock.RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Jitter:       true,
	}

	locker1, err := postgres.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	locker2, err := postgres.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	key := getUniqueKey(t, "retry")

	// First locker acquires lock
	err = locker1.Lock(ctx, key, 3*time.Second)
	require.NoError(t, err)

	// Start goroutine to release lock after 1 second
	go func() {
		time.Sleep(1 * time.Second)

		_ = locker1.Unlock(ctx, key)
	}()

	// Second locker should retry and eventually acquire
	start := time.Now()
	err = locker2.Lock(ctx, key, 5*time.Second)
	duration := time.Since(start)

	require.NoError(t, err, "should eventually acquire lock after retries")
	assert.Greater(t, duration, 900*time.Millisecond, "should have waited for lock to be released")
	assert.Less(t, duration, 3*time.Second, "should not take too long with retries")

	err = locker2.Unlock(ctx, key)
	require.NoError(t, err)
}

func TestLocker_KeyHashing(t *testing.T) {
	t.Parallel()
	skipIfPostgresNotAvailable(t)

	ctx := context.Background()

	db, cleanup := getTestDatabase(t)
	t.Cleanup(cleanup)

	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	locker, err := postgres.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	// Test that different keys get different locks
	key1 := getUniqueKey(t, "hash1")
	key2 := getUniqueKey(t, "hash2")

	err = locker.Lock(ctx, key1, 5*time.Second)
	require.NoError(t, err)

	// Should be able to acquire lock on different key
	err = locker.Lock(ctx, key2, 5*time.Second)
	require.NoError(t, err)

	err = locker.Unlock(ctx, key1)
	require.NoError(t, err)

	err = locker.Unlock(ctx, key2)
	require.NoError(t, err)
}

func TestRWLocker_BasicReadWriteLock(t *testing.T) {
	t.Parallel()
	skipIfPostgresNotAvailable(t)

	ctx := context.Background()

	db, cleanup := getTestDatabase(t)
	t.Cleanup(cleanup)

	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	locker, err := postgres.NewRWLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	key := getUniqueKey(t, "rw-basic")

	// Acquire read lock
	err = locker.RLock(ctx, key, 10*time.Second)
	require.NoError(t, err)

	// Release read lock
	err = locker.RUnlock(ctx, key)
	require.NoError(t, err)

	// Acquire write lock
	err = locker.Lock(ctx, key, 10*time.Second)
	require.NoError(t, err)

	// Release write lock
	err = locker.Unlock(ctx, key)
	require.NoError(t, err)
}

func TestRWLocker_MultipleReaders(t *testing.T) {
	t.Parallel()
	skipIfPostgresNotAvailable(t)

	ctx := context.Background()

	db, cleanup := getTestDatabase(t)
	t.Cleanup(cleanup)

	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	// Create multiple locker instances
	lockers := make([]lock.RWLocker, 0, 5)

	for range 5 {
		locker, err := postgres.NewRWLocker(ctx, db, cfg, retryCfg, false)
		require.NoError(t, err)

		lockers = append(lockers, locker)
	}

	key := getUniqueKey(t, "rw-readers")

	var (
		wg            sync.WaitGroup
		barrier       sync.WaitGroup
		readersActive int64
	)

	// Use barrier to ensure all readers acquire locks before checking
	barrier.Add(len(lockers))

	// All readers should be able to acquire simultaneously
	for i, locker := range lockers {
		wg.Add(1)

		go func(_ int, l lock.RWLocker) {
			defer wg.Done()

			err := l.RLock(ctx, key, 10*time.Second)
			assert.NoError(t, err)

			atomic.AddInt64(&readersActive, 1)

			// Wait for all readers to acquire their locks
			barrier.Done()
			barrier.Wait()

			// Now all readers should be active
			active := atomic.LoadInt64(&readersActive)
			assert.GreaterOrEqual(t, active, int64(len(lockers)), "all readers should be active simultaneously")

			// Hold the lock briefly to ensure readers can coexist
			time.Sleep(50 * time.Millisecond)

			atomic.AddInt64(&readersActive, -1)

			err = l.RUnlock(ctx, key)
			assert.NoError(t, err)
		}(i, locker)
	}

	wg.Wait()
}

func TestRWLocker_WriterBlocksReaders(t *testing.T) {
	t.Parallel()
	skipIfPostgresNotAvailable(t)

	ctx := context.Background()

	db, cleanup := getTestDatabase(t)
	t.Cleanup(cleanup)

	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	locker1, err := postgres.NewRWLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	locker2, err := postgres.NewRWLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	key := getUniqueKey(t, "rw-writer-blocks")

	// Acquire write lock
	err = locker1.Lock(ctx, key, 5*time.Second)
	require.NoError(t, err)

	// Try to acquire read lock (should block/timeout)
	ctx2, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	err = locker2.RLock(ctx2, key, 5*time.Second)
	require.Error(t, err, "read lock should be blocked by write lock")

	// Release write lock
	err = locker1.Unlock(ctx, key)
	require.NoError(t, err)

	// Now read lock should succeed
	err = locker2.RLock(ctx, key, 5*time.Second)
	require.NoError(t, err)

	err = locker2.RUnlock(ctx, key)
	require.NoError(t, err)
}

func TestRWLocker_TryLockWithReaders(t *testing.T) {
	t.Parallel()
	skipIfPostgresNotAvailable(t)

	ctx := context.Background()

	db, cleanup := getTestDatabase(t)
	t.Cleanup(cleanup)

	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	locker1, err := postgres.NewRWLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	locker2, err := postgres.NewRWLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	key := getUniqueKey(t, "rw-trylock-readers")

	// Acquire read lock
	err = locker1.RLock(ctx, key, 5*time.Second)
	require.NoError(t, err)

	// TryLock should fail (readers present)
	acquired, err := locker2.TryLock(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.False(t, acquired, "write TryLock should fail when readers present")

	// Release read lock
	err = locker1.RUnlock(ctx, key)
	require.NoError(t, err)

	// TryLock should now succeed
	acquired2, err := locker2.TryLock(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired2, "write TryLock should succeed after readers release")

	// Cleanup
	err = locker2.Unlock(ctx, key)
	require.NoError(t, err)
}
