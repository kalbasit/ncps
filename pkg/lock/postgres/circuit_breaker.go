package postgres

import (
	"sync"
	"time"
)

const (
	// jitterFactor is the maximum proportion of delay to add as random jitter.
	jitterFactor = 0.5

	// defaultCircuitBreakerThreshold is the number of consecutive failures before
	// the circuit breaker opens.
	defaultCircuitBreakerThreshold = 5

	// defaultCircuitBreakerTimeout is how long the circuit breaker stays open
	// before attempting to close again.
	defaultCircuitBreakerTimeout = 1 * time.Minute
)

// circuitBreaker implements a simple circuit breaker pattern for database health.
// It tracks consecutive failures and opens the circuit after a threshold is reached.
type circuitBreaker struct {
	mu sync.Mutex

	failureCount int
	threshold    int
	timeout      time.Duration
	openedAt     time.Time
}

// newCircuitBreaker creates a new circuit breaker.
func newCircuitBreaker(threshold int, timeout time.Duration) *circuitBreaker {
	if threshold <= 0 {
		threshold = defaultCircuitBreakerThreshold
	}

	if timeout <= 0 {
		timeout = defaultCircuitBreakerTimeout
	}

	return &circuitBreaker{
		threshold: threshold,
		timeout:   timeout,
	}
}

// recordFailure increments the failure count.
func (cb *circuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++

	if cb.failureCount >= cb.threshold && cb.openedAt.IsZero() {
		cb.openedAt = time.Now()
	}
}

// recordSuccess resets the failure count and closes the circuit.
func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount = 0
	cb.openedAt = time.Time{}
}

// isOpen returns true if the circuit breaker is open and should reject requests.
func (cb *circuitBreaker) isOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.openedAt.IsZero() {
		return false
	}

	// Check if timeout has passed - if so, allow one request through
	if time.Since(cb.openedAt) >= cb.timeout {
		cb.openedAt = time.Time{} // Reset to allow retry
		cb.failureCount = 0

		return false
	}

	return true
}
