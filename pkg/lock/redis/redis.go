// Package redis provides distributed lock implementations using Redis.
//
// This package implements the lock.Locker and lock.RWLocker interfaces using
// Redis as the backend. It uses the Redlock algorithm for exclusive locks and
// Redis sets for read-write locks.
//
// Features:
//   - Redlock algorithm for distributed exclusive locks
//   - Retry with exponential backoff and jitter
//   - Circuit breaker for Redis health monitoring
//   - Optional degraded mode (fallback to local locks)
//   - Comprehensive error handling and logging
package redis

import (
	"errors"
)

// Errors returned by Redis lock operations.
var (
	ErrNoRedisAddrs            = errors.New("at least one Redis address is required")
	ErrInsufficientNodesQuorum = errors.New("insufficient Redis nodes for quorum")
	ErrCircuitBreakerOpen      = errors.New("circuit breaker open: Redis is unavailable")
	ErrWriteLockHeld           = errors.New("write lock already held")
	ErrReadersTimeout          = errors.New("timeout waiting for readers to finish")
	ErrWriteLockTimeout        = errors.New("timeout waiting for write lock to clear")
)

// Circuit breaker states.
const (
	stateOpen   = "open"
	stateClosed = "closed"
)

// Config holds Redis configuration for distributed locking.
type Config struct {
	// Addrs is a list of Redis server addresses.
	// For single node: ["localhost:6379"]
	// For cluster: ["node1:6379", "node2:6379", "node3:6379"]
	Addrs []string

	// Username for authentication (optional, required for Redis ACL).
	Username string

	// Password for authentication (optional).
	Password string

	// DB is the Redis database number (0-15).
	DB int

	// UseTLS enables TLS connection.
	UseTLS bool

	// PoolSize is the maximum number of socket connections.
	PoolSize int

	// KeyPrefix for all distributed lock keys.
	KeyPrefix string
}
