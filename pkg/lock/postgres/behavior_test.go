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

func TestLocker_LockContentionTimeout(t *testing.T) {
	t.Parallel()

	skipIfPostgresNotAvailable(t)

	ctx := context.Background()

	querier, cleanup := getTestDatabase(t)
	t.Cleanup(cleanup)

	// Config with low retry count and small delays for fast test
	cfg := getTestConfig()
	retryCfg := lock.RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 50 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Jitter:       false,
	}

	locker1, err := postgres.NewLocker(ctx, querier, cfg, retryCfg, false)
	require.NoError(t, err)

	locker2, err := postgres.NewLocker(ctx, querier, cfg, retryCfg, false)
	require.NoError(t, err)

	key := getUniqueKey(t, "contention-timeout")

	// 1. Locker1 acquires the lock
	err = locker1.Lock(ctx, key, 5*time.Second)
	require.NoError(t, err)

	// 2. Locker2 tries to acquire the same lock
	// Should attempt 3 times with backoff, then fail with "lock acquisition failed"
	startTime := time.Now()
	err = locker2.Lock(ctx, key, 5*time.Second)
	duration := time.Since(startTime)

	// Verify error
	require.Error(t, err)
	require.ErrorIs(t, err, postgres.ErrLockAcquisitionFailed)
	require.Contains(t, err.Error(), "after 3 attempts")

	// Verify it actually waited (approx 50ms + 100ms = 150ms minimum)
	// We allow some slop, but it should definitely be > 140ms
	require.Greater(t, duration, 140*time.Millisecond,
		"Lock should have waited for ~150ms before failing, took %v", duration)

	// 3. Locker1 unlocks
	err = locker1.Unlock(ctx, key)
	require.NoError(t, err)

	// 4. Locker2 can now acquire the lock
	err = locker2.Lock(ctx, key, 5*time.Second)
	require.NoError(t, err)

	assert.NoError(t, locker2.Unlock(ctx, key))
}
