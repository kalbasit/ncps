package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	mathrand "math/rand"

	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/local"
)

// RWLocker implements lock.RWLocker using Redis sets for readers.
type RWLocker struct {
	client            redis.UniversalClient // Supports both single-node and cluster
	keyPrefix         string
	retryConfig       lock.RetryConfig
	allowDegradedMode bool

	// readerID stores the unique reader ID for this locker instance
	readerIDMu sync.Mutex
	readerID   string

	// fallbackLocker is used when Redis is unavailable and degraded mode is enabled
	fallbackLocker lock.RWLocker

	// circuitBreaker tracks Redis health
	circuitBreaker *circuitBreaker

	// Track lock acquisition times for duration metrics (write locks only)
	// Note: Read lock duration tracking is not supported due to concurrent access
	writeAcquisitionTimes sync.Map
}

// NewRWLocker creates a new Redis-based read-write locker.
func NewRWLocker(
	ctx context.Context,
	cfg Config,
	retryCfg lock.RetryConfig,
	allowDegradedMode bool,
) (lock.RWLocker, error) {
	if len(cfg.Addrs) == 0 {
		return nil, ErrNoRedisAddrs
	}

	var client redis.UniversalClient

	// If multiple addresses are provided, use cluster client for HA
	if len(cfg.Addrs) > 1 {
		client = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    cfg.Addrs,
			Username: cfg.Username,
			Password: cfg.Password,
			PoolSize: cfg.PoolSize,
		})
	} else {
		client = redis.NewClient(&redis.Options{
			Addr:     cfg.Addrs[0],
			Username: cfg.Username,
			Password: cfg.Password,
			DB:       cfg.DB,
			PoolSize: cfg.PoolSize,
		})
	}

	// Test connection
	if err := client.Ping(ctx).Err(); err != nil {
		if allowDegradedMode {
			zerolog.Ctx(ctx).Warn().
				Err(err).
				Msg("Redis unavailable, running in degraded mode with local locks")

			return local.NewRWLocker(), nil
		}

		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "ncps:lock:"
	}

	mode := "single-node"
	if len(cfg.Addrs) > 1 {
		mode = "cluster"
	}

	zerolog.Ctx(ctx).Info().
		Str("mode", mode).
		Int("nodes", len(cfg.Addrs)).
		Msg("connected to Redis for read-write locking")

	return &RWLocker{
		client:            client,
		keyPrefix:         cfg.KeyPrefix,
		retryConfig:       retryCfg,
		allowDegradedMode: allowDegradedMode,
		fallbackLocker:    local.NewRWLocker(),
		circuitBreaker:    newCircuitBreaker(5, 1*time.Minute),
	}, nil
}

// Lock acquires an exclusive write lock with retry and exponential backoff.
func (rw *RWLocker) Lock(ctx context.Context, key string, ttl time.Duration) error {
	// Check circuit breaker
	if rw.circuitBreaker.isOpen() {
		lock.RecordLockFailure(ctx, lock.LockTypeWrite, lock.LockModeDistributed, lock.LockFailureCircuitBreaker)

		if rw.allowDegradedMode {
			return rw.fallbackLocker.Lock(ctx, key, ttl)
		}

		return ErrCircuitBreakerOpen
	}

	// Use hash tags to ensure keys land on same cluster node
	writerKey := fmt.Sprintf("%s{%s}:writer", rw.keyPrefix, key)
	readersKey := fmt.Sprintf("%s{%s}:readers", rw.keyPrefix, key)

	var lastErr error

	for attempt := 0; attempt < rw.retryConfig.MaxAttempts; attempt++ {
		if attempt > 0 {
			// Record retry attempt for metrics
			lock.RecordLockRetryAttempt(ctx, lock.LockTypeWrite)

			// Calculate backoff delay
			delay := rw.calculateBackoff(attempt)

			select {
			case <-ctx.Done():
				lock.RecordLockFailure(ctx, lock.LockTypeWrite, lock.LockModeDistributed, lock.LockFailureContextCanceled)

				return ctx.Err()
			case <-time.After(delay):
			}
		}

		// Try to acquire writer lock
		success, err := rw.client.SetNX(ctx, writerKey, "1", ttl).Result()
		if err != nil {
			lastErr = err

			if isConnectionError(err) {
				rw.circuitBreaker.recordFailure()

				if rw.circuitBreaker.isOpen() && rw.allowDegradedMode {
					lock.RecordLockFailure(ctx, lock.LockTypeWrite, lock.LockModeDistributed, lock.LockFailureCircuitBreaker)

					return rw.fallbackLocker.Lock(ctx, key, ttl)
				}
			}

			// Connection error, retry
			continue
		}

		if !success {
			// Lock is held by someone else, retry
			lastErr = ErrWriteLockHeld

			continue
		}

		// Wait for all readers to finish
		deadline := time.Now().Add(ttl)

		for {
			// Get all readers and their expiration times (stored as hash)
			readers, err := rw.client.HGetAll(ctx, readersKey).Result()
			if err != nil {
				rw.client.Del(ctx, writerKey) // Clean up

				lastErr = fmt.Errorf("error checking readers: %w", err)

				break
			}

			// Count active (non-expired) readers
			now := time.Now().Unix()
			activeReaders := 0

			for readerID, expiresAtStr := range readers {
				expiresAt, err := time.Parse(time.RFC3339, expiresAtStr)
				if err != nil {
					// Invalid expiration, consider it expired
					if err := rw.client.HDel(ctx, readersKey, readerID).Err(); err != nil {
						zerolog.Ctx(ctx).
							Warn().
							Err(err).
							Str("key", key).
							Str("readerID", readerID).
							Msg("failed to remove invalid reader from lock")
					}

					continue
				}

				if expiresAt.Unix() > now {
					activeReaders++
				} else {
					// Remove expired reader
					rw.client.HDel(ctx, readersKey, readerID)
				}
			}

			if activeReaders == 0 {
				// Success!
				rw.circuitBreaker.recordSuccess()

				// Record successful acquisition
				lock.RecordLockAcquisition(ctx, lock.LockTypeWrite, lock.LockModeDistributed, lock.LockResultSuccess)
				rw.writeAcquisitionTimes.Store(key, time.Now())

				return nil
			}

			if time.Now().After(deadline) {
				rw.client.Del(ctx, writerKey) // Clean up

				lastErr = ErrReadersTimeout

				break
			}

			select {
			case <-ctx.Done():
				rw.client.Del(ctx, writerKey) // Clean up

				lock.RecordLockFailure(ctx, lock.LockTypeWrite, lock.LockModeDistributed, lock.LockFailureContextCanceled)

				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}

	// All retries exhausted
	lock.RecordLockFailure(ctx, lock.LockTypeWrite, lock.LockModeDistributed, lock.LockFailureMaxRetries)

	return fmt.Errorf("failed to acquire write lock after %d attempts: %w",
		rw.retryConfig.MaxAttempts, lastErr)
}

// calculateBackoff calculates the backoff delay with exponential backoff and optional jitter.
func (rw *RWLocker) calculateBackoff(attempt int) time.Duration {
	// Exponential backoff: initialDelay * 2^attempt
	delay := float64(rw.retryConfig.InitialDelay) * math.Pow(2, float64(attempt))

	// Cap at max delay
	if delay > float64(rw.retryConfig.MaxDelay) {
		delay = float64(rw.retryConfig.MaxDelay)
	}

	// Add jitter to prevent thundering herd
	if rw.retryConfig.Jitter {
		factor := rw.retryConfig.JitterFactor
		if factor <= 0 {
			factor = 0.5
		}

		jitter := mathrand.Float64() * delay * factor //nolint:gosec // jitter doesn't need crypto randomness
		delay += jitter
	}

	return time.Duration(delay)
}

// Unlock releases an exclusive write lock.
func (rw *RWLocker) Unlock(ctx context.Context, key string) error {
	// Record lock duration
	if val, ok := rw.writeAcquisitionTimes.LoadAndDelete(key); ok {
		if startTime, ok := val.(time.Time); ok {
			duration := time.Since(startTime).Seconds()
			lock.RecordLockDuration(ctx, lock.LockTypeWrite, lock.LockModeDistributed, duration)
		}
	}

	if rw.circuitBreaker.isOpen() && rw.allowDegradedMode {
		return rw.fallbackLocker.Unlock(ctx, key)
	}

	// Use hash tag to ensure key lands on same cluster node
	writerKey := fmt.Sprintf("%s{%s}:writer", rw.keyPrefix, key)

	return rw.client.Del(ctx, writerKey).Err()
}

// TryLock attempts to acquire an exclusive write lock without blocking.
func (rw *RWLocker) TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if rw.circuitBreaker.isOpen() {
		lock.RecordLockFailure(ctx, lock.LockTypeWrite, lock.LockModeDistributed, lock.LockFailureCircuitBreaker)

		if rw.allowDegradedMode {
			return rw.fallbackLocker.TryLock(ctx, key, ttl)
		}

		return false, ErrCircuitBreakerOpen
	}

	// Use hash tags to ensure keys land on same cluster node
	writerKey := fmt.Sprintf("%s{%s}:writer", rw.keyPrefix, key)
	readersKey := fmt.Sprintf("%s{%s}:readers", rw.keyPrefix, key)

	// Try to acquire writer lock
	success, err := rw.client.SetNX(ctx, writerKey, "1", ttl).Result()
	if err != nil {
		if isConnectionError(err) {
			rw.circuitBreaker.recordFailure()

			if rw.circuitBreaker.isOpen() && rw.allowDegradedMode {
				lock.RecordLockFailure(ctx, lock.LockTypeWrite, lock.LockModeDistributed, lock.LockFailureCircuitBreaker)

				return rw.fallbackLocker.TryLock(ctx, key, ttl)
			}
		}

		lock.RecordLockFailure(ctx, lock.LockTypeWrite, lock.LockModeDistributed, lock.LockFailureRedisError)

		return false, fmt.Errorf("error trying write lock: %w", err)
	}

	if !success {
		lock.RecordLockAcquisition(ctx, lock.LockTypeWrite, lock.LockModeDistributed, lock.LockResultContention)

		return false, nil // Lock is held
	}

	// Check if there are any active readers
	readers, err := rw.client.HGetAll(ctx, readersKey).Result()
	if err != nil {
		rw.client.Del(ctx, writerKey) // Clean up

		lock.RecordLockFailure(ctx, lock.LockTypeWrite, lock.LockModeDistributed, lock.LockFailureRedisError)

		return false, fmt.Errorf("error checking readers: %w", err)
	}

	// Count active (non-expired) readers
	now := time.Now().Unix()
	activeReaders := 0

	for readerID, expiresAtStr := range readers {
		expiresAt, err := time.Parse(time.RFC3339, expiresAtStr)
		if err != nil {
			// Invalid expiration, consider it expired
			rw.client.HDel(ctx, readersKey, readerID)

			continue
		}

		if expiresAt.Unix() > now {
			activeReaders++
		} else {
			// Remove expired reader
			rw.client.HDel(ctx, readersKey, readerID)
		}
	}

	if activeReaders > 0 {
		rw.client.Del(ctx, writerKey) // Clean up, readers present

		lock.RecordLockAcquisition(ctx, lock.LockTypeWrite, lock.LockModeDistributed, lock.LockResultContention)

		return false, nil
	}

	rw.circuitBreaker.recordSuccess()

	// Record successful acquisition
	lock.RecordLockAcquisition(ctx, lock.LockTypeWrite, lock.LockModeDistributed, lock.LockResultSuccess)
	rw.writeAcquisitionTimes.Store(key, time.Now())

	return true, nil
}

// RLock acquires a shared read lock.
func (rw *RWLocker) RLock(ctx context.Context, key string, ttl time.Duration) error {
	if rw.circuitBreaker.isOpen() {
		lock.RecordLockFailure(ctx, lock.LockTypeRead, lock.LockModeDistributed, lock.LockFailureCircuitBreaker)

		if rw.allowDegradedMode {
			return rw.fallbackLocker.RLock(ctx, key, ttl)
		}

		return ErrCircuitBreakerOpen
	}

	// Use hash tags to ensure keys land on same cluster node
	lockKey := fmt.Sprintf("%s{%s}:readers", rw.keyPrefix, key)
	writerKey := fmt.Sprintf("%s{%s}:writer", rw.keyPrefix, key)

	// Generate unique reader ID
	readerID := rw.getOrCreateReaderID()

	// Wait for writer to finish (with timeout)
	deadline := time.Now().Add(ttl)

	for {
		exists, err := rw.client.Exists(ctx, writerKey).Result()
		if err != nil {
			if isConnectionError(err) {
				rw.circuitBreaker.recordFailure()

				if rw.circuitBreaker.isOpen() && rw.allowDegradedMode {
					lock.RecordLockFailure(ctx, lock.LockTypeRead, lock.LockModeDistributed, lock.LockFailureCircuitBreaker)

					return rw.fallbackLocker.RLock(ctx, key, ttl)
				}
			}

			lock.RecordLockFailure(ctx, lock.LockTypeRead, lock.LockModeDistributed, lock.LockFailureRedisError)

			return fmt.Errorf("error checking writer lock: %w", err)
		}

		if exists == 0 {
			break
		}

		if time.Now().After(deadline) {
			lock.RecordLockFailure(ctx, lock.LockTypeRead, lock.LockModeDistributed, lock.LockFailureTimeout)

			return ErrWriteLockTimeout
		}

		time.Sleep(10 * time.Millisecond)
	}

	// Add to reader hash with per-reader expiration timestamp
	expiresAt := time.Now().Add(ttl).Format(time.RFC3339)

	err := rw.client.HSet(ctx, lockKey, readerID, expiresAt).Err()
	if err != nil {
		lock.RecordLockFailure(ctx, "read", "distributed", "redis_error")

		return fmt.Errorf("error acquiring read lock: %w", err)
	}

	rw.circuitBreaker.recordSuccess()

	// Record successful acquisition
	lock.RecordLockAcquisition(ctx, lock.LockTypeRead, lock.LockModeDistributed, lock.LockResultSuccess)

	return nil
}

// RUnlock releases a shared read lock.
func (rw *RWLocker) RUnlock(ctx context.Context, key string) error {
	if rw.circuitBreaker.isOpen() && rw.allowDegradedMode {
		return rw.fallbackLocker.RUnlock(ctx, key)
	}

	// Use hash tag to ensure key lands on same cluster node
	lockKey := fmt.Sprintf("%s{%s}:readers", rw.keyPrefix, key)
	readerID := rw.getOrCreateReaderID()

	return rw.client.HDel(ctx, lockKey, readerID).Err()
}

// getOrCreateReaderID returns a unique reader ID for this locker instance.
func (rw *RWLocker) getOrCreateReaderID() string {
	rw.readerIDMu.Lock()
	defer rw.readerIDMu.Unlock()

	if rw.readerID == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b) // crypto/rand.Read always returns err == nil
		rw.readerID = hex.EncodeToString(b)
	}

	return rw.readerID
}

// circuitBreaker implements a simple circuit breaker for Redis health monitoring.
type circuitBreaker struct {
	mu               sync.RWMutex
	failureCount     int
	failureThreshold int
	resetTimeout     time.Duration
	lastFailure      time.Time
	state            string // "closed", "open"
}

func newCircuitBreaker(failureThreshold int, resetTimeout time.Duration) *circuitBreaker {
	return &circuitBreaker{
		failureThreshold: failureThreshold,
		resetTimeout:     resetTimeout,
		state:            stateClosed,
	}
}

func (cb *circuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++
	cb.lastFailure = time.Now()

	if cb.failureCount >= cb.failureThreshold {
		cb.state = stateOpen
	}
}

func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount = 0
	cb.state = stateClosed
}

func (cb *circuitBreaker) isOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == stateOpen && time.Since(cb.lastFailure) > cb.resetTimeout {
		// The timeout has passed, so we can transition back to closed.
		cb.state = stateClosed
		cb.failureCount = 0
	}

	return cb.state == stateOpen
}

// isConnectionError checks if an error is a connection error.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	// Check for common connection errors
	errStr := err.Error()

	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "no such host")
}

// isLockAlreadyTakenError checks if an error indicates the lock is already taken.
func isLockAlreadyTakenError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	return strings.Contains(errStr, "lock already taken") ||
		strings.Contains(errStr, "already taken")
}
