package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"sync"
	"time"

	"github.com/rs/zerolog"

	mathrand "math/rand"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/local"
)

// Locker implements lock.Locker using PostgreSQL advisory locks.
type Locker struct {
	db                *sql.DB
	keyPrefix         string
	retryConfig       RetryConfig
	allowDegradedMode bool

	// connections tracks dedicated connections for each active lock
	connections map[string]*sql.Conn
	connMu      sync.Mutex

	// fallbackLocker is used when database is unavailable and degraded mode is enabled
	fallbackLocker lock.Locker

	// circuitBreaker tracks database health
	circuitBreaker *circuitBreaker

	// Track lock acquisition times for duration metrics
	acquisitionTimes sync.Map
}

// NewLocker creates a new PostgreSQL advisory lock-based locker.
func NewLocker(
	ctx context.Context,
	querier database.Querier,
	cfg Config,
	retryCfg RetryConfig,
	allowDegradedMode bool,
) (lock.Locker, error) {
	if querier == nil {
		return nil, ErrNoDatabase
	}

	// Get the underlying database connection
	db := querier.DB()

	// We can't easily detect database type from just the connection
	// Instead, we'll try to execute a PostgreSQL-specific query to verify
	// Note: In production, the database type will be known from the connection string

	// Test database connection with a simple advisory lock/unlock to verify PostgreSQL
	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, err := db.Conn(testCtx)
	if err != nil {
		if allowDegradedMode {
			zerolog.Ctx(ctx).Warn().
				Err(err).
				Msg("failed to connect to PostgreSQL, falling back to local locks (DEGRADED MODE)")

			return local.NewLocker(), nil
		}

		return nil, fmt.Errorf("%w: %w", ErrDatabaseConnectionFailed, err)
	}
	defer conn.Close()

	// Try a test advisory lock to verify functionality
	var testLockResult bool

	err = conn.QueryRowContext(testCtx, "SELECT pg_try_advisory_lock(123456789)").Scan(&testLockResult)
	if err != nil {
		if allowDegradedMode {
			zerolog.Ctx(ctx).Warn().
				Err(err).
				Msg("PostgreSQL advisory locks not available, falling back to local locks (DEGRADED MODE)")

			return local.NewLocker(), nil
		}

		return nil, fmt.Errorf("PostgreSQL advisory locks not available: %w", err)
	}

	// Unlock the test lock
	if testLockResult {
		_, _ = conn.ExecContext(testCtx, "SELECT pg_advisory_unlock(123456789)")
	}

	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "ncps:lock:"
	}

	zerolog.Ctx(ctx).Info().
		Msg("connected to PostgreSQL for distributed locking using advisory locks")

	return &Locker{
		db:                db,
		keyPrefix:         cfg.KeyPrefix,
		retryConfig:       retryCfg,
		allowDegradedMode: allowDegradedMode,
		connections:       make(map[string]*sql.Conn),
		fallbackLocker:    local.NewLocker(),
		circuitBreaker:    newCircuitBreaker(5, 1*time.Minute),
	}, nil
}

// hashKey converts a string key to an int64 for use with PostgreSQL advisory locks.
// Uses FNV-1a hash algorithm for consistent hashing.
func (l *Locker) hashKey(key string) int64 {
	h := fnv.New64a()
	h.Write([]byte(l.keyPrefix + key))

	// Convert uint64 to int64 (PostgreSQL uses bigint/int64)
	//nolint:gosec // Hash output is always valid for int64 conversion
	return int64(h.Sum64())
}

// getOrCreateConn gets or creates a dedicated connection for the given key.
func (l *Locker) getOrCreateConn(ctx context.Context, key string) (*sql.Conn, error) {
	l.connMu.Lock()
	defer l.connMu.Unlock()

	if conn, ok := l.connections[key]; ok {
		return conn, nil
	}

	conn, err := l.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create dedicated connection: %w", err)
	}

	l.connections[key] = conn

	return conn, nil
}

// releaseConn releases the dedicated connection for the given key.
func (l *Locker) releaseConn(key string) {
	l.connMu.Lock()
	defer l.connMu.Unlock()

	if conn, ok := l.connections[key]; ok {
		_ = conn.Close()

		delete(l.connections, key)
	}
}

// Lock acquires an exclusive lock with retry and exponential backoff.
func (l *Locker) Lock(ctx context.Context, key string, ttl time.Duration) error {
	// Check circuit breaker
	if l.circuitBreaker.isOpen() {
		if l.allowDegradedMode {
			zerolog.Ctx(ctx).Warn().
				Str("key", key).
				Msg("circuit breaker open, using fallback local lock (DEGRADED MODE)")

			return l.fallbackLocker.Lock(ctx, key, ttl)
		}

		return ErrCircuitBreakerOpen
	}

	lockID := l.hashKey(key)

	var lastErr error

	for attempt := 0; attempt < l.retryConfig.MaxAttempts; attempt++ {
		if attempt > 0 {
			// Record retry attempt for metrics
			lock.RecordLockRetryAttempt(ctx, lock.LockTypeExclusive)

			// Calculate backoff delay
			delay := l.calculateBackoff(attempt)

			zerolog.Ctx(ctx).Debug().
				Str("key", key).
				Int("attempt", attempt+1).
				Dur("delay", delay).
				Msg("retrying lock acquisition after backoff")

			select {
			case <-ctx.Done():
				lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-postgres", lock.LockFailureContextCanceled)

				return ctx.Err()
			case <-time.After(delay):
			}
		}

		// Get or create a dedicated connection for this lock
		conn, err := l.getOrCreateConn(ctx, key)
		if err != nil {
			lastErr = err

			l.circuitBreaker.recordFailure()

			if l.circuitBreaker.isOpen() && l.allowDegradedMode {
				zerolog.Ctx(ctx).Warn().
					Err(err).
					Str("key", key).
					Msg("database connection failed, switching to degraded mode")

				lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-postgres", lock.LockFailureCircuitBreaker)

				return l.fallbackLocker.Lock(ctx, key, ttl)
			}

			continue
		}

		// Try to acquire the lock using pg_lock_advisory
		var lockResult bool

		err = conn.QueryRowContext(ctx, "SELECT pg_advisory_lock($1)", lockID).Scan(&lockResult)
		if err != nil {
			lastErr = err

			// Check if this is a connection error
			if isConnectionError(err) {
				l.circuitBreaker.recordFailure()

				if l.circuitBreaker.isOpen() && l.allowDegradedMode {
					zerolog.Ctx(ctx).Warn().
						Err(err).
						Str("key", key).
						Msg("database connection failed, switching to degraded mode")

					lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-postgres", lock.LockFailureCircuitBreaker)

					return l.fallbackLocker.Lock(ctx, key, ttl)
				}
			}

			continue
		}

		// Success!
		l.circuitBreaker.recordSuccess()

		// Record metrics
		lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, "distributed-postgres", lock.LockResultSuccess)
		l.acquisitionTimes.Store(key, time.Now())

		zerolog.Ctx(ctx).Debug().
			Str("key", key).
			Int64("lock_id", lockID).
			Dur("ttl", ttl).
			Int("attempts", attempt+1).
			Msg("acquired PostgreSQL advisory lock")

		return nil
	}

	// All retries exhausted
	lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-postgres", lock.LockFailureMaxRetries)

	return fmt.Errorf("%w: key=%s after %d attempts: %w",
		ErrLockAcquisitionFailed, key, l.retryConfig.MaxAttempts, lastErr)
}

// Unlock releases an exclusive lock.
func (l *Locker) Unlock(ctx context.Context, key string) error {
	// Record lock duration
	if val, ok := l.acquisitionTimes.LoadAndDelete(key); ok {
		if startTime, ok := val.(time.Time); ok {
			duration := time.Since(startTime).Seconds()
			lock.RecordLockDuration(ctx, lock.LockTypeExclusive, "distributed-postgres", duration)
		}
	}

	// Check if we're in degraded mode
	if l.circuitBreaker.isOpen() && l.allowDegradedMode {
		return l.fallbackLocker.Unlock(ctx, key)
	}

	lockID := l.hashKey(key)

	// Get the dedicated connection for this lock
	l.connMu.Lock()
	conn, ok := l.connections[key]
	l.connMu.Unlock()

	if !ok {
		// This can happen if Lock failed but Unlock is still called
		return nil
	}

	// Release the lock
	var unlockResult bool

	err := conn.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1)", lockID).Scan(&unlockResult)

	// Always release the connection, even if unlock failed
	l.releaseConn(key)

	if err != nil {
		// Don't fail here - just log the error
		zerolog.Ctx(ctx).Warn().
			Err(err).
			Str("key", key).
			Int64("lock_id", lockID).
			Msg("failed to release PostgreSQL advisory lock")

		return nil
	}

	if !unlockResult {
		zerolog.Ctx(ctx).Warn().
			Str("key", key).
			Int64("lock_id", lockID).
			Msg("advisory lock was not held during unlock")
	}

	zerolog.Ctx(ctx).Debug().
		Str("key", key).
		Int64("lock_id", lockID).
		Msg("released PostgreSQL advisory lock")

	return nil
}

// TryLock attempts to acquire an exclusive lock without retries.
func (l *Locker) TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	// Check circuit breaker
	if l.circuitBreaker.isOpen() {
		lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-postgres", lock.LockFailureCircuitBreaker)

		if l.allowDegradedMode {
			return l.fallbackLocker.TryLock(ctx, key, ttl)
		}

		return false, ErrCircuitBreakerOpen
	}

	lockID := l.hashKey(key)

	// Get or create a dedicated connection for this lock
	conn, err := l.getOrCreateConn(ctx, key)
	if err != nil {
		l.circuitBreaker.recordFailure()

		if l.circuitBreaker.isOpen() && l.allowDegradedMode {
			lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-postgres", lock.LockFailureCircuitBreaker)

			return l.fallbackLocker.TryLock(ctx, key, ttl)
		}

		return false, fmt.Errorf("failed to get database connection: %w", err)
	}

	// Try to acquire the lock using pg_try_advisory_lock (non-blocking)
	var lockResult bool

	err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&lockResult)
	if err != nil {
		// Clean up connection on error
		l.releaseConn(key)

		if isConnectionError(err) {
			l.circuitBreaker.recordFailure()

			if l.circuitBreaker.isOpen() && l.allowDegradedMode {
				lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-postgres", lock.LockFailureCircuitBreaker)

				return l.fallbackLocker.TryLock(ctx, key, ttl)
			}
		}

		lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-postgres", "database_error")

		return false, fmt.Errorf("error trying to acquire lock: %w", err)
	}

	if !lockResult {
		// Lock is held by someone else
		l.releaseConn(key)
		lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, "distributed-postgres", lock.LockResultContention)

		return false, nil
	}

	// Success!
	l.circuitBreaker.recordSuccess()

	// Record metrics
	lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, "distributed-postgres", lock.LockResultSuccess)
	l.acquisitionTimes.Store(key, time.Now())

	return true, nil
}

// calculateBackoff calculates the backoff delay with exponential backoff and optional jitter.
func (l *Locker) calculateBackoff(attempt int) time.Duration {
	// Exponential backoff: initialDelay * 2^attempt
	delay := float64(l.retryConfig.InitialDelay) * math.Pow(2, float64(attempt))

	// Cap at max delay
	if delay > float64(l.retryConfig.MaxDelay) {
		delay = float64(l.retryConfig.MaxDelay)
	}

	// Add jitter to prevent thundering herd
	if l.retryConfig.Jitter {
		jitter := mathrand.Float64() * delay * jitterFactor //nolint:gosec // jitter doesn't need crypto randomness
		delay += jitter
	}

	return time.Duration(delay)
}

// isConnectionError checks if an error is a database connection error.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	// Check for common connection errors
	return errors.Is(err, sql.ErrConnDone) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled)
}
