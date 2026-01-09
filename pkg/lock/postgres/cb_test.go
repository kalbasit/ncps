package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kalbasit/ncps/pkg/lock/postgres"
)

//nolint:paralleltest // Modifying global state for mocking
func TestCircuitBreaker(t *testing.T) {
	threshold := 3
	timeout := 100 * time.Millisecond
	initialTime := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("initial state is closed", func(t *testing.T) {
		cb := postgres.NewCircuitBreaker(threshold, timeout)
		assert.False(t, cb.IsOpen())
	})

	t.Run("success doesn't change state", func(t *testing.T) {
		cb := postgres.NewCircuitBreaker(threshold, timeout)
		cb.RecordSuccess()
		assert.False(t, cb.IsOpen())
	})

	t.Run("failures below threshold keep circuit closed", func(t *testing.T) {
		cb := postgres.NewCircuitBreaker(threshold, timeout)
		cb.RecordFailure()
		cb.RecordFailure()
		assert.False(t, cb.IsOpen())
	})

	t.Run("circuit opens after threshold failures", func(t *testing.T) {
		restore := postgres.MockTimeNow(initialTime)
		defer restore()

		cb := postgres.NewCircuitBreaker(threshold, timeout)

		// Record threshold failures
		for i := 0; i < threshold; i++ {
			cb.RecordFailure()
		}

		assert.True(t, cb.IsOpen(), "Circuit breaker should be open after threshold failures")
	})

	t.Run("half-open state after timeout", func(t *testing.T) {
		restore := postgres.MockTimeNow(initialTime)
		defer restore()

		cb := postgres.NewCircuitBreaker(threshold, timeout)

		// Open the circuit
		for i := 0; i < threshold; i++ {
			cb.RecordFailure()
		}

		assert.True(t, cb.IsOpen())

		// Advance time past timeout
		restore()

		restore = postgres.MockTimeNow(initialTime.Add(timeout + 1*time.Millisecond))
		defer restore()

		// Check half-open state
		assert.False(t, cb.IsOpen(), "Circuit breaker should be half-open (closed state for one request) after timeout")

		// A single failure in half-open state should immediately re-open circuit
		cb.RecordFailure()
		assert.True(t, cb.IsOpen(), "Circuit breaker should re-open immediately on failure in half-open state")
	})

	t.Run("recovery after success in half-open state", func(t *testing.T) {
		restore := postgres.MockTimeNow(initialTime)
		defer restore()

		cb := postgres.NewCircuitBreaker(threshold, timeout)

		// Open the circuit
		for i := 0; i < threshold; i++ {
			cb.RecordFailure()
		}

		assert.True(t, cb.IsOpen())

		// Advance time past timeout to enter half-open state
		restore()

		restore = postgres.MockTimeNow(initialTime.Add(timeout + 1*time.Millisecond))
		defer restore()

		// Half-open: consume the allowed request
		_ = cb.IsOpen()

		// Record success to close circuit
		cb.RecordSuccess()
		assert.False(t, cb.IsOpen(), "Circuit breaker should be closed after success")
	})

	t.Run("failure count is reset after recovery", func(t *testing.T) {
		restore := postgres.MockTimeNow(initialTime)
		defer restore()

		cb := postgres.NewCircuitBreaker(threshold, timeout)

		// Open the circuit
		for i := 0; i < threshold; i++ {
			cb.RecordFailure()
		}

		assert.True(t, cb.IsOpen())

		// Advance time and recover
		restore()

		restore = postgres.MockTimeNow(initialTime.Add(timeout + 1*time.Millisecond))
		defer restore()

		// Half-open: consume the allowed request
		_ = cb.IsOpen()

		// Record success to close and reset
		cb.RecordSuccess()
		assert.False(t, cb.IsOpen())

		// Verify failure count is reset - should take threshold failures to open again
		for i := 0; i < threshold-1; i++ {
			cb.RecordFailure()
			assert.False(t, cb.IsOpen(), "Circuit breaker should stay closed until threshold reached again")
		}

		// Record last failure to reach threshold
		cb.RecordFailure()
		assert.True(t, cb.IsOpen(), "Circuit breaker should open after threshold reached again")
	})
}
