package server

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
	"github.com/kalbasit/ncps/pkg/database"
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

	db, err := database.Open("sqlite:" + dbFile)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := cache.New(newContext(), cacheName, db, localStore, localStore, localStore)
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
