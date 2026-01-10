package lock

import (
	"math"
	"time"

	mathrand "math/rand"
)

// CalculateBackoff calculates the backoff duration based on retry config and attempt number.
// The attempt number is 0-indexed (first attempt is 0, first retry is 1).
func CalculateBackoff(cfg RetryConfig, attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}

	// Formula: InitialDelay * 2^(attempt-1)
	// For attempt 1 (first retry), delay is InitialDelay * 2^0 = InitialDelay
	// For attempt 2, delay is InitialDelay * 2^1 = 2 * InitialDelay
	delay := cfg.InitialDelay * time.Duration(math.Pow(2, float64(attempt-1)))

	// Cap at MaxDelay
	if delay > cfg.MaxDelay {
		delay = cfg.MaxDelay
	}

	// Apply jitter if enabled
	if cfg.Jitter {
		// Calculate jitter: rand(0, delay * JitterFactor)
		factor := cfg.GetJitterFactor()

		// Use the global math/rand which is safe for concurrent use.
		// This avoids creating a new source on every call.
		//nolint:gosec // G404: math/rand is acceptable for jitter, doesn't need crypto-grade randomness
		jitter := mathrand.Float64() * float64(delay) * factor
		delay += time.Duration(jitter)
	}

	return delay
}
