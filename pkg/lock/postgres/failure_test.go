package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/postgres"
)

func TestLocker_CircuitBreaker_DBFailure(t *testing.T) {
	t.Parallel()

	skipIfPostgresNotAvailable(t)

	ctx := context.Background()

	querier, cleanup := getTestDatabase(t)
	defer cleanup()

	cfg := getTestConfig()
	retryCfg := lock.RetryConfig{
		MaxAttempts:  2,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     50 * time.Millisecond,
		Jitter:       false,
	}

	// Disable degraded mode to test circuit breaker error
	locker, err := postgres.NewLocker(ctx, querier, cfg, retryCfg, false)
	require.NoError(t, err)

	pgLocker := locker

	cb := pgLocker.GetCircuitBreaker()
	assert.False(t, cb.IsOpen(), "Circuit breaker should be closed initially")

	// Verify lock works initially
	key := getUniqueKey(t, "cb-failure-test")
	err = locker.Lock(ctx, key, 1*time.Second)
	require.NoError(t, err)
	err = locker.Unlock(ctx, key)
	require.NoError(t, err)

	// Close the underlying database connection to simulate failure
	db := pgLocker.GetDB()
	require.NoError(t, db.Close())

	// Try to acquire a lock - should fail and record failure in circuit breaker
	// We might need multiple attempts to trip the breaker depending on threshold
	// The default threshold is 5 (from NewLocker call in locker.go which uses newCircuitBreaker(5, ...))
	// So we need to fail 5 times.
	threshold := 5

	// Try to acquire a lock multiple times until circuit breaker opens
	// Since MaxAttempts > 1, each Lock call records multiple failures
	circuitOpened := false

	for i := 0; i < threshold*2; i++ {
		testKey := getUniqueKey(t, "cb-failure-attempt")
		err := locker.Lock(ctx, testKey, 1*time.Second)
		require.Error(t, err)

		if errors.Is(err, postgres.ErrCircuitBreakerOpen) {
			circuitOpened = true

			break
		}

		require.Contains(t, err.Error(), "database is closed")
	}

	assert.True(t, circuitOpened, "Circuit breaker should have opened")

	// Next attempt should return ErrCircuitBreakerOpen immediately
	err = locker.Lock(ctx, getUniqueKey(t, "cb-failure-final"), 1*time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, postgres.ErrCircuitBreakerOpen)
}
