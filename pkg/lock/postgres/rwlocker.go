package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/local"
)

// RWLocker implements lock.RWLocker using PostgreSQL advisory locks.
// It provides both exclusive (write) and shared (read) locking semantics.
type RWLocker struct {
	*Locker

	// readConnections tracks dedicated connections for each active read lock
	readConnections map[string][]*sql.Conn
	readConnMu      sync.Mutex
}

// NewRWLocker creates a new PostgreSQL advisory lock-based read-write locker.
func NewRWLocker(
	ctx context.Context,
	querier database.Querier,
	cfg Config,
	retryCfg lock.RetryConfig,
	allowDegradedMode bool,
) (lock.RWLocker, error) {
	pgLocker, err := NewLocker(ctx, querier, cfg, retryCfg, allowDegradedMode)
	if err != nil {
		return nil, err
	}

	// The embedded Locker has a fallbackLocker of type lock.Locker (local.NewLocker()).
	// We must replace it with one that implements lock.RWLocker to prevent
	// a panic when RLock/RUnlock fall back to degraded mode.
	pgLocker.fallbackLocker = local.NewRWLocker()

	return &RWLocker{
		Locker:          pgLocker,
		readConnections: make(map[string][]*sql.Conn),
	}, nil
}

// RLock acquires a shared read lock.
// Multiple readers can hold the lock simultaneously, but they will block if a writer holds the lock.
// NOTE: The `ttl` parameter is ignored. The lock is held until RUnlock()
// is called or the underlying database connection is closed.
func (rw *RWLocker) RLock(ctx context.Context, key string, ttl time.Duration) error {
	// Check circuit breaker
	if !rw.circuitBreaker.AllowRequest() {
		if rw.allowDegradedMode {
			zerolog.Ctx(ctx).Warn().
				Str("key", key).
				Msg("circuit breaker open, using fallback local lock (DEGRADED MODE)")

			return rw.fallbackLocker.(lock.RWLocker).RLock(ctx, key, ttl)
		}

		return ErrCircuitBreakerOpen
	}

	lockID := rw.hashKey(key)

	var lastErr error

	for attempt := 0; attempt < rw.retryConfig.MaxAttempts; attempt++ {
		if attempt > 0 {
			// Record retry attempt for metrics
			lock.RecordLockRetryAttempt(ctx, lock.LockTypeRead)

			// Calculate backoff delay
			delay := lock.CalculateBackoff(rw.retryConfig, attempt)

			zerolog.Ctx(ctx).Debug().
				Str("key", key).
				Int("attempt", attempt+1).
				Dur("delay", delay).
				Msg("retrying read lock acquisition after backoff")

			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				lock.RecordLockFailure(ctx, lock.LockTypeRead, "distributed-postgres", lock.LockFailureContextCanceled)

				return ctx.Err()
			case <-timer.C:
			}
		}

		// Create a new dedicated connection for this read lock
		conn, err := rw.db.Conn(ctx)
		if err != nil {
			lastErr = err

			rw.circuitBreaker.RecordFailure()

			if !rw.circuitBreaker.AllowRequest() && rw.allowDegradedMode {
				zerolog.Ctx(ctx).Warn().
					Err(err).
					Str("key", key).
					Msg("database connection failed, switching to degraded mode")

				lock.RecordLockFailure(ctx, lock.LockTypeRead, "distributed-postgres", lock.LockFailureCircuitBreaker)

				return rw.fallbackLocker.(lock.RWLocker).RLock(ctx, key, ttl)
			}

			continue
		}

		// Try to acquire the shared lock using pg_try_advisory_lock_shared (non-blocking)
		var lockResult bool

		err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock_shared($1)", lockID).Scan(&lockResult)
		if err != nil {
			_ = conn.Close()
			lastErr = err

			// Check if this is a connection error
			if isConnectionError(err) {
				rw.circuitBreaker.RecordFailure()

				if !rw.circuitBreaker.AllowRequest() && rw.allowDegradedMode {
					zerolog.Ctx(ctx).Warn().
						Err(err).
						Str("key", key).
						Msg("database connection failed, switching to degraded mode")

					lock.RecordLockFailure(ctx, lock.LockTypeRead, "distributed-postgres", lock.LockFailureCircuitBreaker)

					return rw.fallbackLocker.(lock.RWLocker).RLock(ctx, key, ttl)
				}
			}

			continue
		}

		if !lockResult {
			// Lock is held by writer, retry
			_ = conn.Close()

			lastErr = ErrLockContention

			continue
		}

		// Success! Store the connection
		rw.readConnMu.Lock()
		rw.readConnections[key] = append(rw.readConnections[key], conn)
		rw.readConnMu.Unlock()

		rw.circuitBreaker.RecordSuccess()

		// Record metrics
		lock.RecordLockAcquisition(ctx, lock.LockTypeRead, "distributed-postgres", lock.LockResultSuccess)

		zerolog.Ctx(ctx).Debug().
			Str("key", key).
			Int64("lock_id", lockID).
			Dur("ttl", ttl).
			Int("attempts", attempt+1).
			Msg("acquired PostgreSQL shared advisory lock")

		return nil
	}

	// All retries exhausted
	lock.RecordLockFailure(ctx, lock.LockTypeRead, "distributed-postgres", lock.LockFailureMaxRetries)

	return fmt.Errorf("%w: key=%s after %d attempts: %w",
		ErrLockAcquisitionFailed, key, rw.retryConfig.MaxAttempts, lastErr)
}

// RUnlock releases a shared read lock.
func (rw *RWLocker) RUnlock(ctx context.Context, key string) error {
	// Check if we're in degraded mode
	if !rw.circuitBreaker.AllowRequest() && rw.allowDegradedMode {
		return rw.fallbackLocker.(lock.RWLocker).RUnlock(ctx, key)
	}

	lockID := rw.hashKey(key)

	// Get one of the connections for this read lock
	rw.readConnMu.Lock()

	conns, ok := rw.readConnections[key]
	if !ok || len(conns) == 0 {
		rw.readConnMu.Unlock()
		// This can happen if RLock failed but RUnlock is still called
		return nil
	}

	// Pop the last connection
	conn := conns[len(conns)-1]
	conns = conns[:len(conns)-1]

	if len(conns) == 0 {
		delete(rw.readConnections, key)
	} else {
		rw.readConnections[key] = conns
	}

	rw.readConnMu.Unlock()

	// Release the shared lock
	var unlockResult bool

	err := conn.QueryRowContext(ctx, "SELECT pg_advisory_unlock_shared($1)", lockID).Scan(&unlockResult)

	// Always close the connection, even if unlock failed
	_ = conn.Close()

	if err != nil {
		// Don't fail here - just log the error
		zerolog.Ctx(ctx).Warn().
			Err(err).
			Str("key", key).
			Int64("lock_id", lockID).
			Msg("failed to release PostgreSQL shared advisory lock")

		return nil
	}

	if !unlockResult {
		zerolog.Ctx(ctx).Warn().
			Str("key", key).
			Int64("lock_id", lockID).
			Msg("shared advisory lock was not held during unlock")
	}

	zerolog.Ctx(ctx).Debug().
		Str("key", key).
		Int64("lock_id", lockID).
		Msg("released PostgreSQL shared advisory lock")

	return nil
}
