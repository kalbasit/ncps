package local

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kalbasit/ncps/pkg/lock"
)

// RWLocker implements lock.RWLocker using per-key RWMutexes.
type RWLocker struct {
	mu      sync.Mutex
	lockers map[string]*keyRWLock
}

type keyRWLock struct {
	sync.RWMutex
	refCount  int
	startTime time.Time
}

// NewRWLocker creates a new local read-write locker.
func NewRWLocker() lock.RWLocker {
	return &RWLocker{
		lockers: make(map[string]*keyRWLock),
	}
}

// getLock returns the lock for the given key, creating it if it doesn't exist.
// It also increments the reference count.
func (rw *RWLocker) getLock(key string) *keyRWLock {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	kl, ok := rw.lockers[key]
	if !ok {
		kl = &keyRWLock{}
		rw.lockers[key] = kl
	}

	kl.refCount++

	return kl
}

// releaseLock decrements the reference count and removes the lock from the map if it reaches zero.
func (rw *RWLocker) releaseLock(key string) {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if kl, ok := rw.lockers[key]; ok {
		kl.refCount--
		if kl.refCount == 0 {
			delete(rw.lockers, key)
		}
	}
}

// Lock acquires an exclusive lock. The ttl parameter is ignored.
func (rw *RWLocker) Lock(ctx context.Context, key string, _ time.Duration) error {
	kl := rw.getLock(key)

	kl.Lock()

	kl.startTime = time.Now()

	// Record acquisition attempt
	lock.RecordLockAcquisition(ctx, lock.LockTypeWrite, lock.LockModeLocal, lock.LockResultSuccess)

	return nil
}

// Unlock releases an exclusive lock for the given key.
func (rw *RWLocker) Unlock(ctx context.Context, key string) error {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	kl, ok := rw.lockers[key]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnlockUnknownKey, key)
	}

	if !kl.startTime.IsZero() {
		duration := time.Since(kl.startTime).Seconds()
		lock.RecordLockDuration(ctx, lock.LockTypeWrite, lock.LockModeLocal, duration)

		kl.startTime = time.Time{}
	}

	kl.Unlock()

	kl.refCount--
	if kl.refCount == 0 {
		delete(rw.lockers, key)
	}

	return nil
}

// TryLock attempts to acquire an exclusive lock without blocking.
func (rw *RWLocker) TryLock(ctx context.Context, key string, _ time.Duration) (bool, error) {
	kl := rw.getLock(key)

	acquired := kl.TryLock()

	if acquired {
		kl.startTime = time.Now()

		lock.RecordLockAcquisition(ctx, lock.LockTypeWrite, lock.LockModeLocal, lock.LockResultSuccess)
	} else {
		lock.RecordLockAcquisition(ctx, lock.LockTypeWrite, lock.LockModeLocal, lock.LockResultContention)
		rw.releaseLock(key)
	}

	return acquired, nil
}

// RLock acquires a shared read lock. The ttl parameter is ignored.
func (rw *RWLocker) RLock(ctx context.Context, key string, _ time.Duration) error {
	kl := rw.getLock(key)

	kl.RLock()

	lock.RecordLockAcquisition(ctx, lock.LockTypeRead, lock.LockModeLocal, lock.LockResultSuccess)

	return nil
}

// RUnlock releases a shared read lock for the given key.
func (rw *RWLocker) RUnlock(_ context.Context, key string) error {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	kl, ok := rw.lockers[key]
	if !ok {
		return fmt.Errorf("%w: %s", ErrRUnlockUnknownKey, key)
	}

	kl.RUnlock()

	kl.refCount--
	if kl.refCount == 0 {
		delete(rw.lockers, key)
	}

	return nil
}
