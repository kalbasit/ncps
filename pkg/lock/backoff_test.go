package lock_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kalbasit/ncps/pkg/lock"
)

func TestCalculateBackoff(t *testing.T) {
	t.Parallel()

	cfg := lock.RetryConfig{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Jitter:       false,
	}

	// Attempt 0: No backoff
	assert.Equal(t, time.Duration(0), lock.CalculateBackoff(cfg, 0))

	// Attempt 1: Initial delay (100ms * 2^0)
	assert.Equal(t, 100*time.Millisecond, lock.CalculateBackoff(cfg, 1))

	// Attempt 2: 200ms (100ms * 2^1)
	assert.Equal(t, 200*time.Millisecond, lock.CalculateBackoff(cfg, 2))

	// Attempt 3: 400ms (100ms * 2^2)
	assert.Equal(t, 400*time.Millisecond, lock.CalculateBackoff(cfg, 3))

	// Attempt 4: 800ms (100ms * 2^3)
	assert.Equal(t, 800*time.Millisecond, lock.CalculateBackoff(cfg, 4))

	// Attempt 5: Cap at MaxDelay (1s)
	assert.Equal(t, 1*time.Second, lock.CalculateBackoff(cfg, 5))
}

func TestCalculateBackoff_Jitter(t *testing.T) {
	t.Parallel()

	cfg := lock.RetryConfig{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Jitter:       true,
		JitterFactor: 0.5,
	}

	// With jitter, the delay should be between InitialDelay and InitialDelay * (1 + JitterFactor)
	// For attempt 1, it should be between 100ms and 150ms.
	for range 100 {
		delay := lock.CalculateBackoff(cfg, 1)
		assert.GreaterOrEqual(t, delay, 100*time.Millisecond)
		assert.LessOrEqual(t, delay, 150*time.Millisecond)
	}
}
