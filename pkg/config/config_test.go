package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/config"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/lock/local"
	"github.com/kalbasit/ncps/testhelper"
)

func setupDatabase(t *testing.T) (database.Querier, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "database-path-")
	require.NoError(t, err)

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	cleanup := func() {
		db.DB().Close()
		os.RemoveAll(dir)
	}

	return db, cleanup
}

func TestGetClusterUUID(t *testing.T) {
	t.Parallel()

	t.Run("config not existing", func(t *testing.T) {
		t.Parallel()

		db, cleanup := setupDatabase(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())

		_, err := c.GetClusterUUID(context.Background())
		assert.ErrorIs(t, err, config.ErrConfigNotFound)
	})

	t.Run("key existing", func(t *testing.T) {
		t.Parallel()

		db, cleanup := setupDatabase(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())

		const expectedUUID = "abc-123"

		_, err := db.CreateConfig(context.Background(), database.CreateConfigParams{
			Key:   config.KeyClusterUUID,
			Value: expectedUUID,
		})
		require.NoError(t, err)

		actualUUID, err := c.GetClusterUUID(context.Background())
		require.NoError(t, err)
		assert.Equal(t, expectedUUID, actualUUID)
	})
}

func TestSetClusterUUID(t *testing.T) {
	t.Parallel()

	t.Run("config not existing", func(t *testing.T) {
		t.Parallel()

		db, cleanup := setupDatabase(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())

		err := c.SetClusterUUID(context.Background(), "abc-123")
		require.NoError(t, err)

		conf, err := db.GetConfigByKey(context.Background(), config.KeyClusterUUID)
		require.NoError(t, err)

		assert.Equal(t, config.KeyClusterUUID, conf.Key)
		assert.Equal(t, "abc-123", conf.Value)
	})

	t.Run("key existing", func(t *testing.T) {
		t.Parallel()

		db, cleanup := setupDatabase(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())

		err := c.SetClusterUUID(context.Background(), "abc-123")
		require.NoError(t, err)

		err = c.SetClusterUUID(context.Background(), "def-456")
		require.NoError(t, err)

		conf, err := db.GetConfigByKey(context.Background(), config.KeyClusterUUID)
		require.NoError(t, err)

		assert.Equal(t, config.KeyClusterUUID, conf.Key)
		assert.Equal(t, "def-456", conf.Value)
	})
}
