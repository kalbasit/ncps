package upstream

import (
	"sync"
	"time"
)

// CircuitState represents the state of the circuit breaker
type CircuitState int

const (
	// CircuitClosed means the circuit is closed and requests can go through
	CircuitClosed CircuitState = iota
	// CircuitOpen means the circuit is open and requests are blocked
	CircuitOpen
	// CircuitHalfOpen means the circuit is half-open and testing if service is back
	CircuitHalfOpen
)

// CircuitBreakerConfig contains configuration for the circuit breaker
type CircuitBreakerConfig struct {
	// MaxFailures is the maximum number of failures before opening the circuit
	MaxFailures uint32
	// Timeout is how long to wait before trying to close the circuit
	Timeout time.Duration
	// ResetTimeout is how long to wait before resetting failure count
	ResetTimeout time.Duration
}

// CircuitBreaker implements a circuit breaker pattern for upstream failures
type CircuitBreaker struct {
	mu           sync.RWMutex
	state        CircuitState
	failures     uint32
	lastFailTime time.Time
	config       CircuitBreakerConfig
}

// NewCircuitBreaker creates a new circuit breaker with the given configuration
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	if config.MaxFailures == 0 {
		config.MaxFailures = 5
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.ResetTimeout == 0 {
		config.ResetTimeout = 5 * time.Minute
	}

	return &CircuitBreaker{
		state:  CircuitClosed,
		config: config,
	}
}

// IsOpen returns true if the circuit breaker is open
func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	return cb.state == CircuitOpen
}

// RecordSuccess records a successful operation
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	cb.state = CircuitClosed
}

// RecordFailure records a failed operation
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailTime = time.Now()

	if cb.failures >= cb.config.MaxFailures {
		cb.state = CircuitOpen
	}
}

// CanAttempt returns true if an attempt can be made
func (cb *CircuitBreaker) CanAttempt() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()

	switch cb.state {
	case CircuitClosed:
		// Reset failures if enough time has passed
		if now.Sub(cb.lastFailTime) > cb.config.ResetTimeout {
			cb.failures = 0
		}
		return true
	case CircuitOpen:
		// Check if we should move to half-open state
		if now.Sub(cb.lastFailTime) > cb.config.Timeout {
			cb.state = CircuitHalfOpen
			return true
		}
		return false
	case CircuitHalfOpen:
		// Allow one attempt in half-open state
		return true
	default:
		return false
	}
}

// GetState returns the current circuit state
func (cb *CircuitBreaker) GetState() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// GetFailures returns the current failure count
func (cb *CircuitBreaker) GetFailures() uint32 {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failures
}
