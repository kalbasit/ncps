// Package local provides local (single-instance) lock implementations.
//
// These locks use standard Go sync primitives (sync.Mutex and sync.RWMutex)
// and are suitable for single-instance deployments. They ignore TTL
// parameters since local locks don't expire.
package local

import (
	"context"
	"hash"
	"hash/fnv"
	"sync"
	"time"

	"github.com/kalbasit/ncps/pkg/lock"
)

const (
	// numShards is the number of mutex shards for lock striping.
	// This provides bounded memory usage while allowing concurrent locks for different keys.
	numShards = 1024
)

// Locker implements lock.Locker using lock striping (sharded mutexes).
// Uses a fixed pool of mutexes to avoid unbounded memory growth while
// still providing per-key locking semantics with good concurrency.
type Locker struct {
	hasherPool sync.Pool

	shards [numShards]sync.Mutex

	// Protect acquisition times map with a separate mutex
	timesMu          sync.Mutex
	acquisitionTimes map[string]time.Time
}

// NewLocker creates a new local locker.
func NewLocker() lock.Locker {
	return &Locker{
		acquisitionTimes: make(map[string]time.Time),
		hasherPool:       sync.Pool{New: func() interface{} { return fnv.New32a() }},
	}
}

// getShard returns the shard index for a given key.
func (l *Locker) getShard(key string) int {
	h, ok := l.hasherPool.Get().(hash.Hash32)
	if !ok {
		panic("local.Locker: unexpected type in hasher pool; expected hash.Hash32")
	}

	defer l.hasherPool.Put(h)

	h.Reset()
	h.Write([]byte(key))

	return int(h.Sum32() % numShards)
}

// Lock acquires an exclusive lock. The ttl parameter is ignored, but the key is used for sharding.
func (l *Locker) Lock(ctx context.Context, key string, _ time.Duration) error {
	// Acquire the shard lock for this key
	shard := l.getShard(key)
	l.shards[shard].Lock()

	// Record acquisition attempt
	lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, lock.LockModeLocal, lock.LockResultSuccess)

	// Track acquisition time for duration metrics
	l.timesMu.Lock()
	l.acquisitionTimes[key] = time.Now()
	l.timesMu.Unlock()

	return nil
}

// Unlock releases an exclusive lock for the given key.
func (l *Locker) Unlock(ctx context.Context, key string) error {
	// Calculate and record lock hold duration
	l.timesMu.Lock()

	if startTime, ok := l.acquisitionTimes[key]; ok {
		duration := time.Since(startTime).Seconds()
		lock.RecordLockDuration(ctx, lock.LockTypeExclusive, lock.LockModeLocal, duration)
		delete(l.acquisitionTimes, key)
	}

	l.timesMu.Unlock()

	// Unlock the shard for this key
	shard := l.getShard(key)
	l.shards[shard].Unlock()

	return nil
}

// TryLock attempts to acquire an exclusive lock without blocking.
func (l *Locker) TryLock(ctx context.Context, key string, _ time.Duration) (bool, error) {
	// Try to acquire the shard lock for this key
	shard := l.getShard(key)
	acquired := l.shards[shard].TryLock()

	if acquired {
		lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, lock.LockModeLocal, lock.LockResultSuccess)

		l.timesMu.Lock()
		l.acquisitionTimes[key] = time.Now()
		l.timesMu.Unlock()
	} else {
		lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, lock.LockModeLocal, lock.LockResultContention)
	}

	return acquired, nil
}

// RWLocker implements lock.RWLocker using sync.RWMutex.
type RWLocker struct {
	mu sync.RWMutex

	// Track lock acquisition times for duration metrics (write locks only)
	// Note: Read lock duration tracking is not supported due to concurrent access
	// Protected by mu (write locks are exclusive), so no need for sync.Map
	writeAcquisitionTimes map[string]time.Time
}

// NewRWLocker creates a new local read-write locker.
func NewRWLocker() lock.RWLocker {
	return &RWLocker{
		writeAcquisitionTimes: make(map[string]time.Time),
	}
}

// Lock acquires an exclusive lock. The key and ttl parameters are ignored.
func (rw *RWLocker) Lock(ctx context.Context, key string, _ time.Duration) error {
	rw.mu.Lock()

	lock.RecordLockAcquisition(ctx, lock.LockTypeWrite, lock.LockModeLocal, lock.LockResultSuccess)

	rw.writeAcquisitionTimes[key] = time.Now()

	return nil
}

// Unlock releases an exclusive lock. The key parameter is ignored.
func (rw *RWLocker) Unlock(ctx context.Context, key string) error {
	if startTime, ok := rw.writeAcquisitionTimes[key]; ok {
		duration := time.Since(startTime).Seconds()
		lock.RecordLockDuration(ctx, lock.LockTypeWrite, lock.LockModeLocal, duration)
		delete(rw.writeAcquisitionTimes, key)
	}

	rw.mu.Unlock()

	return nil
}

// TryLock attempts to acquire an exclusive lock without blocking.
// The key and ttl parameters are ignored.
func (rw *RWLocker) TryLock(ctx context.Context, key string, _ time.Duration) (bool, error) {
	acquired := rw.mu.TryLock()

	if acquired {
		lock.RecordLockAcquisition(ctx, lock.LockTypeWrite, lock.LockModeLocal, lock.LockResultSuccess)

		rw.writeAcquisitionTimes[key] = time.Now()
	} else {
		lock.RecordLockAcquisition(ctx, lock.LockTypeWrite, lock.LockModeLocal, lock.LockResultContention)
	}

	return acquired, nil
}

// RLock acquires a shared read lock. The key and ttl parameters are ignored.
func (rw *RWLocker) RLock(ctx context.Context, _ string, _ time.Duration) error {
	rw.mu.RLock()

	lock.RecordLockAcquisition(ctx, lock.LockTypeRead, lock.LockModeLocal, lock.LockResultSuccess)

	return nil
}

// RUnlock releases a shared read lock. The ctx and key parameters are ignored.
func (rw *RWLocker) RUnlock(_ context.Context, _ string) error {
	rw.mu.RUnlock()

	return nil
}
