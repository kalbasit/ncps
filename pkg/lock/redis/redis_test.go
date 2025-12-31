package redis_test

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/lock/redis"
)

// skipIfRedisNotAvailable skips the test if Redis is not available for testing.
func skipIfRedisNotAvailable(t *testing.T) {
	t.Helper()

	if os.Getenv("NCPS_ENABLE_REDIS_TESTS") != "1" {
		t.Skip("Redis tests disabled (set NCPS_ENABLE_REDIS_TESTS=1 to enable)")
	}
}

// getTestConfig returns a Redis configuration for testing.
func getTestConfig() redis.Config {
	// Default to localhost:6379 for local development
	addrs := []string{"localhost:6379"}

	// Use environment variable if set (for CI with dynamic ports)
	if envAddrs := os.Getenv("NCPS_TEST_REDIS_ADDRS"); envAddrs != "" {
		addrs = []string{envAddrs}
	}

	return redis.Config{
		Addrs:     addrs,
		Username:  "",
		Password:  "",
		DB:        0,
		UseTLS:    false,
		PoolSize:  10,
		KeyPrefix: "test:ncps:lock:",
	}
}

// getTestRetryConfig returns a retry configuration for testing.
func getTestRetryConfig() redis.RetryConfig {
	return redis.RetryConfig{
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
	skipIfRedisNotAvailable(t)

	ctx := context.Background()
	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	locker, err := redis.NewLocker(ctx, cfg, retryCfg, false)
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
	skipIfRedisNotAvailable(t)

	ctx := context.Background()
	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	// Create two separate locker instances
	locker1, err := redis.NewLocker(ctx, cfg, retryCfg, false)
	require.NoError(t, err)

	locker2, err := redis.NewLocker(ctx, cfg, retryCfg, false)
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
	skipIfRedisNotAvailable(t)

	ctx := context.Background()
	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	locker1, err := redis.NewLocker(ctx, cfg, retryCfg, false)
	require.NoError(t, err)

	locker2, err := redis.NewLocker(ctx, cfg, retryCfg, false)
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

func TestLocker_LockExpiry(t *testing.T) {
	t.Parallel()
	skipIfRedisNotAvailable(t)

	ctx := context.Background()
	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	locker1, err := redis.NewLocker(ctx, cfg, retryCfg, false)
	require.NoError(t, err)

	locker2, err := redis.NewLocker(ctx, cfg, retryCfg, false)
	require.NoError(t, err)

	key := getUniqueKey(t, "expiry")

	// Acquire lock with short TTL
	err = locker1.Lock(ctx, key, 1*time.Second)
	require.NoError(t, err)

	// Wait for lock to expire
	time.Sleep(2 * time.Second)

	// Second locker should be able to acquire (lock expired)
	err = locker2.Lock(ctx, key, 5*time.Second)
	require.NoError(t, err, "should acquire lock after TTL expiry")

	err = locker2.Unlock(ctx, key)
	require.NoError(t, err)
}

func TestLocker_RetryWithBackoff(t *testing.T) {
	t.Parallel()
	skipIfRedisNotAvailable(t)

	ctx := context.Background()
	cfg := getTestConfig()
	retryCfg := redis.RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Jitter:       true,
	}

	locker1, err := redis.NewLocker(ctx, cfg, retryCfg, false)
	require.NoError(t, err)

	locker2, err := redis.NewLocker(ctx, cfg, retryCfg, false)
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

func TestLocker_DegradedMode(t *testing.T) {
	t.Parallel()
	skipIfRedisNotAvailable(t)

	ctx := context.Background()

	// Configure with invalid Redis address
	cfg := redis.Config{
		Addrs:     []string{"localhost:9999"}, // Invalid port
		KeyPrefix: "test:ncps:lock:",
	}
	retryCfg := getTestRetryConfig()

	// With degraded mode enabled, should fall back to local locks
	locker, err := redis.NewLocker(ctx, cfg, retryCfg, true)
	require.NoError(t, err, "should create locker in degraded mode")

	key := getUniqueKey(t, "degraded")

	// Should still be able to lock (using local fallback)
	err = locker.Lock(ctx, key, 5*time.Second)
	require.NoError(t, err)

	err = locker.Unlock(ctx, key)
	require.NoError(t, err)
}

func TestLocker_DegradedModeDisabled(t *testing.T) {
	t.Parallel()
	skipIfRedisNotAvailable(t)

	ctx := context.Background()

	// Configure with invalid Redis address
	cfg := redis.Config{
		Addrs:     []string{"localhost:9999"}, // Invalid port
		KeyPrefix: "test:ncps:lock:",
	}
	retryCfg := getTestRetryConfig()

	// With degraded mode disabled, should fail to create locker
	_, err := redis.NewLocker(ctx, cfg, retryCfg, false)
	require.Error(t, err, "should fail to create locker without degraded mode")
}

func TestLocker_NoAddresses(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := redis.Config{
		Addrs: []string{}, // No addresses
	}
	retryCfg := getTestRetryConfig()

	_, err := redis.NewLocker(ctx, cfg, retryCfg, false)
	assert.ErrorIs(t, err, redis.ErrNoRedisAddrs)
}

func TestRWLocker_BasicReadWriteLock(t *testing.T) {
	t.Parallel()
	skipIfRedisNotAvailable(t)

	ctx := context.Background()
	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	locker, err := redis.NewRWLocker(ctx, cfg, retryCfg, false)
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
	skipIfRedisNotAvailable(t)

	ctx := context.Background()
	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	// Create multiple locker instances
	var lockers []interface {
		RLock(context.Context, string, time.Duration) error
		RUnlock(context.Context, string) error
	}

	for i := 0; i < 5; i++ {
		locker, err := redis.NewRWLocker(ctx, cfg, retryCfg, false)
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

		go func(_ int, l interface {
			RLock(context.Context, string, time.Duration) error
			RUnlock(context.Context, string) error
		},
		) {
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
	skipIfRedisNotAvailable(t)

	ctx := context.Background()
	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	locker1, err := redis.NewRWLocker(ctx, cfg, retryCfg, false)
	require.NoError(t, err)

	locker2, err := redis.NewRWLocker(ctx, cfg, retryCfg, false)
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
	skipIfRedisNotAvailable(t)

	ctx := context.Background()
	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	locker1, err := redis.NewRWLocker(ctx, cfg, retryCfg, false)
	require.NoError(t, err)

	locker2, err := redis.NewRWLocker(ctx, cfg, retryCfg, false)
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
