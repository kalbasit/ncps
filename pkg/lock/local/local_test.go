package local_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/lock/local"
)

// ensureLockHeld waits for a lock to be held in concurrent scenarios.
// This replaces arbitrary time.Sleep calls with semantic naming that documents
// the synchronization intent. The 50ms duration is sufficient for lock acquisition
// across all platforms while being much faster than longer waits.
func ensureLockHeld(t *testing.T) {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
}

func TestLocker_BasicLockUnlock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	locker := local.NewLocker()

	// Acquire lock
	err := locker.Lock(ctx, "test-key", 5*time.Second)
	require.NoError(t, err)

	// Release lock
	err = locker.Unlock(ctx, "test-key")
	require.NoError(t, err)
}

func TestLocker_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	locker := local.NewLocker()

	var (
		counter int64
		wg      sync.WaitGroup
	)

	// Start 10 goroutines that increment counter under lock

	for range 10 {
		wg.Go(func() {
			for range 100 {
				err := locker.Lock(ctx, "counter", 5*time.Second)
				require.NoError(t, err)

				// Critical section
				val := atomic.LoadInt64(&counter)

				time.Sleep(time.Microsecond) // Minimal work simulation (1 microsecond)
				atomic.StoreInt64(&counter, val+1)

				err = locker.Unlock(ctx, "counter")
				assert.NoError(t, err)
			}
		})
	}

	wg.Wait()

	// All increments should have succeeded
	assert.Equal(t, int64(1000), atomic.LoadInt64(&counter))
}

func TestLocker_TryLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	locker := local.NewLocker()

	// First acquisition should succeed
	acquired, err := locker.TryLock(ctx, "test-key", 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired)

	// Second acquisition should fail (lock is held)
	acquired2, err := locker.TryLock(ctx, "test-key", 5*time.Second)
	require.NoError(t, err)
	assert.False(t, acquired2)

	// Release lock
	err = locker.Unlock(ctx, "test-key")
	require.NoError(t, err)

	// Third acquisition should succeed
	acquired3, err := locker.TryLock(ctx, "test-key", 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired3)

	// Cleanup
	err = locker.Unlock(ctx, "test-key")
	require.NoError(t, err)
}

func TestRWLocker_BasicReadWriteLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	locker := local.NewRWLocker()

	// Acquire read lock
	err := locker.RLock(ctx, "test-key", 5*time.Second)
	require.NoError(t, err)

	// Release read lock
	err = locker.RUnlock(ctx, "test-key")
	require.NoError(t, err)

	// Acquire write lock
	err = locker.Lock(ctx, "test-key", 5*time.Second)
	require.NoError(t, err)

	// Release write lock
	err = locker.Unlock(ctx, "test-key")
	require.NoError(t, err)
}

func TestRWLocker_MultipleReaders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	locker := local.NewRWLocker()

	numReaders := 5

	var (
		wg            sync.WaitGroup
		barrier       sync.WaitGroup
		readersActive int64
	)

	// Use barrier to ensure all readers acquire locks before checking
	barrier.Add(numReaders)

	// Start 5 readers

	for range numReaders {
		wg.Go(func() {
			err := locker.RLock(ctx, "test-key", 5*time.Second)
			require.NoError(t, err)

			// Increment active readers
			atomic.AddInt64(&readersActive, 1)

			// Signal that this reader has acquired the lock
			barrier.Done()
			// Wait for all readers to acquire their locks
			barrier.Wait()

			// Now check that multiple readers are active
			active := atomic.LoadInt64(&readersActive)
			assert.GreaterOrEqual(t, active, int64(numReaders), "all readers should be active simultaneously")

			// Hold lock for a bit (ensure readers can coexist)
			ensureLockHeld(t)

			// Decrement active readers
			atomic.AddInt64(&readersActive, -1)

			err = locker.RUnlock(ctx, "test-key")
			assert.NoError(t, err)
		})
	}

	wg.Wait()
}

func TestRWLocker_WriterBlocksReaders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	locker := local.NewRWLocker()

	// Acquire write lock
	err := locker.Lock(ctx, "test-key", 5*time.Second)
	require.NoError(t, err)

	var writerHolding atomic.Int32
	writerHolding.Store(1)

	var readerAcquired atomic.Int32

	// Start a reader in background
	go func() {
		err := locker.RLock(ctx, "test-key", 5*time.Second)
		assert.NoError(t, err)

		// Reader should only acquire after writer releases
		assert.Equal(t, int32(0), writerHolding.Load(), "reader acquired while writer still holding")

		readerAcquired.Store(1)

		err = locker.RUnlock(ctx, "test-key")
		assert.NoError(t, err)
	}()

	// Hold write lock for a bit (ensure writer blocks readers)
	ensureLockHeld(t)

	// Release write lock
	writerHolding.Store(0)

	err = locker.Unlock(ctx, "test-key")
	require.NoError(t, err)

	// Wait for reader to finish
	ensureLockHeld(t)
	assert.Equal(t, int32(1), readerAcquired.Load(), "reader should have acquired lock")
}

func TestRWLocker_TryLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	locker := local.NewRWLocker()

	// Acquire write lock
	err := locker.Lock(ctx, "test-key", 5*time.Second)
	require.NoError(t, err)

	// TryLock should fail
	acquired, err := locker.TryLock(ctx, "test-key", 5*time.Second)
	require.NoError(t, err)
	assert.False(t, acquired)

	// Release lock
	err = locker.Unlock(ctx, "test-key")
	require.NoError(t, err)

	// TryLock should succeed
	acquired2, err := locker.TryLock(ctx, "test-key", 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired2)

	// Cleanup
	err = locker.Unlock(ctx, "test-key")
	require.NoError(t, err)
}

func TestLocker_IgnoresKeyAndTTL(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	locker := local.NewLocker()

	// Lock with one key
	err := locker.Lock(ctx, "key1", 1*time.Second)
	require.NoError(t, err)

	// TryLock with different key should succeed (different per-key mutex)
	acquired, err := locker.TryLock(ctx, "key2", 1*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired, "local lock should use per-key mutexes")

	// TryLock with same key should fail (same mutex)
	acquired2, err := locker.TryLock(ctx, "key1", 1*time.Second)
	require.NoError(t, err)
	assert.False(t, acquired2, "same key should be locked")

	err = locker.Unlock(ctx, "key1")
	require.NoError(t, err)

	err = locker.Unlock(ctx, "key2")
	require.NoError(t, err)

	// TTL parameter should still be ignored
	err = locker.Lock(ctx, "key3", 999*time.Hour)
	require.NoError(t, err)

	err = locker.Unlock(ctx, "key3")
	require.NoError(t, err)
}

func TestLocker_DeadlockReproduction(t *testing.T) {
	t.Parallel()

	// These keys were found to hash to the same shard (202) in the user's report.
	// In the new implementation, they use separate mutexes, so there's no deadlock.
	key1 := "download:narinfo:6wpnygxh29xzn5pkav0x66jxhfh9d6hj"
	key2 := "download:nar:0rwy6f0xg45wxlcz4cd2qwb88xfvskvadpv0pc7k5c1b18qal4yh"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	locker := local.NewLocker()

	t.Log("Acquiring first lock...")

	err := locker.Lock(ctx, key1, time.Second)
	require.NoError(t, err)

	defer func() {
		err := locker.Unlock(ctx, key1)
		assert.NoError(t, err)
	}()

	t.Log("Acquiring second lock (should NO LONGER deadlock)...")

	err = locker.Lock(ctx, key2, time.Second)
	require.NoError(t, err)

	defer func() {
		err := locker.Unlock(ctx, key2)
		assert.NoError(t, err)
	}()

	t.Log("Success!")
}

// TestLocker_ConcurrentUnlock tests the race condition where multiple goroutines
// call Unlock concurrently on the same key. Without proper synchronization,
// both can pass the !ok check and attempt to unlock, causing a panic.
func TestLocker_ConcurrentUnlock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	locker := local.NewLocker()

	// Acquire lock
	err := locker.Lock(ctx, "test-key", 5*time.Second)
	require.NoError(t, err)

	// Try to unlock from multiple goroutines simultaneously
	// This should trigger the race condition where both goroutines
	// can pass the !ok check before either completes the unlock
	var wg sync.WaitGroup

	numGoroutines := 10

	// Use a channel to synchronize the start of all goroutines
	start := make(chan struct{})

	for range numGoroutines {
		wg.Go(func() {
			<-start // Wait for signal to start

			_ = locker.Unlock(ctx, "test-key")
		})
	}

	// Release all goroutines at once to maximize race condition probability
	close(start)
	wg.Wait()

	// If we get here without panic, the race condition is fixed
}

// TestRWLocker_ConcurrentUnlock tests the race condition in RWLocker.Unlock.
func TestRWLocker_ConcurrentUnlock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	locker := local.NewRWLocker()

	// Acquire write lock
	err := locker.Lock(ctx, "test-key", 5*time.Second)
	require.NoError(t, err)

	// Try to unlock from multiple goroutines simultaneously
	var wg sync.WaitGroup

	numGoroutines := 10
	start := make(chan struct{})

	for range numGoroutines {
		wg.Go(func() {
			<-start

			_ = locker.Unlock(ctx, "test-key")
		})
	}

	close(start)
	wg.Wait()
}

// TestRWLocker_ConcurrentRUnlock tests the race condition in RWLocker.RUnlock.
func TestRWLocker_ConcurrentRUnlock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	locker := local.NewRWLocker()

	// Acquire read lock
	err := locker.RLock(ctx, "test-key", 5*time.Second)
	require.NoError(t, err)

	// Try to RUnlock from multiple goroutines simultaneously
	var wg sync.WaitGroup

	numGoroutines := 10
	start := make(chan struct{})

	for range numGoroutines {
		wg.Go(func() {
			<-start

			_ = locker.RUnlock(ctx, "test-key")
		})
	}

	close(start)
	wg.Wait()
}
