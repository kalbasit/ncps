// Package lock provides an abstraction layer for locking mechanisms.
//
// This package supports both local (single-instance) and distributed (multi-instance)
// locking implementations through a common interface. Local locks use standard
// sync.Mutex and sync.RWMutex. Distributed locks use Redis with the Redlock algorithm.
package lock

import (
	"context"
	"time"
)

// Locker provides exclusive locking semantics.
//
// Implementations can be local (using sync.Mutex) or distributed (using Redis).
// The interface is designed to support key-based locking for distributed scenarios
// while allowing local implementations to ignore the key parameter.
type Locker interface {
	// Lock acquires an exclusive lock for the given key with the specified TTL.
	//
	// For local implementations, the key and ttl parameters are ignored and the
	// method behaves like sync.Mutex.Lock().
	//
	// For distributed implementations (Redis), this method will:
	//   - Attempt to acquire the lock using the Redlock algorithm
	//   - Retry with exponential backoff if configured
	//   - Return an error if the lock cannot be acquired after max retries
	//
	// The context can be used to cancel lock acquisition attempts.
	Lock(ctx context.Context, key string, ttl time.Duration) error

	// Unlock releases an exclusive lock for the given key.
	//
	// For local implementations, the key parameter is ignored and the method
	// behaves like sync.Mutex.Unlock().
	//
	// For distributed implementations, this releases the Redis lock. If unlock
	// fails, the lock will eventually expire based on its TTL.
	//
	// It is safe to call Unlock even if Lock failed, but it may return an error.
	Unlock(ctx context.Context, key string) error

	// TryLock attempts to acquire an exclusive lock without blocking.
	//
	// For local implementations, this uses sync.Mutex.TryLock().
	//
	// For distributed implementations, this attempts to acquire the Redis lock
	// with a single attempt (no retries).
	//
	// Returns:
	//   - (true, nil) if the lock was acquired
	//   - (false, nil) if the lock is held by someone else
	//   - (false, error) if an error occurred
	TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

// RWLocker provides read-write locking semantics.
//
// Multiple readers can hold the lock simultaneously, but writers have exclusive
// access. This is useful for protecting resources during read-heavy operations
// while allowing safe cleanup operations.
type RWLocker interface {
	Locker

	// RLock acquires a shared read lock for the given key with the specified TTL.
	//
	// For local implementations, the key and ttl parameters are ignored and the
	// method behaves like sync.RWMutex.RLock().
	//
	// For distributed implementations (Redis), multiple readers can acquire the
	// lock concurrently, but they must wait for any active writers to finish.
	RLock(ctx context.Context, key string, ttl time.Duration) error

	// RUnlock releases a shared read lock for the given key.
	//
	// For local implementations, the key parameter is ignored and the method
	// behaves like sync.RWMutex.RUnlock().
	RUnlock(ctx context.Context, key string) error
}
