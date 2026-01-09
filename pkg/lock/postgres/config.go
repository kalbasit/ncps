package postgres

import "time"

// Config holds the configuration for PostgreSQL advisory locks.
type Config struct {
	// KeyPrefix is prepended to all lock keys for namespacing.
	// Defaults to "ncps:lock:" if empty.
	KeyPrefix string
}

// RetryConfig holds retry configuration for lock acquisition.
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
}

// DefaultRetryConfig returns sensible default retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     2 * time.Second,
		Jitter:       true,
	}
}
