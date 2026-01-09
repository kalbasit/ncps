package postgres

import (
	"database/sql"
	"time"

	"github.com/kalbasit/ncps/pkg/lock"
)

// Export internal identifiers for testing.
func NewCircuitBreaker(threshold int, timeout time.Duration) *circuitBreaker {
	return newCircuitBreaker(threshold, timeout)
}

// IsOpen returns true if the circuit breaker is open.
// WARNING: When the circuit is in half-open state, calling IsOpen() consumes
// the single allowed request, which may cause the next call to report the
// circuit as open. This state change is not obvious from the function name.
// Consider using this only for assertions where this side effect is acceptable.
func (cb *circuitBreaker) IsOpen() bool   { return !cb.AllowRequest() }
func (cb *circuitBreaker) RecordFailure() { cb.recordFailure() }
func (cb *circuitBreaker) RecordSuccess() { cb.recordSuccess() }

// MockTimeNow allows mocking time.Now for testing purposes.
func MockTimeNow(t time.Time) func() {
	original := timeNow
	timeNow = func() time.Time { return t }

	return func() { timeNow = original }
}

func CalculateBackoff(cfg lock.RetryConfig, attempt int) time.Duration {
	return calculateBackoff(cfg, attempt)
}

// GetCircuitBreaker returns the circuit breaker from a Locker for testing.
func (l *Locker) GetCircuitBreaker() *circuitBreaker {
	return l.circuitBreaker
}

// GetDB returns the underlying sql.DB for testing.
func (l *Locker) GetDB() *sql.DB {
	return l.db
}

// GetAcquisitionTime returns the stored acquisition time for a key, for testing.
func (l *Locker) GetAcquisitionTime(key string) (time.Time, bool) {
	val, ok := l.acquisitionTimes.Load(key)
	if !ok {
		return time.Time{}, false
	}

	t, ok := val.(time.Time)

	return t, ok
}
