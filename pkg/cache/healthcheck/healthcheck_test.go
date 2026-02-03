package healthcheck_test

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
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

const (
	cacheName       = "cache.example.com"
	downloadLockTTL = 5 * time.Minute
	cacheLockTTL    = 30 * time.Minute
)

// cacheFactory is a function that returns a clean, ready-to-use Cache instance
// and takes care of cleaning up once the test is done.
type cacheFactory func(t *testing.T) (*cache.Cache, func())

func setupSQLiteCache(t *testing.T) (*cache.Cache, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	db, dbCleanup := testhelper.SetupSQLite(t)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	downloadLocker := locklocal.NewLocker()
	cacheLocker := locklocal.NewRWLocker()

	c, err := cache.New(newContext(), cacheName, db, localStore, localStore, localStore, "",
		downloadLocker, cacheLocker, downloadLockTTL, cacheLockTTL)
	require.NoError(t, err)

	cleanup := func() {
		c.Close()
		dbCleanup()
		os.RemoveAll(dir)
	}

	return c, cleanup
}

func setupPostgresCache(t *testing.T) (*cache.Cache, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	db, _, dbCleanup := testhelper.SetupPostgres(t)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	downloadLocker := locklocal.NewLocker()
	cacheLocker := locklocal.NewRWLocker()

	c, err := cache.New(newContext(), cacheName, db, localStore, localStore, localStore, "",
		downloadLocker, cacheLocker, downloadLockTTL, cacheLockTTL)
	require.NoError(t, err)

	cleanup := func() {
		c.Close()
		dbCleanup()
		os.RemoveAll(dir)
	}

	return c, cleanup
}

func setupMySQLCache(t *testing.T) (*cache.Cache, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	db, _, dbCleanup := testhelper.SetupMySQL(t)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	downloadLocker := locklocal.NewLocker()
	cacheLocker := locklocal.NewRWLocker()

	c, err := cache.New(newContext(), cacheName, db, localStore, localStore, localStore, "",
		downloadLocker, cacheLocker, downloadLockTTL, cacheLockTTL)
	require.NoError(t, err)

	cleanup := func() {
		c.Close()
		dbCleanup()
		os.RemoveAll(dir)
	}

	return c, cleanup
}

func TestHealthCheckBackends(t *testing.T) {
	t.Parallel()

	backends := []struct {
		name   string
		envVar string
		setup  cacheFactory
	}{
		{name: "SQLite", setup: setupSQLiteCache},
		{name: "PostgreSQL", envVar: "NCPS_TEST_ADMIN_POSTGRES_URL", setup: setupPostgresCache},
		{name: "MySQL", envVar: "NCPS_TEST_ADMIN_MYSQL_URL", setup: setupMySQLCache},
	}

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			if b.envVar != "" && os.Getenv(b.envVar) == "" {
				t.Skipf("Skipping %s: %s not set", b.name, b.envVar)
			}

			testHealthCheck(t, b.setup)
		})
	}
}

func testHealthCheck(t *testing.T, factory cacheFactory) {
	t.Helper()

	ts := testdata.NewTestServer(t, 40)
	t.Cleanup(ts.Close)

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	c, cleanup := factory(t)
	t.Cleanup(cleanup)

	c.AddUpstreamCaches(newContext(), uc)

	// Get the instance of healthchecker
	healthChecker := c.GetHealthChecker()

	// Trigger the HealthCheck
	trigC := healthChecker.Trigger()
	<-trigC

	// Check that the upstream is healthy and the priority is updated
	assert.True(t, uc.IsHealthy())
	assert.Equal(t, uint64(40), uc.GetPriority())

	// Shutdown the test server
	ts.Close()

	// Trigger the HealthCheck
	trigC = healthChecker.Trigger()
	<-trigC

	// Check that the upstream is unhealthy
	assert.False(t, uc.IsHealthy())
}

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}
