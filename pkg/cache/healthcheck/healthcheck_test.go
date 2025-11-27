package healthcheck_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

const cacheName = "cache.example.com"

func TestHealthCheck(t *testing.T) {
	t.Parallel()

	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	defer os.RemoveAll(dir) // clean up

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), testdata.PublicKeys(), nil)
	require.NoError(t, err)

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:" + dbFile)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := cache.New(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

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
