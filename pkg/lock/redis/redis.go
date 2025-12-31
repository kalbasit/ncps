// Package redis provides distributed lock implementations using Redis.
//
// This package implements the lock.Locker and lock.RWLocker interfaces using
// Redis as the backend. It uses the Redlock algorithm for exclusive locks and
// Redis sets for read-write locks.
//
// Features:
//   - Redlock algorithm for distributed exclusive locks
//   - Retry with exponential backoff and jitter
//   - Circuit breaker for Redis health monitoring
//   - Optional degraded mode (fallback to local locks)
//   - Comprehensive error handling and logging
package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/go-redsync/redsync/v4"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	redsyncredis "github.com/go-redsync/redsync/v4/redis"
	goredislib "github.com/go-redsync/redsync/v4/redis/goredis/v9"
	mathrand "math/rand"

	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/local"
)

// Errors returned by Redis lock operations.
var (
	ErrNoRedisAddrs            = errors.New("at least one Redis address is required")
	ErrInsufficientNodesQuorum = errors.New("insufficient Redis nodes for quorum")
	ErrCircuitBreakerOpen      = errors.New("circuit breaker open: Redis is unavailable")
	ErrWriteLockHeld           = errors.New("write lock already held")
	ErrReadersTimeout          = errors.New("timeout waiting for readers to finish")
	ErrWriteLockTimeout        = errors.New("timeout waiting for write lock to clear")
)

// Circuit breaker states.
const (
	stateOpen   = "open"
	stateClosed = "closed"
)

const jitterFactor = 0.1

// Config holds Redis configuration for distributed locking.
type Config struct {
	// Addrs is a list of Redis server addresses.
	// For single node: ["localhost:6379"]
	// For cluster: ["node1:6379", "node2:6379", "node3:6379"]
	Addrs []string

	// Username for authentication (optional, required for Redis ACL).
	Username string

	// Password for authentication (optional).
	Password string

	// DB is the Redis database number (0-15).
	DB int

	// UseTLS enables TLS connection.
	UseTLS bool

	// PoolSize is the maximum number of socket connections.
	PoolSize int

	// KeyPrefix for all distributed lock keys.
	KeyPrefix string
}

// RetryConfig configures retry behavior for lock acquisition.
type RetryConfig struct {
	// MaxAttempts is the maximum number of retry attempts.
	MaxAttempts int

	// InitialDelay is the initial retry delay.
	InitialDelay time.Duration

	// MaxDelay is the maximum retry delay (exponential backoff caps at this).
	MaxDelay time.Duration

	// Jitter enables adding random jitter to prevent thundering herd.
	Jitter bool
}

// Locker implements lock.Locker using Redis with Redlock algorithm.
type Locker struct {
	clients           []*redis.Client // All connected Redis clients for HA
	redsync           *redsync.Redsync
	keyPrefix         string
	retryConfig       RetryConfig
	allowDegradedMode bool

	// mutexes tracks acquired locks for cleanup
	mutexes map[string]*redsync.Mutex
	mu      sync.Mutex

	// fallbackLocker is used when Redis is unavailable and degraded mode is enabled
	fallbackLocker lock.Locker

	// circuitBreaker tracks Redis health
	circuitBreaker *circuitBreaker
}

// NewLocker creates a new Redis-based locker.
func NewLocker(ctx context.Context, cfg Config, retryCfg RetryConfig, allowDegradedMode bool) (lock.Locker, error) {
	if len(cfg.Addrs) == 0 {
		return nil, ErrNoRedisAddrs
	}

	// Connect to all Redis nodes for Redlock HA
	clients := make([]*redis.Client, 0, len(cfg.Addrs))
	pools := make([]redsyncredis.Pool, 0, len(cfg.Addrs))

	var firstErr error

	for _, addr := range cfg.Addrs {
		client := redis.NewClient(&redis.Options{
			Addr:     addr,
			Username: cfg.Username,
			Password: cfg.Password,
			DB:       cfg.DB,
			PoolSize: cfg.PoolSize,
		})

		// Test connection
		if err := client.Ping(ctx).Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}

			zerolog.Ctx(ctx).Warn().
				Err(err).
				Str("addr", addr).
				Msg("failed to connect to Redis node")

			continue
		}

		clients = append(clients, client)
		pools = append(pools, goredislib.NewPool(client))
	}

	// Check if we have a quorum (majority) of nodes
	quorum := len(cfg.Addrs)/2 + 1
	if len(pools) < quorum {
		// Close all connected clients
		for _, client := range clients {
			_ = client.Close()
		}

		if allowDegradedMode {
			zerolog.Ctx(ctx).Warn().
				Int("connected", len(pools)).
				Int("required", quorum).
				Msg("insufficient Redis nodes for quorum, running in degraded mode")

			return local.NewLocker(), nil
		}

		if firstErr != nil {
			return nil, fmt.Errorf("failed to connect to sufficient Redis nodes (%d/%d): %w",
				len(pools), quorum, firstErr)
		}

		return nil, fmt.Errorf("%w: %d/%d", ErrInsufficientNodesQuorum, len(pools), quorum)
	}

	rs := redsync.New(pools...)

	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "ncps:lock:"
	}

	zerolog.Ctx(ctx).Info().
		Int("connected_nodes", len(clients)).
		Int("total_nodes", len(cfg.Addrs)).
		Msg("connected to Redis nodes for distributed locking")

	return &Locker{
		clients:           clients,
		redsync:           rs,
		keyPrefix:         cfg.KeyPrefix,
		retryConfig:       retryCfg,
		allowDegradedMode: allowDegradedMode,
		mutexes:           make(map[string]*redsync.Mutex),
		fallbackLocker:    local.NewLocker(),
		circuitBreaker:    newCircuitBreaker(5, 1*time.Minute),
	}, nil
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

	lockKey := l.keyPrefix + key

	var lastErr error

	for attempt := 0; attempt < l.retryConfig.MaxAttempts; attempt++ {
		if attempt > 0 {
			// Calculate backoff delay
			delay := l.calculateBackoff(attempt)

			zerolog.Ctx(ctx).Debug().
				Str("key", key).
				Int("attempt", attempt+1).
				Dur("delay", delay).
				Msg("retrying lock acquisition after backoff")

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		mutex := l.redsync.NewMutex(
			lockKey,
			redsync.WithExpiry(ttl),
			redsync.WithTries(1), // We handle retries ourselves
		)

		if err := mutex.LockContext(ctx); err != nil {
			lastErr = err

			// Check if this is a connection error (circuit breaker)
			if isConnectionError(err) {
				l.circuitBreaker.recordFailure()

				if l.circuitBreaker.isOpen() && l.allowDegradedMode {
					zerolog.Ctx(ctx).Warn().
						Err(err).
						Str("key", key).
						Msg("Redis connection failed, switching to degraded mode")

					return l.fallbackLocker.Lock(ctx, key, ttl)
				}
			}

			if errors.Is(err, redsync.ErrFailed) {
				// Lock is held by someone else, retry
				continue
			}

			// Other error, fail immediately
			return fmt.Errorf("failed to acquire lock %s: %w", key, err)
		}

		// Success!
		l.mu.Lock()
		l.mutexes[key] = mutex
		l.mu.Unlock()

		l.circuitBreaker.recordSuccess()

		zerolog.Ctx(ctx).Debug().
			Str("key", key).
			Dur("ttl", ttl).
			Int("attempts", attempt+1).
			Msg("acquired distributed lock")

		return nil
	}

	return fmt.Errorf("failed to acquire lock %s after %d attempts: %w",
		key, l.retryConfig.MaxAttempts, lastErr)
}

// Unlock releases an exclusive lock.
func (l *Locker) Unlock(ctx context.Context, key string) error {
	// Check if we're in degraded mode
	if l.circuitBreaker.isOpen() && l.allowDegradedMode {
		return l.fallbackLocker.Unlock(ctx, key)
	}

	l.mu.Lock()
	mutex, ok := l.mutexes[key]
	delete(l.mutexes, key)
	l.mu.Unlock()

	if !ok {
		// This can happen if Lock failed but Unlock is still called
		return nil
	}

	if ok, err := mutex.UnlockContext(ctx); !ok || err != nil {
		// Don't fail here - lock will expire via TTL
		zerolog.Ctx(ctx).Warn().
			Err(err).
			Str("key", key).
			Msg("failed to release distributed lock (will expire via TTL)")

		return nil
	}

	zerolog.Ctx(ctx).Debug().
		Str("key", key).
		Msg("released distributed lock")

	return nil
}

// TryLock attempts to acquire an exclusive lock without retries.
func (l *Locker) TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	// Check circuit breaker
	if l.circuitBreaker.isOpen() {
		if l.allowDegradedMode {
			return l.fallbackLocker.TryLock(ctx, key, ttl)
		}

		return false, ErrCircuitBreakerOpen
	}

	lockKey := l.keyPrefix + key

	mutex := l.redsync.NewMutex(
		lockKey,
		redsync.WithExpiry(ttl),
		redsync.WithTries(1),
	)

	err := mutex.LockContext(ctx)
	if errors.Is(err, redsync.ErrFailed) {
		// Lock is held by someone else
		return false, nil
	}

	if err != nil {
		if isConnectionError(err) {
			l.circuitBreaker.recordFailure()

			if l.circuitBreaker.isOpen() && l.allowDegradedMode {
				return l.fallbackLocker.TryLock(ctx, key, ttl)
			}
		}

		return false, fmt.Errorf("error trying lock %s: %w", key, err)
	}

	// Success!
	l.mu.Lock()
	l.mutexes[key] = mutex
	l.mu.Unlock()

	l.circuitBreaker.recordSuccess()

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

// RWLocker implements lock.RWLocker using Redis sets for readers.
type RWLocker struct {
	client            redis.UniversalClient // Supports both single-node and cluster
	keyPrefix         string
	retryConfig       RetryConfig
	allowDegradedMode bool

	// readerID stores the unique reader ID for this locker instance
	readerIDMu sync.Mutex
	readerID   string

	// fallbackLocker is used when Redis is unavailable and degraded mode is enabled
	fallbackLocker lock.RWLocker

	// circuitBreaker tracks Redis health
	circuitBreaker *circuitBreaker
}

// NewRWLocker creates a new Redis-based read-write locker.
func NewRWLocker(ctx context.Context, cfg Config, retryCfg RetryConfig, allowDegradedMode bool) (lock.RWLocker, error) {
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
			// Calculate backoff delay
			delay := rw.calculateBackoff(attempt)

			select {
			case <-ctx.Done():
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

				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}

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
		jitter := mathrand.Float64() * delay * 0.1 //nolint:gosec // jitter doesn't need crypto randomness
		delay += jitter
	}

	return time.Duration(delay)
}

// Unlock releases an exclusive write lock.
func (rw *RWLocker) Unlock(ctx context.Context, key string) error {
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
				return rw.fallbackLocker.TryLock(ctx, key, ttl)
			}
		}

		return false, fmt.Errorf("error trying write lock: %w", err)
	}

	if !success {
		return false, nil // Lock is held
	}

	// Check if there are any active readers
	readers, err := rw.client.HGetAll(ctx, readersKey).Result()
	if err != nil {
		rw.client.Del(ctx, writerKey) // Clean up

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

		return false, nil
	}

	rw.circuitBreaker.recordSuccess()

	return true, nil
}

// RLock acquires a shared read lock.
func (rw *RWLocker) RLock(ctx context.Context, key string, ttl time.Duration) error {
	if rw.circuitBreaker.isOpen() {
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
					return rw.fallbackLocker.RLock(ctx, key, ttl)
				}
			}

			return fmt.Errorf("error checking writer lock: %w", err)
		}

		if exists == 0 {
			break
		}

		if time.Now().After(deadline) {
			return ErrWriteLockTimeout
		}

		time.Sleep(10 * time.Millisecond)
	}

	// Add to reader hash with per-reader expiration timestamp
	expiresAt := time.Now().Add(ttl).Format(time.RFC3339)

	err := rw.client.HSet(ctx, lockKey, readerID, expiresAt).Err()
	if err != nil {
		return fmt.Errorf("error acquiring read lock: %w", err)
	}

	rw.circuitBreaker.recordSuccess()

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
