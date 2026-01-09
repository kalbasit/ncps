package postgres

import "time"

// Export internal identifiers for testing.
func NewCircuitBreaker(threshold int, timeout time.Duration) *circuitBreaker {
	return newCircuitBreaker(threshold, timeout)
}

func (cb *circuitBreaker) IsOpen() bool   { return cb.isOpen() }
func (cb *circuitBreaker) RecordFailure() { cb.recordFailure() }
func (cb *circuitBreaker) RecordSuccess() { cb.recordSuccess() }

func (l *Locker) CalculateBackoff(attempt int) time.Duration {
	return l.calculateBackoff(attempt)
}
