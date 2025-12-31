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
}

// NewLocker creates a new local locker.
func NewLocker() lock.Locker {
	return &Locker{}
}

// Lock acquires an exclusive lock. The key and ttl parameters are ignored.
func (l *Locker) Lock(_ context.Context, _ string, _ time.Duration) error {
	l.mu.Lock()

	return nil
}

// Unlock releases an exclusive lock. The key parameter is ignored.
func (l *Locker) Unlock(_ context.Context, _ string) error {
	l.mu.Unlock()

	return nil
}

// TryLock attempts to acquire an exclusive lock without blocking.
// The key and ttl parameters are ignored.
func (l *Locker) TryLock(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return l.mu.TryLock(), nil
}

// RWLocker implements lock.RWLocker using sync.RWMutex.
type RWLocker struct {
	mu sync.RWMutex
}

// NewRWLocker creates a new local read-write locker.
func NewRWLocker() lock.RWLocker {
	return &RWLocker{}
}

// Lock acquires an exclusive lock. The key and ttl parameters are ignored.
func (rw *RWLocker) Lock(_ context.Context, _ string, _ time.Duration) error {
	rw.mu.Lock()

	return nil
}

// Unlock releases an exclusive lock. The key parameter is ignored.
func (rw *RWLocker) Unlock(_ context.Context, _ string) error {
	rw.mu.Unlock()

	return nil
}

// TryLock attempts to acquire an exclusive lock without blocking.
// The key and ttl parameters are ignored.
func (rw *RWLocker) TryLock(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return rw.mu.TryLock(), nil
}

// RLock acquires a shared read lock. The key and ttl parameters are ignored.
func (rw *RWLocker) RLock(_ context.Context, _ string, _ time.Duration) error {
	rw.mu.RLock()

	return nil
}

// RUnlock releases a shared read lock. The key parameter is ignored.
func (rw *RWLocker) RUnlock(_ context.Context, _ string) error {
	rw.mu.RUnlock()

	return nil
}
