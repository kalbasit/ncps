package mysql

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/lock"
)

// RWLocker implements lock.RWLocker using MySQL/MariaDB GET_LOCK function.
// Note: MySQL's GET_LOCK is exclusive ONLY. There is no shared lock equivalent.
// Therefore, RLock behaves exactly like Lock, and RUnlock behaves like Unlock.
// This matches the behavior of exclusive locking but satisfies the interface.
// If your application requires high read concurrency that can be shared,
// consider using Redis or PostgreSQL backends instead.
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
	locker, err := NewLocker(ctx, querier, cfg, retryCfg, allowDegradedMode)
	if err != nil {
		return nil, err
	}

	// Log a warning about MySQL's exclusive-only locking limitation
	zerolog.Ctx(ctx).Warn().
		Msg("MySQL RWLocker uses exclusive locks for both RLock and Lock - no read concurrency is provided. " +
			"Consider using PostgreSQL or Redis for true shared read locks.")

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
