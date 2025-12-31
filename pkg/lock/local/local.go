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
	acquisitionTimes sync.Map
}

// NewLocker creates a new local locker.
func NewLocker() lock.Locker {
	return &Locker{}
}

// Lock acquires an exclusive lock. The ttl parameter is ignored, but the key is used for metrics.
func (l *Locker) Lock(ctx context.Context, key string, _ time.Duration) error {
	l.mu.Lock()

	// Record acquisition attempt
	lock.RecordLockAcquisition(ctx, "exclusive", "local", "success")

	// Track acquisition time for duration metrics
	l.acquisitionTimes.Store(key, time.Now())

	return nil
}

// Unlock releases an exclusive lock. The key parameter is ignored.
func (l *Locker) Unlock(ctx context.Context, key string) error {
	// Calculate and record lock hold duration
	if val, ok := l.acquisitionTimes.LoadAndDelete(key); ok {
		if startTime, ok := val.(time.Time); ok {
			duration := time.Since(startTime).Seconds()
			lock.RecordLockDuration(ctx, "exclusive", "local", duration)
		}
	}

	l.mu.Unlock()

	return nil
}

// TryLock attempts to acquire an exclusive lock without blocking.
// The key and ttl parameters are ignored.
func (l *Locker) TryLock(ctx context.Context, key string, _ time.Duration) (bool, error) {
	acquired := l.mu.TryLock()

	if acquired {
		lock.RecordLockAcquisition(ctx, "exclusive", "local", "success")
		l.acquisitionTimes.Store(key, time.Now())
	} else {
		lock.RecordLockAcquisition(ctx, "exclusive", "local", "contention")
	}

	return acquired, nil
}

// RWLocker implements lock.RWLocker using sync.RWMutex.
type RWLocker struct {
	mu sync.RWMutex

	// Track lock acquisition times for metrics (key -> acquisition time)
	// For write locks and read locks separately
	writeAcquisitionTimes sync.Map
	readAcquisitionTimes  sync.Map
}

// NewRWLocker creates a new local read-write locker.
func NewRWLocker() lock.RWLocker {
	return &RWLocker{}
}

// Lock acquires an exclusive lock. The key and ttl parameters are ignored.
func (rw *RWLocker) Lock(ctx context.Context, key string, _ time.Duration) error {
	rw.mu.Lock()

	lock.RecordLockAcquisition(ctx, "write", "local", "success")

	rw.writeAcquisitionTimes.Store(key, time.Now())

	return nil
}

// Unlock releases an exclusive lock. The key parameter is ignored.
func (rw *RWLocker) Unlock(ctx context.Context, key string) error {
if val, ok := rw.writeAcquisitionTimes.LoadAndDelete(key); ok {
	if startTime, ok := val.(time.Time); ok {
		duration := time.Since(startTime).Seconds()
		lock.RecordLockDuration(ctx, "write", "local", duration)
	}
}

	rw.mu.Unlock()

	return nil
}

// TryLock attempts to acquire an exclusive lock without blocking.
// The key and ttl parameters are ignored.
func (rw *RWLocker) TryLock(ctx context.Context, key string, _ time.Duration) (bool, error) {
	acquired := rw.mu.TryLock()

	if acquired {
		lock.RecordLockAcquisition(ctx, "write", "local", "success")
		rw.writeAcquisitionTimes.Store(key, time.Now())
	} else {
		lock.RecordLockAcquisition(ctx, "write", "local", "contention")
	}

	return acquired, nil
}

// RLock acquires a shared read lock. The key and ttl parameters are ignored.
func (rw *RWLocker) RLock(ctx context.Context, key string, _ time.Duration) error {
	rw.mu.RLock()

	lock.RecordLockAcquisition(ctx, "read", "local", "success")

	rw.readAcquisitionTimes.Store(key, time.Now())

	return nil
}

// RUnlock releases a shared read lock. The key parameter is ignored.
func (rw *RWLocker) RUnlock(ctx context.Context, key string) error {
	if startTime, ok := rw.readAcquisitionTimes.LoadAndDelete(key); ok {
		duration := time.Since(startTime.(time.Time)).Seconds()
		lock.RecordLockDuration(ctx, "read", "local", duration)
	}

	rw.mu.RUnlock()

	return nil
}
