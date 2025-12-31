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

	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := 0; j < 100; j++ {
				err := locker.Lock(ctx, "counter", 5*time.Second)
				assert.NoError(t, err)

				// Critical section
				val := atomic.LoadInt64(&counter)

				time.Sleep(time.Microsecond) // Simulate work
				atomic.StoreInt64(&counter, val+1)

				err = locker.Unlock(ctx, "counter")
				assert.NoError(t, err)
			}
		}()
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

	var (
		wg            sync.WaitGroup
		readersActive int64
	)

	// Start 5 readers

	for i := 0; i < 5; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			err := locker.RLock(ctx, "test-key", 5*time.Second)
			assert.NoError(t, err)

			// Increment active readers
			atomic.AddInt64(&readersActive, 1)

			// Hold lock for a bit
			time.Sleep(50 * time.Millisecond)

			// Check that multiple readers are active
			active := atomic.LoadInt64(&readersActive)
			assert.Greater(t, active, int64(1), "multiple readers should be active simultaneously")

			// Decrement active readers
			atomic.AddInt64(&readersActive, -1)

			err = locker.RUnlock(ctx, "test-key")
			assert.NoError(t, err)
		}()
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

	// Hold write lock for a bit
	time.Sleep(50 * time.Millisecond)

	// Release write lock
	writerHolding.Store(0)

	err = locker.Unlock(ctx, "test-key")
	require.NoError(t, err)

	// Wait for reader to finish
	time.Sleep(50 * time.Millisecond)
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

	// Lock with different keys should still conflict (local lock ignores keys)
	err := locker.Lock(ctx, "key1", 1*time.Second)
	require.NoError(t, err)

	// TryLock with different key should fail (same mutex)
	acquired, err := locker.TryLock(ctx, "key2", 1*time.Second)
	require.NoError(t, err)
	assert.False(t, acquired, "local lock should ignore keys")

	err = locker.Unlock(ctx, "key1")
	require.NoError(t, err)

	// Now TryLock should succeed
	acquired2, err := locker.TryLock(ctx, "key2", 1*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired2)

	err = locker.Unlock(ctx, "key2")
	require.NoError(t, err)
}
