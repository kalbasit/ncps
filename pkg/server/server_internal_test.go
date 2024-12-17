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

//nolint:gochecknoglobals
var logger = zerolog.New(io.Discard)

func TestSetDeletePermitted(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:" + dbFile)
	require.NoError(t, err)

	ctx := context.Background()

	localStore, err := local.New(ctx, dir)
	require.NoError(t, err)

	c, err := cache.New(ctx, cacheName, db, localStore, localStore, localStore)
	require.NoError(t, err)

	t.Run("false", func(t *testing.T) {
		t.Parallel()

		s := New(logger, c)
		s.SetDeletePermitted(false)

		assert.False(t, s.deletePermitted)
	})

	t.Run("true", func(t *testing.T) {
		t.Parallel()

		s := New(logger, c)
		s.SetDeletePermitted(true)

		assert.True(t, s.deletePermitted)
	})
}
