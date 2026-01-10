package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/circuitbreaker"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/postgres"
)

// Note: The failingQuerier approach doesn't work well with sql.DB type system.
// Instead, we'll use direct circuit breaker manipulation and test helpers.

func TestLocker_CircuitBreakerOpensAfterFailures(t *testing.T) {
	t.Parallel()

	skipIfPostgresNotAvailable(t)

	ctx := context.Background()

	querier, cleanup := getTestDatabase(t)
	defer cleanup()

	// Create a locker with a low circuit breaker threshold for testing
	cfg := getTestConfig()
	retryCfg := lock.RetryConfig{
		MaxAttempts:  2, // Low retry count for faster test
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     50 * time.Millisecond,
		Jitter:       false,
	}

	// Disable degraded mode to test circuit breaker error
	locker, err := postgres.NewLocker(ctx, querier, cfg, retryCfg, false)
	require.NoError(t, err)

	// Verify initial state
	pgLocker := locker

	cb := pgLocker.GetCircuitBreaker()
	assert.False(t, cb.IsOpen(), "Circuit breaker should be closed initially")

	// Now simulate failures by manually opening the circuit breaker
	// In a real scenario, this would happen due to database connection failures
	threshold := 5 // Default circuit breaker threshold

	for i := 0; i < threshold; i++ {
		cb.RecordFailure()
	}

	assert.True(t, cb.IsOpen(), "Circuit breaker should be open after threshold failures")

	// Try to acquire a lock - should fail with circuit breaker error
	key := getUniqueKey(t, "cb-test")
	err = locker.Lock(ctx, key, 1*time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, postgres.ErrCircuitBreakerOpen)
}

func TestLocker_DegradedModeFallback(t *testing.T) {
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

	// Enable degraded mode
	locker, err := postgres.NewLocker(ctx, querier, cfg, retryCfg, true)
	require.NoError(t, err)

	pgLocker := locker

	cb := pgLocker.GetCircuitBreaker()

	// Open the circuit breaker by recording failures
	threshold := 5
	for i := 0; i < threshold; i++ {
		cb.RecordFailure()
	}

	assert.True(t, cb.IsOpen(), "Circuit breaker should be open")

	// Try to acquire a lock - should succeed using fallback local locker
	key := getUniqueKey(t, "degraded-test")
	err = locker.Lock(ctx, key, 1*time.Second)
	require.NoError(t, err, "Lock should succeed in degraded mode")

	// Unlock should also work
	err = locker.Unlock(ctx, key)
	assert.NoError(t, err, "Unlock should succeed in degraded mode")
}

//nolint:paralleltest // Modifying global state for mocking
func TestLocker_CircuitBreakerRecovery(t *testing.T) {
	// Set up a shorter timeout for testing
	cbShort := circuitbreaker.New(3, 100*time.Millisecond)
	initialTime := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)

	restore := circuitbreaker.SetTimeNow(func() time.Time { return initialTime })
	defer restore()

	// Open the circuit breaker
	for i := 0; i < 3; i++ {
		cbShort.RecordFailure()
	}

	assert.True(t, cbShort.IsOpen(), "Circuit breaker should be open")

	// Advance time past timeout to allow half-open state
	restore()

	restore = circuitbreaker.SetTimeNow(func() time.Time { return initialTime.Add(150 * time.Millisecond) })
	defer restore()

	// Circuit should now be in half-open state (AllowRequest returns true for one request)
	assert.False(t, cbShort.IsOpen(), "Circuit breaker should be half-open")

	// Record a success to close the circuit
	cbShort.RecordSuccess()

	assert.False(t, cbShort.IsOpen(), "Circuit breaker should be closed after success")

	// Verify failure count is reset - should take 3 failures to open again
	cbShort.RecordFailure()
	assert.False(t, cbShort.IsOpen())
	cbShort.RecordFailure()
	assert.False(t, cbShort.IsOpen())
	cbShort.RecordFailure()
	assert.True(t, cbShort.IsOpen(), "Should open after threshold reached again")
}

func TestRWLocker_DegradedMode(t *testing.T) {
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

	// Enable degraded mode
	rwLocker, err := postgres.NewRWLocker(ctx, querier, cfg, retryCfg, true)
	require.NoError(t, err)

	// Access the embedded Locker to get the circuit breaker
	pgRWLocker, ok := rwLocker.(*postgres.RWLocker)
	require.True(t, ok, "Expected postgres.RWLocker")

	cb := pgRWLocker.GetCircuitBreaker()

	// Open the circuit breaker
	threshold := 5
	for i := 0; i < threshold; i++ {
		cb.RecordFailure()
	}

	assert.True(t, cb.IsOpen(), "Circuit breaker should be open")

	// Test RLock in degraded mode
	key := getUniqueKey(t, "rw-degraded-test")
	err = rwLocker.RLock(ctx, key, 1*time.Second)
	require.NoError(t, err, "RLock should succeed in degraded mode")

	err = rwLocker.RUnlock(ctx, key)
	require.NoError(t, err, "RUnlock should succeed in degraded mode")

	// Test Lock in degraded mode
	err = rwLocker.Lock(ctx, key, 1*time.Second)
	require.NoError(t, err, "Lock should succeed in degraded mode")

	err = rwLocker.Unlock(ctx, key)
	assert.NoError(t, err, "Unlock should succeed in degraded mode")
}

//nolint:paralleltest
func TestLocker_CircuitBreakerReopensOnFailure(t *testing.T) {
	// Note: Cannot run in parallel as it modifies global timeNow variable
	cbShort := circuitbreaker.New(3, 100*time.Millisecond)

	// Mock time for controlled testing
	initialTime := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)

	restore := circuitbreaker.SetTimeNow(func() time.Time { return initialTime })
	defer restore()

	// Open the circuit
	for i := 0; i < 3; i++ {
		cbShort.RecordFailure()
	}

	assert.True(t, cbShort.IsOpen(), "Circuit should be open")

	// Advance time past timeout
	restore()

	restore = circuitbreaker.SetTimeNow(func() time.Time { return initialTime.Add(150 * time.Millisecond) })
	defer restore()

	// Half-open state
	assert.False(t, cbShort.IsOpen(), "Circuit should be half-open")

	// Record a failure in half-open state - should immediately reopen
	cbShort.RecordFailure()
	assert.True(t, cbShort.IsOpen(), "Circuit should reopen immediately on failure in half-open")
}
