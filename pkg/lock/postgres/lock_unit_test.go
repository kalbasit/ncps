package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/postgres"
)

func TestCalculateBackoff(t *testing.T) {
	t.Parallel()

	cfg := lock.RetryConfig{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Jitter:       false,
	}

	// Test case 1: Initial delay (attempt 1)
	// 100ms * 2^0 = 100ms
	d := postgres.CalculateBackoff(cfg, 1)
	assert.Equal(t, 100*time.Millisecond, d, "Attempt 1 should be initial delay")

	// Test case 2: Exponential backoff (attempt 2)
	// 100ms * 2^1 = 200ms
	d = postgres.CalculateBackoff(cfg, 2)
	assert.Equal(t, 200*time.Millisecond, d, "Attempt 2 should be 2x initial delay")

	// Test case 3: Exponential backoff (attempt 3)
	// 100ms * 2^2 = 400ms
	d = postgres.CalculateBackoff(cfg, 3)
	assert.Equal(t, 400*time.Millisecond, d, "Attempt 3 should be 4x initial delay")

	// Test case 4: Max delay capping
	// 100ms * 2^10 > 1s
	d = postgres.CalculateBackoff(cfg, 10)
	assert.Equal(t, 1*time.Second, d, "Should be capped at MaxDelay")

	// Test case 5: Jitter
	cfgJitter := cfg
	cfgJitter.Jitter = true
	// We can't predict exact value but it should be >= base delay and <= base delay * (1+jitterFactor)
	d = postgres.CalculateBackoff(cfgJitter, 1)
	assert.GreaterOrEqual(t, d, 100*time.Millisecond, "With jitter, delay should be at least base delay")
	// jitterFactor is 0.5, so max is 1.5x
	assert.LessOrEqual(t, d, time.Duration(float64(100*time.Millisecond)*1.6),
		"With jitter, delay should be within reasonable bounds")
}

func TestRWLocker_Interface(t *testing.T) {
	t.Parallel()

	var _ lock.RWLocker = (*postgres.RWLocker)(nil)
}
