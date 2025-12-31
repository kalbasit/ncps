package lock

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	otelPackageName = "github.com/kalbasit/ncps/pkg/lock"

	// Lock type constants for metrics.
	LockTypeExclusive = "exclusive"
	LockTypeRead      = "read"
	LockTypeWrite     = "write"

	// Lock mode constants for metrics.
	LockModeLocal       = "local"
	LockModeDistributed = "distributed"

	// Lock result constants for metrics.
	LockResultSuccess    = "success"
	LockResultFailure    = "failure"
	LockResultContention = "contention"

	// Lock failure reason constants for metrics.
	LockFailureTimeout         = "timeout"
	LockFailureRedisError      = "redis_error"
	LockFailureContextCanceled = "context_canceled"
	LockFailureCircuitBreaker  = "circuit_breaker"
	LockFailureMaxRetries      = "max_retries"
)

var (
	//nolint:gochecknoglobals
	meter metric.Meter

	// lockAcquisitionsTotal tracks total lock acquisition attempts.
	//nolint:gochecknoglobals
	lockAcquisitionsTotal metric.Int64Counter

	// lockHoldDuration tracks how long locks are held.
	//nolint:gochecknoglobals
	lockHoldDuration metric.Float64Histogram

	// lockFailuresTotal tracks total lock failures.
	//nolint:gochecknoglobals
	lockFailuresTotal metric.Int64Counter

	// lockRetryAttemptsTotal tracks total retry attempts.
	//nolint:gochecknoglobals
	lockRetryAttemptsTotal metric.Int64Counter
)

//nolint:gochecknoinits
func init() {
	meter = otel.Meter(otelPackageName)

	var err error

	lockAcquisitionsTotal, err = meter.Int64Counter(
		"ncps_lock_acquisitions_total",
		metric.WithDescription("Total number of lock acquisition attempts"),
		metric.WithUnit("{attempt}"),
	)
	if err != nil {
		panic(err)
	}

	lockHoldDuration, err = meter.Float64Histogram(
		"ncps_lock_hold_duration_seconds",
		metric.WithDescription("Duration that locks are held"),
		metric.WithUnit("s"),
	)
	if err != nil {
		panic(err)
	}

	lockFailuresTotal, err = meter.Int64Counter(
		"ncps_lock_failures_total",
		metric.WithDescription("Total number of lock failures"),
		metric.WithUnit("{failure}"),
	)
	if err != nil {
		panic(err)
	}

	lockRetryAttemptsTotal, err = meter.Int64Counter(
		"ncps_lock_retry_attempts_total",
		metric.WithDescription("Total number of lock retry attempts"),
		metric.WithUnit("{attempt}"),
	)
	if err != nil {
		panic(err)
	}
}

// RecordLockAcquisition records a lock acquisition attempt.
// lockType should be one of LockType* constants.
// mode should be one of LockMode* constants.
// result should be one of LockResult* constants.
func RecordLockAcquisition(ctx context.Context, lockType, mode, result string) {
	if lockAcquisitionsTotal == nil {
		return
	}

	lockAcquisitionsTotal.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("type", lockType),
			attribute.String("mode", mode),
			attribute.String("result", result),
		),
	)
}

// RecordLockDuration records how long a lock was held.
// lockType should be one of LockType* constants.
// mode should be one of LockMode* constants.
// duration should be in seconds.
func RecordLockDuration(ctx context.Context, lockType, mode string, duration float64) {
	if lockHoldDuration == nil {
		return
	}

	lockHoldDuration.Record(ctx, duration,
		metric.WithAttributes(
			attribute.String("type", lockType),
			attribute.String("mode", mode),
		),
	)
}

// RecordLockFailure records a lock failure.
// lockType should be one of LockType* constants.
// mode should be one of LockMode* constants.
// reason should be one of LockFailure* constants.
func RecordLockFailure(ctx context.Context, lockType, mode, reason string) {
	if lockFailuresTotal == nil {
		return
	}

	lockFailuresTotal.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("type", lockType),
			attribute.String("mode", mode),
			attribute.String("reason", reason),
		),
	)
}

// RecordLockRetryAttempt records a lock retry attempt.
// lockType should be one of LockType* constants.
func RecordLockRetryAttempt(ctx context.Context, lockType string) {
	if lockRetryAttemptsTotal == nil {
		return
	}

	lockRetryAttemptsTotal.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("type", lockType),
		),
	)
}
