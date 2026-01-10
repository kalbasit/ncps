package redis_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/redis"
)

func TestNewLocker_ReturnType(t *testing.T) {
	t.Parallel()

	// Connect to non-existent Redis addresses to trigger degraded mode
	cfg := redis.Config{
		Addrs: []string{"localhost:9999", "localhost:9998"},
	}
	retryCfg := lock.RetryConfig{}

	ctx := context.Background()

	// When allowDegradedMode is true, it should return *redis.Locker even if Redis is down
	l, err := redis.NewLocker(ctx, cfg, retryCfg, true)
	require.NoError(t, err)
	assert.IsType(t, (*redis.Locker)(nil), l, "NewLocker should return *redis.Locker in degraded mode")

	// When allowDegradedMode is false, it should return an error
	_, err = redis.NewLocker(ctx, cfg, retryCfg, false)
	assert.Error(t, err)
}
