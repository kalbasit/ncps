package mysql_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/mysql"

	_ "github.com/go-sql-driver/mysql"
)

func TestCalculateBackoff(t *testing.T) {
	t.Parallel()

	cfg := lock.RetryConfig{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Jitter:       false,
	}

	// Test case 1: Initial delay (attempt 1)
	// 100ms * 2^0 = 100ms
	d := mysql.CalculateBackoff(cfg, 1)
	assert.Equal(t, 100*time.Millisecond, d, "Attempt 1 should be initial delay")

	// Test case 2: Exponential backoff (attempt 2)
	// 100ms * 2^1 = 200ms
	d = mysql.CalculateBackoff(cfg, 2)
	assert.Equal(t, 200*time.Millisecond, d, "Attempt 2 should be 2x initial delay")

	// Test case 3: Exponential backoff (attempt 3)
	// 100ms * 2^2 = 400ms
	d = mysql.CalculateBackoff(cfg, 3)
	assert.Equal(t, 400*time.Millisecond, d, "Attempt 3 should be 4x initial delay")

	// Test case 4: Max delay capping
	// 100ms * 2^10 > 1s
	d = mysql.CalculateBackoff(cfg, 10)
	assert.Equal(t, 1*time.Second, d, "Should be capped at MaxDelay")

	// Test case 5: Jitter
	cfgJitter := cfg
	cfgJitter.Jitter = true
	// We can't predict exact value but it should be >= base delay and <= base delay * (1+jitterFactor)
	d = mysql.CalculateBackoff(cfgJitter, 1)
	assert.GreaterOrEqual(t, d, 100*time.Millisecond, "With jitter, delay should be at least base delay")
	// jitterFactor is 0.5, so max is 1.5x
	assert.LessOrEqual(t, d, time.Duration(float64(100*time.Millisecond)*1.5),
		"With jitter, delay should be within reasonable bounds")
}

func TestNewLocker_ReturnType(t *testing.T) {
	t.Parallel()

	// Connect to a non-existent database to trigger degraded mode
	// We need a mock querier
	db, err := sql.Open("mysql", "invalid:password@tcp(localhost:9999)/invalid")
	require.NoError(t, err)

	defer db.Close()

	querier := &mockQuerier{db: db}

	ctx := context.Background()
	cfg := mysql.Config{}
	retryCfg := lock.RetryConfig{}

	// When allowDegradedMode is true, it should return *mysql.Locker even if DB is down
	l, err := mysql.NewLocker(ctx, querier, cfg, retryCfg, true)
	require.NoError(t, err)
	assert.IsType(t, (*mysql.Locker)(nil), l, "NewLocker should return *mysql.Locker in degraded mode")

	// When allowDegradedMode is false, it should return an error
	_, err = mysql.NewLocker(ctx, querier, cfg, retryCfg, false)
	assert.Error(t, err)
}

type mockQuerier struct {
	database.Querier
	db *sql.DB
}

func (m *mockQuerier) DB() *sql.DB { return m.db }
