package config_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/config"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/lock/local"
	"github.com/kalbasit/ncps/testhelper"
)

// databaseFactory is a function that returns a clean, ready-to-use database instance.
type databaseFactory func(t *testing.T) (database.Querier, func())

func setupSQLiteDatabase(t *testing.T) (database.Querier, func()) {
	t.Helper()

	return testhelper.SetupSQLite(t)
}

func setupPostgresDatabase(t *testing.T) (database.Querier, func()) {
	t.Helper()

	return testhelper.SetupPostgres(t)
}

func setupMySQLDatabase(t *testing.T) (database.Querier, func()) {
	t.Helper()

	return testhelper.SetupMySQL(t)
}

func TestGetClusterUUIDBackends(t *testing.T) {
	t.Parallel()

	backends := []struct {
		name   string
		envVar string
		setup  databaseFactory
	}{
		{name: "SQLite", setup: setupSQLiteDatabase},
		{name: "PostgreSQL", envVar: "NCPS_TEST_ADMIN_POSTGRES_URL", setup: setupPostgresDatabase},
		{name: "MySQL", envVar: "NCPS_TEST_ADMIN_MYSQL_URL", setup: setupMySQLDatabase},
	}

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			if b.envVar != "" && os.Getenv(b.envVar) == "" {
				t.Skipf("Skipping %s: %s not set", b.name, b.envVar)
			}

			runGetClusterUUIDTestSuite(t, b.setup)
		})
	}
}

func runGetClusterUUIDTestSuite(t *testing.T, factory databaseFactory) {
	t.Helper()

	t.Run("config not existing", testGetClusterUUIDNotExisting(factory))
	t.Run("key existing", testGetClusterUUIDExisting(factory))
}

func testGetClusterUUIDNotExisting(factory databaseFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())

		_, err := c.GetClusterUUID(context.Background())
		assert.ErrorIs(t, err, config.ErrConfigNotFound)
	}
}

func testGetClusterUUIDExisting(factory databaseFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
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
	}
}

func TestSetClusterUUIDBackends(t *testing.T) {
	t.Parallel()

	backends := []struct {
		name   string
		envVar string
		setup  databaseFactory
	}{
		{name: "SQLite", setup: setupSQLiteDatabase},
		{name: "PostgreSQL", envVar: "NCPS_TEST_ADMIN_POSTGRES_URL", setup: setupPostgresDatabase},
		{name: "MySQL", envVar: "NCPS_TEST_ADMIN_MYSQL_URL", setup: setupMySQLDatabase},
	}

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			if b.envVar != "" && os.Getenv(b.envVar) == "" {
				t.Skipf("Skipping %s: %s not set", b.name, b.envVar)
			}

			runSetClusterUUIDTestSuite(t, b.setup)
		})
	}
}

func runSetClusterUUIDTestSuite(t *testing.T, factory databaseFactory) {
	t.Helper()

	t.Run("config not existing", testSetClusterUUIDNotExisting(factory))
	t.Run("key existing", testSetClusterUUIDExisting(factory))
}

func testSetClusterUUIDNotExisting(factory databaseFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())

		err := c.SetClusterUUID(context.Background(), "abc-123")
		require.NoError(t, err)

		conf, err := db.GetConfigByKey(context.Background(), config.KeyClusterUUID)
		require.NoError(t, err)

		assert.Equal(t, config.KeyClusterUUID, conf.Key)
		assert.Equal(t, "abc-123", conf.Value)
	}
}

func testSetClusterUUIDExisting(factory databaseFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
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
	}
}
