package local

import (
	"context"
	"sync"
	"time"

	"github.com/kalbasit/ncps/pkg/lock"
)

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
