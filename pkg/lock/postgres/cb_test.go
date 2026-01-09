package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kalbasit/ncps/pkg/lock/postgres"
)

func TestCircuitBreaker(t *testing.T) {
	t.Parallel()

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

	// Record failure at threshold should open circuit
	cb.RecordFailure()
	assert.True(t, cb.IsOpen())

	// Re-record failure doesn't change anything
	cb.RecordFailure()
	assert.True(t, cb.IsOpen())

	// Wait for timeout
	time.Sleep(timeout + 10*time.Millisecond)

	// Should be half-open (isOpen returns false but failureCount not reset)
	assert.False(t, cb.IsOpen())

	// A single failure in half-open state should immediately re-open circuit
	cb.RecordFailure()
	assert.True(t, cb.IsOpen())

	// Wait for timeout again
	time.Sleep(timeout + 10*time.Millisecond)
	assert.False(t, cb.IsOpen())

	// A single success in half-open state should close circuit and reset failures
	cb.RecordSuccess()
	assert.False(t, cb.IsOpen())

	// Should now require 'threshold' failures to open again
	cb.RecordFailure()
	cb.RecordFailure()
	assert.False(t, cb.IsOpen())
	cb.RecordFailure()
	assert.True(t, cb.IsOpen())
}
