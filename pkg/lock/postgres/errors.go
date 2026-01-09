package postgres

import "errors"

var (
	// ErrNotPostgreSQL is returned when attempting to use PostgreSQL advisory locks
	// with a non-PostgreSQL database.
	ErrNotPostgreSQL = errors.New("database is not PostgreSQL; advisory locks are only supported on PostgreSQL")

	// ErrDatabaseConnectionFailed is returned when the database connection fails.
	ErrDatabaseConnectionFailed = errors.New("failed to connect to database")

	// ErrCircuitBreakerOpen is returned when the circuit breaker is open due to
	// repeated failures, preventing further lock operations.
	ErrCircuitBreakerOpen = errors.New("circuit breaker open: database is unavailable")

	// ErrLockAcquisitionFailed is returned when lock acquisition fails after all retries.
	ErrLockAcquisitionFailed = errors.New("failed to acquire lock after retries")

	// ErrLockContention is returned when a lock is already held by another process.
	ErrLockContention = errors.New("lock held by another process")

	// ErrNoDatabase is returned when no database connection is provided.
	ErrNoDatabase = errors.New("no database connection provided")
)
