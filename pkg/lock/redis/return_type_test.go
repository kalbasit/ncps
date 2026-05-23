package redis_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/redis"
)

func TestNewLocker_ReturnType(t *testing.T) {
	t.Parallel()

	// Use non-existent Redis addresses to trigger degraded mode. A bounded context keeps
	// the Ping retries from consuming the go-redis default DialTimeout (5s) per attempt;
	// 500ms is more than enough for "connection refused" on localhost.
	cfg := redis.Config{
		Addrs: []string{"localhost:9999", "localhost:9998"},
	}
	retryCfg := lock.RetryConfig{}

	// When allowDegradedMode is true, it should return *redis.Locker even if Redis is down.
	ctxDegraded, cancelDegraded := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancelDegraded()

	l, err := redis.NewLocker(ctxDegraded, cfg, retryCfg, true)
	require.NoError(t, err)
	assert.IsType(t, (*redis.Locker)(nil), l, "NewLocker should return *redis.Locker in degraded mode")

	// When allowDegradedMode is false, it should return an error.
	ctxStrict, cancelStrict := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancelStrict()

	_, err = redis.NewLocker(ctxStrict, cfg, retryCfg, false)
	assert.Error(t, err)
}
