package local

import (
	"context"
	"hash"
	"hash/fnv"
	"sync"
	"time"

	"github.com/kalbasit/ncps/pkg/lock"
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
