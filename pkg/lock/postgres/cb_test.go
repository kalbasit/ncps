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

	// Test timing out
	// Mock time to be just before timeout
	initialTime := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)

	// Set up mock time - save restore function
	restoreOriginal := postgres.MockTimeNow(initialTime)
	defer restoreOriginal()

	// State: timeNow returns initialTime.

	// Need one more failure to open (threshold is 3)
	// We already have 2 failures.
	cb.RecordFailure()
	assert.True(t, cb.IsOpen(), "Circuit breaker should be open after threshold failures")

	// Advance time to just after timeout
	// Restore first to avoid stacking, then mock new time
	restoreOriginal()
	// Make a new restore point (though it just restores real TimeNow, which we will override again anyway, but cleaner)
	restore1 := postgres.MockTimeNow(initialTime.Add(timeout + 1*time.Millisecond))

	// Check half-open state
	assert.False(t, cb.IsOpen(), "Circuit breaker should be half-open (closed state for one request) after timeout")

	// A single failure in half-open state should immediately re-open circuit
	// openedAt becomes (initialTime + timeout + 1ms)
	cb.RecordFailure()
	assert.True(t, cb.IsOpen(), "Circuit breaker should re-open immediately on failure in half-open state")

	// Advance time again to reopen
	// We need to be at (initialTime + timeout + 1ms + timeout + 1ms) = initial + 2*timeout + 2ms
	restore1()

	restore2 := postgres.MockTimeNow(initialTime.Add(2*timeout + 10*time.Millisecond))

	assert.False(t, cb.IsOpen(), "Circuit breaker should be half-open after second timeout")

	// A single success in half-open state should close circuit and reset failures
	cb.RecordSuccess()
	assert.False(t, cb.IsOpen(), "Circuit breaker should be closed after success")

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

	restore2()
	// No need to defer restoreOriginal anymore as we called it manually, but calling it twice is safe
	// (it just sets timeNow = original, which is idempotent if correct)
	// Actually, restoreOriginal sets `timeNow` to what it was at start (real functions).
	// restore1 sets `timeNow` to what it was when restore1 created (real functions).
	// So calling restore2() puts real functions back.
}
