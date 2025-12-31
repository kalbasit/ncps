// Package local provides local (single-instance) lock implementations.
//
// These locks use standard Go sync primitives (sync.Mutex and sync.RWMutex)
// and are suitable for single-instance deployments. They ignore key and TTL
// parameters since local locks don't need them.
package local

import (
	"context"
	"sync"
	"time"

	"github.com/kalbasit/ncps/pkg/lock"
)

// Locker implements lock.Locker using sync.Mutex.
type Locker struct {
	mu sync.Mutex

	// Track lock acquisition times for metrics (key -> acquisition time)
	// Protected by mu, so no need for sync.Map
	acquisitionTimes map[string]time.Time
}

// NewLocker creates a new local locker.
func NewLocker() lock.Locker {
	return &Locker{
		acquisitionTimes: make(map[string]time.Time),
	}
}

// Lock acquires an exclusive lock. The ttl parameter is ignored, but the key is used for metrics.
func (l *Locker) Lock(ctx context.Context, key string, _ time.Duration) error {
	l.mu.Lock()

	// Record acquisition attempt
	lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, lock.LockModeLocal, lock.LockResultSuccess)

	// Track acquisition time for duration metrics
	l.acquisitionTimes[key] = time.Now()

	return nil
}

// Unlock releases an exclusive lock. The key parameter is ignored.
func (l *Locker) Unlock(ctx context.Context, key string) error {
	// Calculate and record lock hold duration
	if startTime, ok := l.acquisitionTimes[key]; ok {
		duration := time.Since(startTime).Seconds()
		lock.RecordLockDuration(ctx, lock.LockTypeExclusive, lock.LockModeLocal, duration)
		delete(l.acquisitionTimes, key)
	}

	l.mu.Unlock()

	return nil
}

// TryLock attempts to acquire an exclusive lock without blocking.
// The key and ttl parameters are ignored.
func (l *Locker) TryLock(ctx context.Context, key string, _ time.Duration) (bool, error) {
	acquired := l.mu.TryLock()

	if acquired {
		lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, lock.LockModeLocal, lock.LockResultSuccess)

		l.acquisitionTimes[key] = time.Now()
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
