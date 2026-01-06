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
		defer cleanup()

		c := config.New(db)

		_, err := c.GetClusterUUID(context.Background())
		assert.ErrorIs(t, err, config.ErrNoClusterUUID)
	})

	t.Run("key existing", func(t *testing.T) {
		t.Parallel()

		db, cleanup := setupDatabase(t)
		defer cleanup()

		c := config.New(db)

		conf1, err := db.CreateConfig(context.Background(), database.CreateConfigParams{
			Key:   config.KeyClusterUUID,
			Value: "abc-123",
		})
		require.NoError(t, err)

		conf2, err := c.GetClusterUUID(context.Background())
		require.NoError(t, err)

		assert.Equal(t, conf1.Value, conf2)
	})
}
