package local

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kalbasit/ncps/pkg/lock"
)

var (
	// ErrUnlockUnknownKey is returned when attempting to unlock a key that is not locked.
	ErrUnlockUnknownKey = fmt.Errorf("local.Locker: unlock of unknown key")

	// ErrRUnlockUnknownKey is returned when attempting to runlock a key that is not locked.
	ErrRUnlockUnknownKey = fmt.Errorf("local.Locker: runlock of unknown key")
)

// Locker implements lock.Locker using per-key mutexes.
// Uses a map of mutexes to provide true per-key locking semantics
// without the risk of shard collisions. Ref-counting is used to
// clean up mutexes when they are no longer in use.
type Locker struct {
	mu      sync.Mutex
	lockers map[string]*keyLock
}

type keyLock struct {
	sync.Mutex
	refCount  int
	startTime time.Time
}

// NewLocker creates a new local locker.
func NewLocker() lock.Locker {
	return &Locker{
		lockers: make(map[string]*keyLock),
	}
}

// getLock returns the lock for the given key, creating it if it doesn't exist.
// It also increments the reference count.
func (l *Locker) getLock(key string) *keyLock {
	l.mu.Lock()
	defer l.mu.Unlock()

	kl, ok := l.lockers[key]
	if !ok {
		kl = &keyLock{}
		l.lockers[key] = kl
	}

	kl.refCount++

	return kl
}

// releaseLock decrements the reference count and removes the lock from the map if it reaches zero.
func (l *Locker) releaseLock(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	kl := l.lockers[key]

	kl.refCount--
	if kl.refCount == 0 {
		delete(l.lockers, key)
	}
}

// Lock acquires an exclusive lock. The ttl parameter is ignored.
func (l *Locker) Lock(ctx context.Context, key string, _ time.Duration) error {
	kl := l.getLock(key)

	kl.Lock()

	kl.startTime = time.Now()

	// Record acquisition attempt
	lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, lock.LockModeLocal, lock.LockResultSuccess)

	return nil
}

// Unlock releases an exclusive lock for the given key.
func (l *Locker) Unlock(ctx context.Context, key string) error {
	l.mu.Lock()
	kl, ok := l.lockers[key]
	l.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrUnlockUnknownKey, key)
	}

	if !kl.startTime.IsZero() {
		duration := time.Since(kl.startTime).Seconds()
		lock.RecordLockDuration(ctx, lock.LockTypeExclusive, lock.LockModeLocal, duration)

		kl.startTime = time.Time{}
	}

	kl.Unlock()
	l.releaseLock(key)

	return nil
}

// TryLock attempts to acquire an exclusive lock without blocking.
func (l *Locker) TryLock(ctx context.Context, key string, _ time.Duration) (bool, error) {
	kl := l.getLock(key)

	acquired := kl.TryLock()

	if acquired {
		kl.startTime = time.Now()

		lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, lock.LockModeLocal, lock.LockResultSuccess)
	} else {
		lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, lock.LockModeLocal, lock.LockResultContention)
		l.releaseLock(key)
	}

	return acquired, nil
}
