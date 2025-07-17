# Lazy Loading Implementation for Upstream Caches

## Overview

This implementation adds lazy loading support to the ncps (Nix Cache Proxy Server) to address the issue where ncps currently requires all upstream caches to be online and responding for it to boot successfully. The new implementation provides:

1. **Lazy Loading**: Upstream caches can be offline during startup
2. **Dynamic Management**: Upstreams are automatically added/removed based on health status
3. **Circuit Breaker**: Prevents excessive requests to failing upstreams
4. **Health Monitoring**: Continuous health checks with status notifications

## Key Changes

### 1. Upstream Cache Initialization (`pkg/cache/upstream/cache.go`)

- **Modified `New()` function** to initialize upstreams as unhealthy by default
- **Added circuit breaker** to each upstream cache for failure detection
- **Lazy health verification** during startup instead of requiring immediate connectivity

```go
// Initialize as unhealthy, will be marked healthy by health checker
c.isHealthy = false

// Initialize circuit breaker
c.circuitBreaker = NewCircuitBreaker(CircuitBreakerConfig{
    MaxFailures:  3,
    Timeout:      30 * time.Second,
    ResetTimeout: 2 * time.Minute,
})
```

### 2. Enhanced Health Checker (`pkg/cache/healthcheck/healthcheck.go`)

- **Added dynamic upstream management** with `AddUpstream()` and `RemoveUpstream()` methods
- **Health status notifications** via `HealthStatusChange` channel
- **Thread-safe operations** using mutex for concurrent access
- **Non-blocking notifications** to prevent deadlocks

```go
// HealthStatusChange represents a change in upstream health status
type HealthStatusChange struct {
    Upstream  *upstream.Cache
    IsHealthy bool
}
```

### 3. Circuit Breaker Implementation (`pkg/cache/upstream/circuit_breaker.go`)

- **State machine** with Closed, Open, and Half-Open states
- **Configurable failure thresholds** and timeout periods
- **Automatic recovery** attempts after timeout periods
- **Failure tracking** with exponential backoff

```go
type CircuitBreaker struct {
    state        CircuitState
    failures     uint32
    lastFailTime time.Time
    config       CircuitBreakerConfig
}
```

### 4. Main Cache Updates (`pkg/cache/cache.go`)

- **Modified `AddUpstreamCaches()`** to support lazy loading
- **Added health change processing** for dynamic upstream management
- **Updated `getHealthyUpstreams()`** to work with lazy loading
- **Enhanced logging** for upstream status changes

```go
// AddUpstreamCaches adds one or more upstream caches with lazy loading support.
func (c *Cache) AddUpstreamCaches(ctx context.Context, ucs ...*upstream.Cache) {
    c.upstreamCaches = append(c.upstreamCaches, ucs...)
    c.healthChecker = healthcheck.New(c.upstreamCaches)

    // Set up health change notifications for dynamic management
    healthChangeCh := make(chan healthcheck.HealthStatusChange, 100)
    c.healthChecker.SetHealthChangeNotifier(healthChangeCh)

    // Start the health checker
    c.healthChecker.Start(c.baseContext)

    // Start the health change processor
    go c.processHealthChanges(ctx, healthChangeCh)
}
```

## Benefits

### 1. **Improved Startup Reliability**
- ncps no longer fails to start if upstream caches are temporarily unavailable
- Graceful degradation when some upstreams are offline
- Faster startup times since health checks happen asynchronously

### 2. **Dynamic Upstream Management**
- Upstreams are automatically added to the active pool when they become healthy
- Unhealthy upstreams are removed from the active pool to prevent request failures
- Real-time monitoring and adjustment of available upstreams

### 3. **Better Fault Tolerance**
- Circuit breaker prevents cascading failures
- Automatic recovery when upstreams come back online
- Configurable failure thresholds and timeout periods

### 4. **Enhanced Observability**
- Detailed logging of upstream health status changes
- Health check results with priority information
- Circuit breaker state monitoring

## Configuration

The circuit breaker can be configured with these parameters:

- `MaxFailures`: Number of failures before opening the circuit (default: 3)
- `Timeout`: How long to wait before trying to close the circuit (default: 30s)
- `ResetTimeout`: How long to wait before resetting failure count (default: 2m)

## Usage

The lazy loading functionality is automatically enabled when ncps starts up. No additional configuration is required. The system will:

1. Start with all upstreams marked as unhealthy
2. Begin health checks in the background
3. Add upstreams to the active pool as they become healthy
4. Remove upstreams from the active pool if they become unhealthy

## Migration

This implementation is backward compatible. Existing configurations will work without changes, but will benefit from the new lazy loading behavior.

## Testing

The implementation includes comprehensive error handling and logging to facilitate debugging:

- Health check failures are logged with error details
- Circuit breaker state changes are tracked
- Upstream availability changes are logged with appropriate levels (INFO for healthy, WARN for unhealthy)

## Future Enhancements

Potential future improvements could include:

1. **Configurable circuit breaker parameters** via command line or config file
2. **Metrics collection** for upstream health and performance monitoring
3. **Adaptive health check intervals** based on upstream stability
4. **Upstream priority adjustments** based on response times and reliability
