package mysql

import (
	"context"
	"time"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/lock"
)

// RWLocker implements lock.RWLocker using MySQL/MariaDB GET_LOCK function.
// Note: MySQL's GET_LOCK is exclusive ONLY. There is no shared lock equivalent.
// Therefore, RLock behaves exactly like Lock, and RUnlock behaves like Unlock.
// This matches the behavior of exclusive locking but satisfies the interface.
type RWLocker struct {
	*Locker
}

// NewRWLocker creates a new MySQL/MariaDB advisory lock-based RWLocker.
func NewRWLocker(
	ctx context.Context,
	querier database.Querier,
	cfg Config,
	retryCfg lock.RetryConfig,
	allowDegradedMode bool,
) (lock.RWLocker, error) {
	// Re-use the existing Locker implementation
	l, err := NewLocker(ctx, querier, cfg, retryCfg, allowDegradedMode)
	if err != nil {
		return nil, err
	}

	// We know NewLocker returns *Locker struct which implements lock.Locker
	// We need to type assert or just embed it. All return types are interfaces.
	// Since *Locker is the concrete type, we can use it.
	locker, ok := l.(*Locker)
	if !ok {
		// Should not happen unless NewLocker changes
		// Fallback to creating a new local RWLocker if something is wrong?
		// Or just return error.
		return nil, ErrNoDatabase // Internal error really
	}

	return &RWLocker{
		Locker: locker,
	}, nil
}

// RLock acquires a read lock.
// For MySQL GET_LOCK, this is actually an EXCLUSIVE lock.
func (l *RWLocker) RLock(ctx context.Context, key string, ttl time.Duration) error {
	return l.Lock(ctx, key, ttl)
}

// RUnlock releases a read lock.
func (l *RWLocker) RUnlock(ctx context.Context, key string) error {
	return l.Unlock(ctx, key)
}

// Ensure interface compliance.
var _ lock.RWLocker = (*RWLocker)(nil)

// Note regarding fallback:
// The embedded Locker already handles fallback to local.Locker.
// However, local.Locker IS a lock.Locker.
// Does local.NewLocker() return a struct that also implements RWLocker?
// local.NewLocker() returns *local.Locker.
// Let's check if local.Locker implements RWLocker?
// The interface `lock.Locker` has Lock/Unlock/TryLock.
// The interface `lock.RWLocker` has those + RLock/RUnlock.
// IF we fall back in `Locker.Lock`, it calls `l.fallbackLocker.Lock`.
// `l.fallbackLocker` defines strictly `lock.Locker`.
// IF we are using RWLocker, we might want `fallbackLocker` to also be `lock.RWLocker`?
// In `Locker` struct: `fallbackLocker lock.Locker`.
// So if we use `RWLocker.RLock` -> `RWLocker.Lock` -> `Locker.Lock` -> `Locker.fallbackLocker.Lock`.
// This works because `local.Locker` supports `Lock`.
// But wait, if we wanted "Real" Shared locks in fallback?
// `local.NewLocker()` returns a mutex based locker.
// `local.NewRWLocker()` returns a RWMutex based locker.
// Our `RWLocker` struct wraps a `Locker` struct.
// That `Locker` struct has a `fallbackLocker` field initialized with `local.NewLocker()` (Exclusive).
// So even if we call `RLock`, we are falling back to Exclusive Local Lock if DB fails.
// This is consistent: MySQL lock is exclusive, so fallback being exclusive is fine.
