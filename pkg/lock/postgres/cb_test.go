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
	cb := postgres.NewCircuitBreaker(threshold, timeout)

	// Initial state: closed
	assert.False(t, cb.IsOpen())

	// Record successes shouldn't change anything
	cb.RecordSuccess()
	assert.False(t, cb.IsOpen())

	// Record failures below threshold
	cb.RecordFailure()
	cb.RecordFailure()
	assert.False(t, cb.IsOpen())

	initialTime := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("circuit opens after threshold failures", func(t *testing.T) {
		restore := postgres.MockTimeNow(initialTime)
		defer restore()

		// Need one more failure to open (threshold is 3)
		// We already have 2 failures.
		cb.RecordFailure()
		assert.True(t, cb.IsOpen(), "Circuit breaker should be open after threshold failures")
	})

	t.Run("half-open state after timeout", func(t *testing.T) {
		restore := postgres.MockTimeNow(initialTime.Add(timeout + 1*time.Millisecond))
		defer restore()

		// Check half-open state
		assert.False(t, cb.IsOpen(), "Circuit breaker should be half-open (closed state for one request) after timeout")

		// A single failure in half-open state should immediately re-open circuit
		cb.RecordFailure()
		assert.True(t, cb.IsOpen(), "Circuit breaker should re-open immediately on failure in half-open state")
	})

	t.Run("half-open state after second timeout", func(t *testing.T) {
		restore := postgres.MockTimeNow(initialTime.Add(2*timeout + 10*time.Millisecond))
		defer restore()

		assert.False(t, cb.IsOpen(), "Circuit breaker should be half-open after second timeout")

		// A single success in half-open state should close circuit and reset failures
		cb.RecordSuccess()
		assert.False(t, cb.IsOpen(), "Circuit breaker should be closed after success")
	})

	t.Run("failure count is reset after recovery", func(t *testing.T) {
		restore := postgres.MockTimeNow(initialTime.Add(2*timeout + 10*time.Millisecond))
		defer restore()

		// Verify failure count is reset
		// It should take threshold failures to open again.
		// Record threshold-1 failures (2 failures)
		for i := 0; i < threshold-1; i++ {
			cb.RecordFailure()
			assert.False(t, cb.IsOpen(), "Circuit breaker should stay closed until threshold reached again")
		}

		// Record last failure
		cb.RecordFailure()
		assert.True(t, cb.IsOpen(), "Circuit breaker should open after threshold reached again")
	})
}
