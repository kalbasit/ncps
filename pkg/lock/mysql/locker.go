package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net"
	"sync"
	"time"

	"github.com/rs/zerolog"

	mathrand "math/rand"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/local"
)

var (
	// ErrNoDatabase is returned when the database querier is not provided.
	ErrNoDatabase = errors.New("database querier is required")

	// ErrCircuitBreakerOpen is returned when the circuit breaker is open.
	ErrCircuitBreakerOpen = errors.New("circuit breaker open")

	// ErrLockContention is returned when the lock cannot be acquired due to contention.
	ErrLockContention = errors.New("lock contention")
)

// Locker implements lock.Locker using MySQL/MariaDB GET_LOCK function.
type Locker struct {
	db                *sql.DB
	keyPrefix         string
	retryConfig       lock.RetryConfig
	allowDegradedMode bool

	// connections tracks dedicated connections for each active lock
	connections map[string]*sql.Conn
	connMu      sync.Mutex

	// fallbackLocker is used when database is unavailable and degraded mode is enabled
	fallbackLocker lock.Locker

	// circuitBreaker tracks database health
	// We'll re-use the circuit breaker logic possibly, but we need to see if it's exported.
	// It seems it was unexported in postgres package. I should duplicate it or make it shared.
	// For now, I will implement a basic version or check if I can share it.
	// Looking at the file list, `circuit_breaker.go` is in `pkg/lock/postgres`.
	// I should probably move `circuit_breaker.go` to `pkg/lock` or duplicate it.
	// Given the task constraints, duplication is safer to avoid breaking postgres package right now,
	// but ideally it should be shared.
	// Actually, the user asked to implement `pkg/lock/mysql`.
	// I will duplicate the circuit breaker for now to avoid refactoring huge parts of postgres package unless necessary.
	circuitBreaker *circuitBreaker

	// Track lock acquisition times for duration metrics
	acquisitionTimes sync.Map
}

// Config holds configuration for the MySQL locker.
type Config struct {
	// KeyPrefix is the prefix for all lock keys.
	// If empty, defaults to "ncps:lock:".
	KeyPrefix string
}

// NewLocker creates a new MySQL/MariaDB advisory lock-based locker.
func NewLocker(
	ctx context.Context,
	querier database.Querier,
	cfg Config,
	retryCfg lock.RetryConfig,
	allowDegradedMode bool,
) (lock.Locker, error) {
	if querier == nil {
		return nil, ErrNoDatabase
	}

	// Get the underlying database connection
	db := querier.DB()

	// Test database connection with a simple advisory lock/unlock to verify MySQL
	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, err := db.Conn(testCtx)
	if err != nil {
		if allowDegradedMode {
			zerolog.Ctx(ctx).Warn().
				Err(err).
				Msg("failed to connect to MySQL, falling back to local locks (DEGRADED MODE)")

			return local.NewLocker(), nil
		}

		return nil, fmt.Errorf("failed to connect to MySQL: %w", err)
	}
	defer conn.Close()

	// Try a test advisory lock to verify functionality
	// MySQL uses GET_LOCK(str, timeout)
	// We use timeout 0 for immediate return
	var testLockResult int
	// In MySQL GET_LOCK returns 1 if success, 0 if timeout, NULL if error.
	// We scan into int.

	err = conn.QueryRowContext(testCtx, "SELECT GET_LOCK('ncps_test_lock', 0)").Scan(&testLockResult)
	if err != nil {
		if allowDegradedMode {
			zerolog.Ctx(ctx).Warn().
				Err(err).
				Msg("MySQL advisory locks not available, falling back to local locks (DEGRADED MODE)")

			return local.NewLocker(), nil
		}

		return nil, fmt.Errorf("MySQL advisory locks not available: %w", err)
	}

	// Unlock the test lock
	if testLockResult == 1 {
		_, _ = conn.ExecContext(testCtx, "SELECT RELEASE_LOCK('ncps_test_lock')")
	}

	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "ncps:lock:"
	}

	zerolog.Ctx(ctx).Info().
		Msg("connected to MySQL for distributed locking using GET_LOCK")

	return &Locker{
		db:                db,
		keyPrefix:         cfg.KeyPrefix,
		retryConfig:       retryCfg,
		allowDegradedMode: allowDegradedMode,
		connections:       make(map[string]*sql.Conn),
		fallbackLocker:    local.NewLocker(),
		circuitBreaker:    newCircuitBreaker(5, 30*time.Second), // Hardcoded defaults for now
	}, nil
}

// hashKey converts a string key to a 16-character hex string for use with MySQL GET_LOCK.
// GET_LOCK limit is 64 chars, but hashing ensures constant length and safety.
func (l *Locker) hashKey(key string) string {
	h := fnv.New64a()
	h.Write([]byte(l.keyPrefix + key))
	// Format as 16-char hex string
	return fmt.Sprintf("%016x", h.Sum64())
}

// Lock acquires an exclusive lock with retry and exponential backoff.
func (l *Locker) Lock(ctx context.Context, key string, ttl time.Duration) error {
	// Check circuit breaker
	if !l.circuitBreaker.AllowRequest() {
		if l.allowDegradedMode {
			zerolog.Ctx(ctx).Warn().
				Str("key", key).
				Msg("circuit breaker open, using fallback local lock (DEGRADED MODE)")

			return l.fallbackLocker.Lock(ctx, key, ttl)
		}

		return ErrCircuitBreakerOpen
	}

	lockName := l.hashKey(key)

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

			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-mysql", lock.LockFailureContextCanceled)

				return ctx.Err()
			case <-timer.C:
			}
		}

		// Create a new dedicated connection for this lock attempt
		conn, err := l.db.Conn(ctx)
		if err != nil {
			lastErr = err

			l.circuitBreaker.recordFailure()

			if !l.circuitBreaker.AllowRequest() && l.allowDegradedMode {
				zerolog.Ctx(ctx).Warn().
					Err(err).
					Str("key", key).
					Msg("database connection failed, switching to degraded mode")

				lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-mysql", lock.LockFailureCircuitBreaker)

				return l.fallbackLocker.Lock(ctx, key, ttl)
			}

			continue
		}

		// Try to acquire the lock using GET_LOCK(str, timeout)
		var lockResult int // 1 success, 0 timeout, NULL error

		err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", lockName).Scan(&lockResult)
		if err != nil {
			// Clean up connection on error
			_ = conn.Close()
			lastErr = err

			// Check if this is a connection error
			if isConnectionError(err) {
				l.circuitBreaker.recordFailure()

				if !l.circuitBreaker.AllowRequest() && l.allowDegradedMode {
					zerolog.Ctx(ctx).Warn().
						Err(err).
						Str("key", key).
						Msg("database connection failed, switching to degraded mode")

					lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-mysql", lock.LockFailureCircuitBreaker)

					return l.fallbackLocker.Lock(ctx, key, ttl)
				}
			}

			continue
		}

		if lockResult != 1 {
			// Lock is held by someone else, retry
			_ = conn.Close()

			// Treat as contention error for lastErr, but we will retry
			lastErr = ErrLockContention

			// We don't record failure here because we are retrying
			continue
		}

		// Success! Store the connection
		l.connMu.Lock()
		l.connections[key] = conn
		l.connMu.Unlock()

		l.circuitBreaker.recordSuccess()

		// Record metrics
		lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, "distributed-mysql", lock.LockResultSuccess)
		l.acquisitionTimes.Store(key, time.Now())

		zerolog.Ctx(ctx).Debug().
			Str("key", key).
			Str("lock_name", lockName).
			Dur("ttl", ttl).
			Int("attempts", attempt+1).
			Msg("acquired MySQL advisory lock")

		return nil
	}

	// All retries exhausted
	lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-mysql", lock.LockFailureMaxRetries)

	return fmt.Errorf("failed to acquire lock for key=%s after %d attempts: %w", key, l.retryConfig.MaxAttempts, lastErr)
}

// Unlock releases an exclusive lock.
func (l *Locker) Unlock(ctx context.Context, key string) error {
	// Record lock duration
	if val, ok := l.acquisitionTimes.LoadAndDelete(key); ok {
		if startTime, ok := val.(time.Time); ok {
			duration := time.Since(startTime).Seconds()
			lock.RecordLockDuration(ctx, lock.LockTypeExclusive, "distributed-mysql", duration)
		}
	}

	// Check if we're in degraded mode
	if !l.circuitBreaker.AllowRequest() && l.allowDegradedMode {
		return l.fallbackLocker.Unlock(ctx, key)
	}

	lockName := l.hashKey(key)

	// Atomically get and remove the dedicated connection for this lock
	l.connMu.Lock()

	conn, ok := l.connections[key]
	if ok {
		delete(l.connections, key)
	}

	l.connMu.Unlock()

	if !ok {
		// This can happen if Lock failed but Unlock is still called
		return nil
	}

	// Release the lock
	var unlockResult int // 1 released, 0 not released (not established by this thread), NULL name doesn't exist

	err := conn.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", lockName).Scan(&unlockResult)

	// Always close the connection, even if unlock failed
	_ = conn.Close()

	if err != nil {
		// Don't fail here - just log the error
		zerolog.Ctx(ctx).Warn().
			Err(err).
			Str("key", key).
			Str("lock_name", lockName).
			Msg("failed to release MySQL advisory lock")

		return nil
	}

	if unlockResult != 1 {
		zerolog.Ctx(ctx).Warn().
			Str("key", key).
			Str("lock_name", lockName).
			Msg("advisory lock was not held during unlock")
	}

	zerolog.Ctx(ctx).Debug().
		Str("key", key).
		Str("lock_name", lockName).
		Msg("released MySQL advisory lock")

	return nil
}

// TryLock attempts to acquire an exclusive lock without retries.
func (l *Locker) TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	// Check circuit breaker
	if !l.circuitBreaker.AllowRequest() {
		lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-mysql", lock.LockFailureCircuitBreaker)

		if l.allowDegradedMode {
			return l.fallbackLocker.TryLock(ctx, key, ttl)
		}

		return false, ErrCircuitBreakerOpen
	}

	lockName := l.hashKey(key)

	// Create a new dedicated connection for this lock attempt
	conn, err := l.db.Conn(ctx)
	if err != nil {
		l.circuitBreaker.recordFailure()

		if !l.circuitBreaker.AllowRequest() && l.allowDegradedMode {
			lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-mysql", lock.LockFailureCircuitBreaker)

			return l.fallbackLocker.TryLock(ctx, key, ttl)
		}

		return false, fmt.Errorf("failed to get database connection: %w", err)
	}

	// Try to acquire the lock using GET_LOCK(str, timeout)
	var lockResult int

	err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", lockName).Scan(&lockResult)
	if err != nil {
		// Clean up connection on error
		_ = conn.Close()

		if isConnectionError(err) {
			l.circuitBreaker.recordFailure()

			if !l.circuitBreaker.AllowRequest() && l.allowDegradedMode {
				lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-mysql", lock.LockFailureCircuitBreaker)

				return l.fallbackLocker.TryLock(ctx, key, ttl)
			}
		}

		lock.RecordLockFailure(ctx, lock.LockTypeExclusive, "distributed-mysql", "database_error")

		return false, fmt.Errorf("error trying to acquire lock: %w", err)
	}

	if lockResult != 1 {
		// Lock is held by someone else
		_ = conn.Close()

		lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, "distributed-mysql", lock.LockResultContention)

		return false, nil
	}

	// Success! Store the connection
	l.connMu.Lock()
	l.connections[key] = conn
	l.connMu.Unlock()

	l.circuitBreaker.recordSuccess()

	// Record metrics
	lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, "distributed-mysql", lock.LockResultSuccess)
	l.acquisitionTimes.Store(key, time.Now())

	return true, nil
}

// calculateBackoff calculates the backoff duration for a given attempt.
func (l *Locker) calculateBackoff(attempt int) time.Duration {
	return calculateBackoff(l.retryConfig, attempt)
}

// calculateBackoff calculates the backoff duration based on retry config and attempt number.
func calculateBackoff(cfg lock.RetryConfig, attempt int) time.Duration {
	// Formula: InitialDelay * 2^(attempt-1)
	delay := cfg.InitialDelay * time.Duration(math.Pow(2, float64(attempt-1)))

	// Cap at MaxDelay
	if delay > cfg.MaxDelay {
		delay = cfg.MaxDelay
	}

	// Apply jitter if enabled
	if cfg.Jitter {
		// Calculate jitter: rand(0, delay * JitterFactor)
		factor := cfg.GetJitterFactor()

		// Use the global math/rand which is safe for concurrent use.
		// This avoids creating a new source on every call.
		//nolint:gosec // G404: math/rand is acceptable for jitter, doesn't need crypto-grade randomness
		jitter := mathrand.Float64() * float64(delay) * factor
		delay += time.Duration(jitter)
	}

	return delay
}

// isConnectionError checks if an error is a database connection error.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	// Check for common context and sql errors that indicate a broken connection.
	if errors.Is(err, sql.ErrConnDone) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) {
		return true
	}

	// Check for underlying network errors.
	var netErr net.Error

	return errors.As(err, &netErr)
}

// circuitBreaker is a simple circuit breaker implementation.
// Duplicated from pkg/lock/postgres/circuit_breaker.go.
type circuitBreaker struct {
	mu           sync.RWMutex
	failures     int
	threshold    int
	lastFailure  time.Time
	resetTimeout time.Duration
	state        int // 0: Closed, 1: Open
}

const (
	stateClosed = 0
	stateOpen   = 1
)

func newCircuitBreaker(threshold int, resetTimeout time.Duration) *circuitBreaker {
	return &circuitBreaker{
		threshold:    threshold,
		resetTimeout: resetTimeout,
		state:        stateClosed,
	}
}

func (cb *circuitBreaker) AllowRequest() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if cb.state == stateOpen {
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			// Half-open state handled implicitly by allowing one request
			// In a real implementation we might want a proper half-open state
			return true
		}

		return false
	}

	return true
}

func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == stateOpen {
		cb.state = stateClosed
		cb.failures = 0
	} else {
		// Reset failures on success in closed state too?
		// A stricter implementation might require consecutive successes.
		// For now simple reset is fine.
		cb.failures = 0
	}
}

func (cb *circuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailure = time.Now()

	if cb.failures >= cb.threshold {
		cb.state = stateOpen
	}
}
