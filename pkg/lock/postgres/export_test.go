package postgres

import "time"

// Export internal identifiers for testing.
func NewCircuitBreaker(threshold int, timeout time.Duration) *circuitBreaker {
	return newCircuitBreaker(threshold, timeout)
}

func (cb *circuitBreaker) IsOpen() bool   { return !cb.AllowRequest() }
func (cb *circuitBreaker) RecordFailure() { cb.recordFailure() }
func (cb *circuitBreaker) RecordSuccess() { cb.recordSuccess() }

// MockTimeNow allows mocking time.Now for testing purposes.
func MockTimeNow(t time.Time) func() {
	original := timeNow
	timeNow = func() time.Time { return t }

	return func() { timeNow = original }
}

func CalculateBackoff(cfg RetryConfig, attempt int) time.Duration {
	return calculateBackoff(cfg, attempt)
}

// GetCircuitBreaker returns the circuit breaker from a Locker for testing.
func (l *Locker) GetCircuitBreaker() *circuitBreaker {
	return l.circuitBreaker
}
