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

	db, _, cleanup := testhelper.SetupPostgres(t)

	return db, cleanup
}

func setupMySQLDatabase(t *testing.T) (database.Querier, func()) {
	t.Helper()

	db, _, cleanup := testhelper.SetupMySQL(t)

	return db, cleanup
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

func TestValidateOrStoreCDCConfigBackends(t *testing.T) {
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

			runValidateOrStoreCDCConfigTestSuite(t, b.setup)
		})
	}
}

func runValidateOrStoreCDCConfigTestSuite(t *testing.T, factory databaseFactory) {
	t.Helper()

	t.Run("first boot CDC disabled", testValidateCDCFirstBootDisabled(factory))
	t.Run("first boot CDC enabled", testValidateCDCFirstBootEnabled(factory))
	t.Run("first boot CDC enabled with zero min", testValidateCDCFirstBootZeroMin(factory))
	t.Run("first boot CDC enabled with zero avg", testValidateCDCFirstBootZeroAvg(factory))
	t.Run("first boot CDC enabled with zero max", testValidateCDCFirstBootZeroMax(factory))
	t.Run("first boot CDC enabled with all zero", testValidateCDCFirstBootAllZero(factory))
	t.Run("second boot same flags", testValidateCDCSameFlags(factory))
	t.Run("second boot changed min", testValidateCDCChangedMin(factory))
	t.Run("second boot changed avg", testValidateCDCChangedAvg(factory))
	t.Run("second boot changed max", testValidateCDCChangedMax(factory))
	t.Run("second boot disable after enabled", testValidateCDCDisableAfterEnabled(factory))
}

func testValidateCDCFirstBootDisabled(factory databaseFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())
		ctx := context.Background()

		// First boot with CDC disabled - should not store anything
		err := c.ValidateOrStoreCDCConfig(ctx, false, 65536, 262144, 1048576)
		require.NoError(t, err)

		// Verify nothing was stored
		_, err = c.GetCDCEnabled(ctx)
		assert.ErrorIs(t, err, config.ErrConfigNotFound)
	}
}

func testValidateCDCFirstBootEnabled(factory databaseFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())
		ctx := context.Background()

		// First boot with CDC enabled - should store all 4 values
		err := c.ValidateOrStoreCDCConfig(ctx, true, 65536, 262144, 1048576)
		require.NoError(t, err)

		// Verify values were stored
		enabledStr, err := c.GetCDCEnabled(ctx)
		require.NoError(t, err)
		assert.Equal(t, "true", enabledStr)

		minStr, err := c.GetCDCMin(ctx)
		require.NoError(t, err)
		assert.Equal(t, "65536", minStr)

		avgStr, err := c.GetCDCAvg(ctx)
		require.NoError(t, err)
		assert.Equal(t, "262144", avgStr)

		maxStr, err := c.GetCDCMax(ctx)
		require.NoError(t, err)
		assert.Equal(t, "1048576", maxStr)
	}
}

func testValidateCDCFirstBootZeroMin(factory databaseFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())
		ctx := context.Background()

		// First boot with CDC enabled but zero min - should fail
		err := c.ValidateOrStoreCDCConfig(ctx, true, 0, 262144, 1048576)
		require.Error(t, err)
		require.ErrorIs(t, err, config.ErrCDCInvalidChunkSizes)
		assert.Contains(t, err.Error(), "min=0")

		// Verify nothing was stored
		_, err = c.GetCDCEnabled(ctx)
		assert.ErrorIs(t, err, config.ErrConfigNotFound)
	}
}

func testValidateCDCFirstBootZeroAvg(factory databaseFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())
		ctx := context.Background()

		// First boot with CDC enabled but zero avg - should fail
		err := c.ValidateOrStoreCDCConfig(ctx, true, 65536, 0, 1048576)
		require.Error(t, err)
		require.ErrorIs(t, err, config.ErrCDCInvalidChunkSizes)
		assert.Contains(t, err.Error(), "avg=0")

		// Verify nothing was stored
		_, err = c.GetCDCEnabled(ctx)
		assert.ErrorIs(t, err, config.ErrConfigNotFound)
	}
}

func testValidateCDCFirstBootZeroMax(factory databaseFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())
		ctx := context.Background()

		// First boot with CDC enabled but zero max - should fail
		err := c.ValidateOrStoreCDCConfig(ctx, true, 65536, 262144, 0)
		require.Error(t, err)
		require.ErrorIs(t, err, config.ErrCDCInvalidChunkSizes)
		assert.Contains(t, err.Error(), "max=0")

		// Verify nothing was stored
		_, err = c.GetCDCEnabled(ctx)
		assert.ErrorIs(t, err, config.ErrConfigNotFound)
	}
}

func testValidateCDCFirstBootAllZero(factory databaseFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())
		ctx := context.Background()

		// First boot with CDC enabled but all zero sizes - should fail
		err := c.ValidateOrStoreCDCConfig(ctx, true, 0, 0, 0)
		require.Error(t, err)
		require.ErrorIs(t, err, config.ErrCDCInvalidChunkSizes)

		// Verify nothing was stored
		_, err = c.GetCDCEnabled(ctx)
		assert.ErrorIs(t, err, config.ErrConfigNotFound)
	}
}

func testValidateCDCSameFlags(factory databaseFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())
		ctx := context.Background()

		// First boot with CDC enabled
		err := c.ValidateOrStoreCDCConfig(ctx, true, 65536, 262144, 1048576)
		require.NoError(t, err)

		// Second boot with same flags - should succeed
		err = c.ValidateOrStoreCDCConfig(ctx, true, 65536, 262144, 1048576)
		require.NoError(t, err)
	}
}

func testValidateCDCChangedMin(factory databaseFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())
		ctx := context.Background()

		// First boot with CDC enabled
		err := c.ValidateOrStoreCDCConfig(ctx, true, 65536, 262144, 1048576)
		require.NoError(t, err)

		// Second boot with changed min - should fail
		err = c.ValidateOrStoreCDCConfig(ctx, true, 32768, 262144, 1048576)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CDC config changed")
		assert.Contains(t, err.Error(), "cdc_min")
	}
}

func testValidateCDCChangedAvg(factory databaseFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())
		ctx := context.Background()

		// First boot with CDC enabled
		err := c.ValidateOrStoreCDCConfig(ctx, true, 65536, 262144, 1048576)
		require.NoError(t, err)

		// Second boot with changed avg - should fail
		err = c.ValidateOrStoreCDCConfig(ctx, true, 65536, 131072, 1048576)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CDC config changed")
		assert.Contains(t, err.Error(), "cdc_avg")
	}
}

func testValidateCDCChangedMax(factory databaseFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())
		ctx := context.Background()

		// First boot with CDC enabled
		err := c.ValidateOrStoreCDCConfig(ctx, true, 65536, 262144, 1048576)
		require.NoError(t, err)

		// Second boot with changed max - should fail
		err = c.ValidateOrStoreCDCConfig(ctx, true, 65536, 262144, 2097152)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CDC config changed")
		assert.Contains(t, err.Error(), "cdc_max")
	}
}

func testValidateCDCDisableAfterEnabled(factory databaseFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		c := config.New(db, local.NewRWLocker())
		ctx := context.Background()

		// First boot with CDC enabled
		err := c.ValidateOrStoreCDCConfig(ctx, true, 65536, 262144, 1048576)
		require.NoError(t, err)

		// Second boot with CDC disabled - should fail
		err = c.ValidateOrStoreCDCConfig(ctx, false, 65536, 262144, 1048576)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CDC cannot be disabled after being enabled")
	}
}
