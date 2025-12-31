package server

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/database"
	locklocal "github.com/kalbasit/ncps/pkg/lock/local"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testhelper"
)

const cacheName = "cache.example.com"

func TestSetDeletePermitted(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	defer os.RemoveAll(dir) // clean up

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	// Use local locks for tests
	downloadLocker := locklocal.NewLocker()
	lruLocker := locklocal.NewRWLocker()

	c, err := cache.New(newContext(), cacheName, db, localStore, localStore, localStore, "",
		downloadLocker, lruLocker, 5*time.Minute, 30*time.Minute)
	require.NoError(t, err)

	t.Run("false", func(t *testing.T) {
		t.Parallel()

		s := New(c)
		s.SetDeletePermitted(false)

		assert.False(t, s.deletePermitted)
	})

	t.Run("true", func(t *testing.T) {
		t.Parallel()

		s := New(c)
		s.SetDeletePermitted(true)

		assert.True(t, s.deletePermitted)
	})
}

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}
