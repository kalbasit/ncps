package server

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	locklocal "github.com/kalbasit/ncps/pkg/lock/local"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testhelper"
)

const (
	cacheName           = "cache.example.com"
	downloadLockTTL     = 5 * time.Minute
	downloadPollTimeout = 30 * time.Second
	cacheLockTTL        = 30 * time.Minute
)

// serverFactory is a function that returns a clean, ready-to-use Server instance
// and takes care of cleaning up once the test is done.
type serverFactory func(t *testing.T) (*Server, func())

func setupSQLiteServer(t *testing.T) (*Server, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	db, dbCleanup := testhelper.SetupSQLite(t)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	downloadLocker := locklocal.NewLocker()
	cacheLocker := locklocal.NewRWLocker()

	c, err := cache.New(newContext(), cacheName, db, localStore, localStore, localStore, "",
		downloadLocker, cacheLocker, downloadLockTTL, downloadPollTimeout, cacheLockTTL)
	require.NoError(t, err)

	s := New(c)

	cleanup := func() {
		c.Close()
		dbCleanup()
		os.RemoveAll(dir)
	}

	return s, cleanup
}

func setupPostgresServer(t *testing.T) (*Server, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	db, _, dbCleanup := testhelper.SetupPostgres(t)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	downloadLocker := locklocal.NewLocker()
	cacheLocker := locklocal.NewRWLocker()

	c, err := cache.New(newContext(), cacheName, db, localStore, localStore, localStore, "",
		downloadLocker, cacheLocker, downloadLockTTL, downloadPollTimeout, cacheLockTTL)
	require.NoError(t, err)

	s := New(c)

	cleanup := func() {
		c.Close()
		dbCleanup()
		os.RemoveAll(dir)
	}

	return s, cleanup
}

func setupMySQLServer(t *testing.T) (*Server, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	db, _, dbCleanup := testhelper.SetupMySQL(t)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	downloadLocker := locklocal.NewLocker()

	cacheLocker := locklocal.NewRWLocker()

	c, err := cache.New(newContext(), cacheName, db, localStore, localStore, localStore, "",
		downloadLocker, cacheLocker, downloadLockTTL, downloadPollTimeout, cacheLockTTL)
	require.NoError(t, err)

	s := New(c)

	cleanup := func() {
		c.Close()
		dbCleanup()
		os.RemoveAll(dir)
	}

	return s, cleanup
}

func TestSetDeletePermittedBackends(t *testing.T) {
	t.Parallel()

	backends := []struct {
		name   string
		envVar string
		setup  serverFactory
	}{
		{name: "SQLite", setup: setupSQLiteServer},
		{name: "PostgreSQL", envVar: "NCPS_TEST_ADMIN_POSTGRES_URL", setup: setupPostgresServer},
		{name: "MySQL", envVar: "NCPS_TEST_ADMIN_MYSQL_URL", setup: setupMySQLServer},
	}

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			if b.envVar != "" && os.Getenv(b.envVar) == "" {
				t.Skipf("Skipping %s: %s not set", b.name, b.envVar)
			}

			runSetDeletePermittedTestSuite(t, b.setup)
		})
	}
}

func runSetDeletePermittedTestSuite(t *testing.T, factory serverFactory) {
	t.Helper()

	t.Run("false", testSetDeletePermittedFalse(factory))
	t.Run("true", testSetDeletePermittedTrue(factory))
}

func testSetDeletePermittedFalse(factory serverFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		s, cleanup := factory(t)
		t.Cleanup(cleanup)

		s.SetDeletePermitted(false)

		assert.False(t, s.deletePermitted)
	}
}

func testSetDeletePermittedTrue(factory serverFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		s, cleanup := factory(t)
		t.Cleanup(cleanup)

		s.SetDeletePermitted(true)

		assert.True(t, s.deletePermitted)
	}
}

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}
