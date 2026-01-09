package postgres

import (
	"sync"
	"time"
)

// timeNow allows mocking time.Now for testing purposes
//
//nolint:gochecknoglobals // This is used for testing purposes
var timeNow = time.Now

const (
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

	if cb.failureCount >= cb.threshold {
		cb.openedAt = timeNow()
	}
}

// recordSuccess resets the failure count and closes the circuit.
func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount = 0
	cb.openedAt = time.Time{}
}

// AllowRequest checks if the circuit breaker allows a request to go through.
// It handles the state transition from Open to Half-Open.
func (cb *circuitBreaker) AllowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.openedAt.IsZero() {
		// Circuit is closed
		return true
	}

	if timeNow().Sub(cb.openedAt) >= cb.timeout {
		// Half-open state: allow one request through by resetting the open timer.
		// The failure count is preserved. If the next attempt fails, recordFailure()
		// will see that the threshold is still met and immediately re-open the circuit.
		// If it succeeds, recordSuccess() will reset the failure count and close the circuit.
		cb.openedAt = time.Time{}

		return true
	}

	return false
}
