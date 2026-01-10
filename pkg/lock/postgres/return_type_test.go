package postgres_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/postgres"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestNewLocker_ReturnType(t *testing.T) {
	t.Parallel()

	// Connect to a non-existent database to trigger degraded mode
	db, err := sql.Open("pgx", "postgres://invalid:password@localhost:9999/invalid?sslmode=disable")
	require.NoError(t, err)

	defer db.Close()

	querier := &mockQuerier{db: db}

	ctx := context.Background()
	cfg := postgres.Config{}
	retryCfg := lock.RetryConfig{}

	// When allowDegradedMode is true, it should return *postgres.Locker even if DB is down
	l, err := postgres.NewLocker(ctx, querier, cfg, retryCfg, true)
	require.NoError(t, err)
	assert.IsType(t, (*postgres.Locker)(nil), l, "NewLocker should return *postgres.Locker in degraded mode")

	// When allowDegradedMode is false, it should return an error
	_, err = postgres.NewLocker(ctx, querier, cfg, retryCfg, false)
	assert.Error(t, err)
}

type mockQuerier struct {
	database.Querier
	db *sql.DB
}

func (m *mockQuerier) DB() *sql.DB { return m.db }
