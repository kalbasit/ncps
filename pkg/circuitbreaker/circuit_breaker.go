package circuitbreaker

import (
	"sync"
	"time"
)

// timeNow allows mocking time.Now for testing purposes
//
//nolint:gochecknoglobals // This is used for testing purposes
var timeNow = time.Now

// SetTimeNow sets the time function for the package and returns a function to restore it.
// This is intended for testing purposes only.
func SetTimeNow(f func() time.Time) func() {
	original := timeNow
	timeNow = f
	return func() { timeNow = original }
}

const (
	// DefaultThreshold is the default number of consecutive failures before
	// the circuit breaker opens.
	DefaultThreshold = 5

	// DefaultTimeout is the default duration the circuit breaker stays open
	// before attempting to close again.
	DefaultTimeout = 1 * time.Minute
)

// CircuitBreaker implements a simple circuit breaker pattern for service health.
// It tracks consecutive failures and opens the circuit after a threshold is reached.
type CircuitBreaker struct {
	mu sync.Mutex

	failureCount int
	threshold    int
	timeout      time.Duration
	openedAt     time.Time
}

// New creates a new circuit breaker.
func New(threshold int, timeout time.Duration) *CircuitBreaker {
	if threshold <= 0 {
		threshold = DefaultThreshold
	}

	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	return &CircuitBreaker{
		threshold: threshold,
		timeout:   timeout,
	}
}

// RecordFailure increments the failure count.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++

	if cb.failureCount >= cb.threshold {
		cb.openedAt = timeNow()
	}
}

// RecordSuccess records a success, resetting the failure count and closing the circuit
// if it was open or half-open.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount = 0
	cb.openedAt = time.Time{}
}

// AllowRequest checks if the circuit breaker allows a request to go through.
// It handles the state transition from Open to Half-Open.
func (cb *CircuitBreaker) AllowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.openedAt.IsZero() {
		// Circuit is closed
		return true
	}

	if timeNow().Sub(cb.openedAt) >= cb.timeout {
		// Half-open state: allow one request through by resetting openedAt to current time.
		// This prevents a thundering herd - only one request is allowed through while
		// concurrent requests are blocked until the next timeout cycle.
		// The failure count is preserved. If the next attempt fails, RecordFailure()
		// will see that the threshold is still met and immediately re-open the circuit.
		// If it succeeds, RecordSuccess() will reset the failure count and close the circuit.
		cb.openedAt = timeNow()

		return true
	}

	return false
}

// IsOpen returns true if the circuit breaker is currently open.
func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.openedAt.IsZero() {
		return false
	}

	// Check if timeout has expired (half-open basically counts as open for status check usually,
	// checking strictly if we are in the "blocked" window)
	return timeNow().Sub(cb.openedAt) < cb.timeout
}

// ForceOpen forces the circuit breaker into an open state. This is useful for testing or degraded mode initialization.
func (cb *CircuitBreaker) ForceOpen() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount = cb.threshold
	cb.openedAt = timeNow()
}
