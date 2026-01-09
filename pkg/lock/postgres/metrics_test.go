package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/postgres"
)

// MockMetricRecorder is a mock for recording metrics
// We can't easily mock the global lock.RecordLockDuration package function effectively
// without changing the package structure, but we can verify side effects or
// ensure that the code path that calls it is executed.
//
// However, since we can't mock the package-level functions in `pkg/lock`,
// we will instead verify that the `acquisitionTimes` map in Locker is populated/cleared correctly,
// which is the prerequisite for recording duration.

func TestRWLocker_WriteMetrics(t *testing.T) {
	t.Parallel()

	skipIfPostgresNotAvailable(t)

	ctx := context.Background()

	querier, cleanup := getTestDatabase(t)
	defer cleanup()

	cfg := getTestConfig()
	retryCfg := lock.RetryConfig{MaxAttempts: 1, InitialDelay: 10 * time.Millisecond, MaxDelay: 50 * time.Millisecond}

	rwLockerInterface, err := postgres.NewRWLocker(ctx, querier, cfg, retryCfg, false)
	require.NoError(t, err)

	rwLocker := rwLockerInterface.(*postgres.RWLocker)

	key := getUniqueKey(t, "metrics-write")

	// Acquire Write Lock
	err = rwLocker.Lock(ctx, key, 1*time.Second)
	require.NoError(t, err)

	// Verify that the acquisition time is stored
	acqTime, ok := rwLocker.GetAcquisitionTime(key)
	assert.True(t, ok, "Acquisition time should be stored for write lock")
	assert.WithinDuration(t, time.Now(), acqTime, 1*time.Second)

	// Release Write Lock
	err = rwLocker.Unlock(ctx, key)
	require.NoError(t, err)

	// Verify that the acquisition time is cleared
	_, ok = rwLocker.GetAcquisitionTime(key)
	assert.False(t, ok, "Acquisition time should be cleared after unlock")
}

func TestRWLocker_ReadMetrics_NotRecorded(t *testing.T) {
	t.Parallel()

	skipIfPostgresNotAvailable(t)

	ctx := context.Background()

	querier, cleanup := getTestDatabase(t)
	defer cleanup()

	cfg := getTestConfig()
	retryCfg := lock.RetryConfig{MaxAttempts: 1}

	rwLockerInterface, err := postgres.NewRWLocker(ctx, querier, cfg, retryCfg, false)
	require.NoError(t, err)

	rwLocker := rwLockerInterface.(*postgres.RWLocker)

	key := getUniqueKey(t, "metrics-read")

	// Acquire Read Lock
	err = rwLocker.RLock(ctx, key, 1*time.Second)
	require.NoError(t, err)

	// Verify that the acquisition time is NOT stored for read locks
	_, ok := rwLocker.GetAcquisitionTime(key)
	assert.False(t, ok, "Acquisition time should NOT be stored for read lock")

	// Release Read Lock
	err = rwLocker.RUnlock(ctx, key)
	require.NoError(t, err)
}
