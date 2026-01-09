package lock

import "time"

// RetryConfig holds retry configuration for lock acquisition.
// This is used by both Redis and PostgreSQL distributed lock implementations.
type RetryConfig struct {
	// MaxAttempts is the maximum number of attempts to acquire a lock.
	MaxAttempts int

	// InitialDelay is the initial delay between retry attempts.
	InitialDelay time.Duration

	// MaxDelay is the maximum delay between retry attempts.
	// Exponential backoff will be capped at this value.
	MaxDelay time.Duration

	// Jitter enables random jitter in retry delays to prevent thundering herd.
	Jitter bool

	// JitterFactor is the maximum proportion of delay to add as random jitter.
	// Only used if Jitter is true. Defaults to 0.5 if not set.
	JitterFactor float64
}

// DefaultRetryConfig returns sensible default retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     2 * time.Second,
		Jitter:       true,
		JitterFactor: 0.5,
	}
}
