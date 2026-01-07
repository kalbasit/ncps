package local

import (
	"context"
	"hash"
	"hash/fnv"
	"sync"
	"time"

	"github.com/kalbasit/ncps/pkg/lock"
)

// RWLocker implements lock.RWLocker using sync.RWMutex.
type RWLocker struct {
	hasherPool sync.Pool

	shards [numShards]sync.RWMutex

	// Track lock acquisition times for duration metrics (write locks only)
	// Note: Read lock duration tracking is not supported due to concurrent access
	// Protected by a separate mu (write locks are exclusive), so no need for sync.Map
	timesMu               sync.Mutex
	writeAcquisitionTimes map[string]time.Time
}

// NewRWLocker creates a new local read-write locker.
func NewRWLocker() lock.RWLocker {
	return &RWLocker{
		hasherPool:            sync.Pool{New: func() interface{} { return fnv.New32a() }},
		writeAcquisitionTimes: make(map[string]time.Time),
	}
}

// getShard returns the shard index for a given key.
func (rw *RWLocker) getShard(key string) int {
	h, ok := rw.hasherPool.Get().(hash.Hash32)
	if !ok {
		panic("local.RWLocker: unexpected type in hasher pool; expected hash.Hash32")
	}

	defer rw.hasherPool.Put(h)

	h.Reset()
	h.Write([]byte(key))

	return int(h.Sum32() % numShards)
}

// Lock acquires an exclusive lock. The ttl parameter is ignored, but the key is used for sharding.
func (rw *RWLocker) Lock(ctx context.Context, key string, _ time.Duration) error {
	// Acquire the shard lock for this key
	shard := rw.getShard(key)
	rw.shards[shard].Lock()

	// Record acquisition attempt
	lock.RecordLockAcquisition(ctx, lock.LockTypeWrite, lock.LockModeLocal, lock.LockResultSuccess)

	rw.timesMu.Lock()
	rw.writeAcquisitionTimes[key] = time.Now()
	rw.timesMu.Unlock()

	return nil
}

// Unlock releases an exclusive lock for the given key.
func (rw *RWLocker) Unlock(ctx context.Context, key string) error {
	// Calculate and record lock hold duration
	rw.timesMu.Lock()

	if startTime, ok := rw.writeAcquisitionTimes[key]; ok {
		duration := time.Since(startTime).Seconds()
		lock.RecordLockDuration(ctx, lock.LockTypeWrite, lock.LockModeLocal, duration)
		delete(rw.writeAcquisitionTimes, key)
	}

	rw.timesMu.Unlock()

	// Unlock the shard for this key
	shard := rw.getShard(key)
	rw.shards[shard].Unlock()

	return nil
}

// TryLock attempts to acquire an exclusive lock without blocking.
func (rw *RWLocker) TryLock(ctx context.Context, key string, _ time.Duration) (bool, error) {
	// Try to acquire the shard lock for this key
	shard := rw.getShard(key)
	acquired := rw.shards[shard].TryLock()

	if acquired {
		lock.RecordLockAcquisition(ctx, lock.LockTypeWrite, lock.LockModeLocal, lock.LockResultSuccess)

		rw.timesMu.Lock()
		rw.writeAcquisitionTimes[key] = time.Now()
		rw.timesMu.Unlock()
	} else {
		lock.RecordLockAcquisition(ctx, lock.LockTypeWrite, lock.LockModeLocal, lock.LockResultContention)
	}

	return acquired, nil
}

// RLock acquires a shared read lock. The ttl parameter is ignored, but the key is used for sharding.
func (rw *RWLocker) RLock(ctx context.Context, key string, _ time.Duration) error {
	// Acquire the shard lock for this key
	shard := rw.getShard(key)
	rw.shards[shard].RLock()

	lock.RecordLockAcquisition(ctx, lock.LockTypeRead, lock.LockModeLocal, lock.LockResultSuccess)

	return nil
}

// Unlock releases a shared read lock for the given key.
func (rw *RWLocker) RUnlock(_ context.Context, key string) error {
	// Acquire the shard lock for this key
	shard := rw.getShard(key)
	rw.shards[shard].RUnlock()

	return nil
}
