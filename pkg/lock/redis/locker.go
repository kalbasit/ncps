package redis

import (
	"context"
	"errors"
	"fmt"
	"math"
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

	// Track lock acquisition times for duration metrics
	acquisitionTimes sync.Map
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
				lock.RecordLockFailure(ctx, lock.LockTypeExclusive, lock.LockModeDistributed, lock.LockFailureContextCanceled)

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

					lock.RecordLockFailure(ctx, lock.LockTypeExclusive, lock.LockModeDistributed, lock.LockFailureCircuitBreaker)

					return l.fallbackLocker.Lock(ctx, key, ttl)
				}
			}

			// Check if lock is already taken (normal failure, retry)
			if errors.Is(err, redsync.ErrFailed) || isLockAlreadyTakenError(err) {
				// Lock is held by someone else, retry
				continue
			}

			// Other error, fail immediately
			lock.RecordLockFailure(ctx, lock.LockTypeExclusive, lock.LockModeDistributed, lock.LockFailureRedisError)

			return fmt.Errorf("failed to acquire lock %s: %w", key, err)
		}

		// Success!
		l.mu.Lock()
		l.mutexes[key] = mutex
		l.mu.Unlock()

		l.circuitBreaker.recordSuccess()

		// Record metrics
		lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, lock.LockModeDistributed, lock.LockResultSuccess)
		l.acquisitionTimes.Store(key, time.Now())

		zerolog.Ctx(ctx).Debug().
			Str("key", key).
			Dur("ttl", ttl).
			Int("attempts", attempt+1).
			Msg("acquired distributed lock")

		return nil
	}

	// All retries exhausted
	lock.RecordLockFailure(ctx, lock.LockTypeExclusive, lock.LockModeDistributed, lock.LockFailureMaxRetries)

	return fmt.Errorf("failed to acquire lock %s after %d attempts: %w",
		key, l.retryConfig.MaxAttempts, lastErr)
}

// Unlock releases an exclusive lock.
func (l *Locker) Unlock(ctx context.Context, key string) error {
	// Record lock duration
	if val, ok := l.acquisitionTimes.LoadAndDelete(key); ok {
		if startTime, ok := val.(time.Time); ok {
			duration := time.Since(startTime).Seconds()
			lock.RecordLockDuration(ctx, lock.LockTypeExclusive, lock.LockModeDistributed, duration)
		}
	}

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
		lock.RecordLockFailure(ctx, lock.LockTypeExclusive, lock.LockModeDistributed, lock.LockFailureCircuitBreaker)

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
		lock.RecordLockAcquisition(ctx, lock.LockTypeExclusive, lock.LockModeDistributed, lock.LockResultContention)

		return false, nil
	}

	if err != nil {
		// Check if lock is already taken (normal failure condition)
		if errors.Is(err, redsync.ErrFailed) || isLockAlreadyTakenError(err) {
			// Lock is held by someone else
			return false, nil
		}

		if isConnectionError(err) {
			l.circuitBreaker.recordFailure()

			if l.circuitBreaker.isOpen() && l.allowDegradedMode {
				lock.RecordLockFailure(ctx, lock.LockTypeExclusive, lock.LockModeDistributed, lock.LockFailureCircuitBreaker)

				return l.fallbackLocker.TryLock(ctx, key, ttl)
			}
		}

		lock.RecordLockFailure(ctx, "exclusive", "distributed", "redis_error")

		return false, fmt.Errorf("error trying lock %s: %w", key, err)
	}

	// Success!
	l.mu.Lock()
	l.mutexes[key] = mutex
	l.mu.Unlock()

	l.circuitBreaker.recordSuccess()

	// Record metrics
	lock.RecordLockAcquisition(ctx, "exclusive", "distributed", "success")
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
