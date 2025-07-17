package cache_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

func TestHealthCheck(t *testing.T) {
	t.Parallel()

	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), testdata.PublicKeys())
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

	// Wait for the healthcheck to run
	time.Sleep(1 * time.Second)

	// Check that the upstream is healthy and the priority is updated
	assert.True(t, uc.IsHealthy())
	assert.Equal(t, uint64(40), uc.GetPriority())

	// Shutdown the test server
	ts.Close()

	// Wait for the healthcheck to run again
	time.Sleep(1 * time.Second)

	// Check that the upstream is unhealthy
	assert.False(t, uc.IsHealthy())
}
