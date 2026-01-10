package mysql

import (
	"database/sql"
	"time"

	"github.com/kalbasit/ncps/pkg/circuitbreaker"
	"github.com/kalbasit/ncps/pkg/lock"
)

func CalculateBackoff(cfg lock.RetryConfig, attempt int) time.Duration {
	return lock.CalculateBackoff(cfg, attempt)
}

// GetCircuitBreaker returns the circuit breaker from a Locker for testing.
func (l *Locker) GetCircuitBreaker() *circuitbreaker.CircuitBreaker {
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
