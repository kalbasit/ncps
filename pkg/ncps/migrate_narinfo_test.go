package ncps_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"
	entnarinforeference "github.com/kalbasit/ncps/ent/narinforeference"
	entnarinfosignature "github.com/kalbasit/ncps/ent/narinfosignature"
	narinfopkg "github.com/nix-community/go-nix/pkg/narinfo"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/narinfo"
	"github.com/kalbasit/ncps/pkg/ncps"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// migrationFactory is a function that returns a clean, ready-to-use database,
// local store, and directory path, and takes care of cleaning up once the test is done.
// It also returns a rebind function to convert SQL queries from ? bindvars to the backend's format.
type migrationFactory func(t *testing.T) (*database.Client, *local.Store, string, string, func(string) string, func())

// TestMigrateNarInfoBackends runs all migration tests against all supported database backends.
func TestMigrateNarInfoBackends(t *testing.T) {
	t.Parallel()

	backends := []struct {
		name   string
		envVar string
		setup  migrationFactory
	}{
		{
			name:  "SQLite",
			setup: setupNarInfoMigrationSQLite,
		},
		{
			name:   "PostgreSQL",
			envVar: "NCPS_TEST_ADMIN_POSTGRES_URL",
			setup:  setupNarInfoMigrationPostgres,
		},
		{
			name:   "MySQL",
			envVar: "NCPS_TEST_ADMIN_MYSQL_URL",
			setup:  setupNarInfoMigrationMySQL,
		},
	}

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			if b.envVar != "" && os.Getenv(b.envVar) == "" {
				t.Skipf("Skipping %s: %s not set", b.name, b.envVar)
			}

			runMigrateNarInfoSuite(t, b.setup)
		})
	}
}

func runMigrateNarInfoSuite(t *testing.T, factory migrationFactory) {
	t.Helper()

	t.Run("Success", testMigrateNarInfoSuccess(factory))
	t.Run("DryRun", testMigrateNarInfoDryRun(factory))
	t.Run("Idempotency", testMigrateNarInfoIdempotency(factory))
	t.Run("MultipleNarInfos", testMigrateNarInfoMultipleNarInfos(factory))
	t.Run("AlreadyMigrated", testMigrateNarInfoAlreadyMigrated(factory))
	t.Run("StorageIterationError", testMigrateNarInfoStorageIterationError(factory))
	t.Run("WithReferencesAndSignatures", testMigrateNarInfoWithReferencesAndSignatures(factory))
	t.Run("DeleteAlreadyMigrated", testMigrateNarInfoDeleteAlreadyMigrated(factory))
	t.Run("ConcurrentMigration", testMigrateNarInfoConcurrentMigration(factory))
	t.Run("PartialData", testMigrateNarInfoPartialData(factory))
	t.Run("TransactionRollback", testMigrateNarInfoTransactionRollback(factory))
	t.Run("MissingNarFile", testMigrateNarInfoMissingNarFile(factory))
	t.Run("ProgressTracking", testMigrateNarInfoProgressTracking(factory))
	t.Run("LargeNarInfo", testMigrateNarInfoLargeNarInfo(factory))
}

func testMigrateNarInfoSuccess(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		dbClient, _, dir, dbURL, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Pre-populate storage with narinfos
		narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

		// Verify not in database
		var count int

		err := dbClient.DB().QueryRowContext(
			ctx, rebind("SELECT COUNT(*) FROM narinfos WHERE hash = ?"), testdata.Nar1.NarInfoHash,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)

		// Run migration using CLI command
		app, err := ncps.New()
		require.NoError(t, err)

		// Register in DB as unmigrated
		ni, err := narinfopkg.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
		require.NoError(t, err)
		require.NoError(t, testhelper.RegisterNarInfoAsUnmigrated(ctx, dbClient, testdata.Nar1.NarInfoHash, ni))

		// Use --concurrency=1 to avoid MySQL deadlocks when migrating multiple narinfos in parallel
		args := []string{
			"ncps", "migrate-narinfo",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--concurrency", "1",
		}
		require.NoError(t, app.Run(ctx, args))

		// Verify in database
		err = dbClient.DB().QueryRowContext(
			ctx, rebind("SELECT COUNT(*) FROM narinfos WHERE hash = ?"), testdata.Nar1.NarInfoHash,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)

		// Verify deleted from storage (default behavior)
		assert.NoFileExists(t, narInfoPath)
	}
}

func testMigrateNarInfoDryRun(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		dbClient, store, dir, _, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Pre-populate storage with narinfos
		narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

		// Run dry-run migration
		// WalkNarInfos is implemented by local.Store

		totalProcessed := 0

		err := store.WalkNarInfos(ctx, func(hash string) error {
			totalProcessed++

			// In dry-run mode, we don't actually migrate
			t.Logf("[DRY-RUN] Would migrate hash: %s", hash)

			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, 1, totalProcessed)

		// Verify NOT in database
		var count int

		err = dbClient.DB().QueryRowContext(
			ctx, rebind("SELECT COUNT(*) FROM narinfos WHERE hash = ?"), testdata.Nar1.NarInfoHash,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)

		// Verify still in storage
		assert.FileExists(t, narInfoPath)
	}
}

func testMigrateNarInfoIdempotency(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		dbClient, store, dir, _, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Pre-populate storage with narinfos
		narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

		// WalkNarInfos is implemented by local.Store

		// Run migration first time
		err := store.WalkNarInfos(ctx, func(hash string) error {
			ni, err := store.GetNarInfo(ctx, hash)
			if err != nil {
				return err
			}

			return testhelper.MigrateNarInfoToDatabase(ctx, dbClient, hash, ni)
		})
		require.NoError(t, err)

		// Run migration second time (should be idempotent)
		err = store.WalkNarInfos(ctx, func(hash string) error {
			ni, err := store.GetNarInfo(ctx, hash)
			if err != nil {
				return err
			}

			// This should handle duplicate key gracefully
			err = testhelper.MigrateNarInfoToDatabase(ctx, dbClient, hash, ni)

			// Should either succeed or succeed if multiple concurrently handled the same hash
			if err != nil && !database.IsDuplicateKeyError(err) {
				return err
			}

			return nil
		})
		require.NoError(t, err)

		// Verify still only one record in database
		var count int

		err = dbClient.DB().QueryRowContext(
			ctx, rebind("SELECT COUNT(*) FROM narinfos WHERE hash = ?"), testdata.Nar1.NarInfoHash,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	}
}

func testMigrateNarInfoMultipleNarInfos(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		dbClient, store, dir, _, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Pre-populate storage with multiple narinfos
		entries := []testdata.Entry{testdata.Nar1, testdata.Nar2, testdata.Nar3}

		for _, entry := range entries {
			narInfoPath := filepath.Join(dir, "store", "narinfo", entry.NarInfoPath)
			require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
			require.NoError(t, os.WriteFile(narInfoPath, []byte(entry.NarInfoText), 0o600))

			narPath := filepath.Join(dir, "store", "nar", entry.NarPath)
			require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
			require.NoError(t, os.WriteFile(narPath, []byte(entry.NarText), 0o600))
		}

		// Run migration
		// WalkNarInfos is implemented by local.Store

		totalProcessed := 0

		err := store.WalkNarInfos(ctx, func(hash string) error {
			totalProcessed++

			ni, err := store.GetNarInfo(ctx, hash)
			if err != nil {
				return err
			}

			return testhelper.MigrateNarInfoToDatabase(ctx, dbClient, hash, ni)
		})
		require.NoError(t, err)
		assert.Equal(t, len(entries), totalProcessed)

		// Verify all in database
		var count int

		err = dbClient.DB().QueryRowContext(ctx, rebind("SELECT COUNT(*) FROM narinfos")).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, len(entries), count)
	}
}

func testMigrateNarInfoAlreadyMigrated(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		dbClient, store, dir, _, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Pre-populate storage with narinfo
		narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

		// Pre-migrate to database
		ni, err := store.GetNarInfo(ctx, testdata.Nar1.NarInfoHash)
		require.NoError(t, err)

		err = testhelper.MigrateNarInfoToDatabase(ctx, dbClient, testdata.Nar1.NarInfoHash, ni)
		require.NoError(t, err)

		// Fetch migrated hashes
		migratedHashes, err := dbClient.Ent().NarInfo.Query().
			Where(entnarinfo.URLNotNil()).
			Select(entnarinfo.FieldHash).
			Strings(ctx)
		require.NoError(t, err)

		migratedHashesMap := make(map[string]struct{}, len(migratedHashes))
		for _, hash := range migratedHashes {
			migratedHashesMap[hash] = struct{}{}
		}

		// Run migration (should skip already-migrated)
		// WalkNarInfos is implemented by local.Store

		skipped := 0
		migrated := 0

		err = store.WalkNarInfos(ctx, func(hash string) error {
			if _, ok := migratedHashesMap[hash]; ok {
				skipped++

				return nil
			}

			migrated++

			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, 1, skipped)
		assert.Equal(t, 0, migrated)

		// Verify still in database
		var count int

		err = dbClient.DB().QueryRowContext(
			ctx, rebind("SELECT COUNT(*) FROM narinfos WHERE hash = ?"), testdata.Nar1.NarInfoHash,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	}
}

func testMigrateNarInfoStorageIterationError(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		_, store, _, _, _, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Walk should succeed even if there are no narinfos
		// WalkNarInfos is implemented by local.Store
		callbackInvoked := false

		err := store.WalkNarInfos(ctx, func(_ string) error {
			callbackInvoked = true

			return nil
		})
		require.NoError(t, err)
		assert.False(t, callbackInvoked, "Callback should not be called for empty directory")
	}
}

func testMigrateNarInfoWithReferencesAndSignatures(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		dbClient, store, dir, _, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Use Nar1 which has references and signatures
		narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

		// Parse the narinfo to check references and signatures
		ni, err := store.GetNarInfo(ctx, testdata.Nar1.NarInfoHash)
		require.NoError(t, err)
		require.NotEmpty(t, ni.References, "Test data should have references")
		require.NotEmpty(t, ni.Signatures, "Test data should have signatures")

		// Migrate
		err = testhelper.MigrateNarInfoToDatabase(ctx, dbClient, testdata.Nar1.NarInfoHash, ni)
		require.NoError(t, err)

		// Verify references in database
		var refCount int

		err = dbClient.DB().QueryRowContext(ctx,
			rebind(`SELECT COUNT(*) FROM narinfo_references nr
		 JOIN narinfos n ON nr.narinfo_id = n.id
		 WHERE n.hash = ?`),
			testdata.Nar1.NarInfoHash).Scan(&refCount)
		require.NoError(t, err)
		assert.Equal(t, len(ni.References), refCount)

		// Verify signatures in database
		var sigCount int

		err = dbClient.DB().QueryRowContext(ctx,
			rebind(`SELECT COUNT(*) FROM narinfo_signatures ns
		 JOIN narinfos n ON ns.narinfo_id = n.id
		 WHERE n.hash = ?`),
			testdata.Nar1.NarInfoHash).Scan(&sigCount)
		require.NoError(t, err)
		assert.Equal(t, len(ni.Signatures), sigCount)
	}
}

func testMigrateNarInfoDeleteAlreadyMigrated(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		dbClient, store, dir, _, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Pre-populate storage
		narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

		// Pre-migrate to database
		ni, err := store.GetNarInfo(ctx, testdata.Nar1.NarInfoHash)
		require.NoError(t, err)

		err = testhelper.MigrateNarInfoToDatabase(ctx, dbClient, testdata.Nar1.NarInfoHash, ni)
		require.NoError(t, err)

		// Verify file exists before deletion
		assert.FileExists(t, narInfoPath)

		// Fetch migrated hashes
		migratedHashes, err := dbClient.Ent().NarInfo.Query().
			Where(entnarinfo.URLNotNil()).
			Select(entnarinfo.FieldHash).
			Strings(ctx)
		require.NoError(t, err)

		migratedHashesMap := make(map[string]struct{}, len(migratedHashes))
		for _, hash := range migratedHashes {
			migratedHashesMap[hash] = struct{}{}
		}

		// Run migration with delete flag for already-migrated items
		// WalkNarInfos is implemented by local.Store

		err = store.WalkNarInfos(ctx, func(hash string) error {
			if _, ok := migratedHashesMap[hash]; ok {
				// Already migrated, just delete from storage
				return store.DeleteNarInfo(ctx, hash)
			}

			return nil
		})
		require.NoError(t, err)

		// Verify file deleted
		assert.NoFileExists(t, narInfoPath)

		// Verify still in database
		var count int

		err = dbClient.DB().QueryRowContext(
			ctx, rebind("SELECT COUNT(*) FROM narinfos WHERE hash = ?"), testdata.Nar1.NarInfoHash,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	}
}

func testMigrateNarInfoConcurrentMigration(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		dbClient, store, dir, _, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Increase connection pool for concurrent operations
		dbClient.DB().SetMaxOpenConns(20)

		// Pre-populate storage with multiple narinfos
		entries := []testdata.Entry{testdata.Nar1, testdata.Nar2, testdata.Nar3, testdata.Nar4, testdata.Nar5}

		for _, entry := range entries {
			narInfoPath := filepath.Join(dir, "store", "narinfo", entry.NarInfoPath)
			require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
			require.NoError(t, os.WriteFile(narInfoPath, []byte(entry.NarInfoText), 0o600))

			narPath := filepath.Join(dir, "store", "nar", entry.NarPath)
			require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
			require.NoError(t, os.WriteFile(narPath, []byte(entry.NarText), 0o600))
		}

		// Simulate concurrent migration with errgroup (similar to actual implementation)
		// WalkNarInfos is implemented by local.Store

		var processed int32

		var errorsCount int32

		err := store.WalkNarInfos(ctx, func(hash string) error {
			atomic.AddInt32(&processed, 1)

			ni, err := store.GetNarInfo(ctx, hash)
			if err != nil {
				atomic.AddInt32(&errorsCount, 1)

				return nil //nolint:nilerr // Continue processing other narinfos
			}

			if err := testhelper.MigrateNarInfoToDatabase(ctx, dbClient, hash, ni); err != nil {
				if !database.IsDuplicateKeyError(err) {
					t.Logf("Migration error for hash %s: %v", hash, err)
					atomic.AddInt32(&errorsCount, 1)
				}
			}

			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, int32(len(entries)), atomic.LoadInt32(&processed)) //nolint:gosec // Test code
		assert.Equal(t, int32(0), atomic.LoadInt32(&errorsCount))

		// Verify all in database
		var count int

		err = dbClient.DB().QueryRowContext(ctx, rebind("SELECT COUNT(*) FROM narinfos")).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, len(entries), count)
	}
}

func testMigrateNarInfoPartialData(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		dbClient, store, dir, _, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Use testdata.Nar1 but verify it has no Deriver, System, or CA fields
		narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

		// Migrate
		ni, err := store.GetNarInfo(ctx, testdata.Nar1.NarInfoHash)
		require.NoError(t, err)

		err = testhelper.MigrateNarInfoToDatabase(ctx, dbClient, testdata.Nar1.NarInfoHash, ni)
		require.NoError(t, err)

		// Verify in database - all fields should be stored correctly
		// Test that sql.NullString/sql.NullInt64 handling works
		var (
			dbHash      string
			dbStorePath sql.NullString
			dbURLResult sql.NullString
			dbDeriver   sql.NullString
			dbSystem    sql.NullString
			dbCA        sql.NullString
		)

		err = dbClient.DB().QueryRowContext(ctx,
			rebind("SELECT hash, store_path, url, deriver, system, ca FROM narinfos WHERE hash = ?"),
			testdata.Nar1.NarInfoHash).Scan(&dbHash, &dbStorePath, &dbURLResult, &dbDeriver, &dbSystem, &dbCA)
		require.NoError(t, err)

		assert.Equal(t, testdata.Nar1.NarInfoHash, dbHash)
		assert.True(t, dbStorePath.Valid, "StorePath should be populated")
		assert.True(t, dbURLResult.Valid, "URL should be populated")

		// Verify the values match what was in the narinfo
		if ni.Deriver != "" {
			assert.True(t, dbDeriver.Valid)
			assert.Equal(t, ni.Deriver, dbDeriver.String)
		} else {
			assert.False(t, dbDeriver.Valid)
		}

		if ni.System != "" {
			assert.True(t, dbSystem.Valid)
			assert.Equal(t, ni.System, dbSystem.String)
		} else {
			assert.False(t, dbSystem.Valid)
		}

		if ni.CA != "" {
			assert.True(t, dbCA.Valid)
			assert.Equal(t, ni.CA, dbCA.String)
		} else {
			assert.False(t, dbCA.Valid)
		}
	}
}

func testMigrateNarInfoTransactionRollback(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		dbClient, store, dir, _, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Pre-populate storage
		narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

		ni, err := store.GetNarInfo(ctx, testdata.Nar1.NarInfoHash)
		require.NoError(t, err)

		// Start a transaction and intentionally cause a failure.
		// We use Ent's transaction API to mirror the production code path.
		tx, err := dbClient.Ent().Tx(ctx)
		require.NoError(t, err)

		// Create narinfo (should succeed)
		nb := tx.NarInfo.Create().SetHash(testdata.Nar1.NarInfoHash)

		if ni.StorePath != "" {
			nb = nb.SetStorePath(ni.StorePath)
		}

		if ni.URL != "" {
			nb = nb.SetURL(ni.URL)
		}

		if ni.Compression != "" {
			nb = nb.SetCompression(ni.Compression)
		}

		if ni.FileHash != nil {
			nb = nb.SetFileHash(ni.FileHash.String())
		}
		//nolint:gosec // G115: FileSize/NarSize are non-negative by spec
		nb = nb.SetFileSize(int64(ni.FileSize))

		if ni.NarHash != nil {
			nb = nb.SetNarHash(ni.NarHash.String())
		}
		//nolint:gosec // G115: NarSize is non-negative by spec
		nb = nb.SetNarSize(int64(ni.NarSize))

		if ni.Deriver != "" {
			nb = nb.SetDeriver(ni.Deriver)
		}

		if ni.System != "" {
			nb = nb.SetSystem(ni.System)
		}

		if ni.CA != "" {
			nb = nb.SetCa(ni.CA)
		}

		nir, err := nb.Save(ctx)
		require.NoError(t, err)
		require.NotZero(t, nir.ID)

		// Rollback the transaction
		err = tx.Rollback()
		require.NoError(t, err)

		// Verify NOT in database (transaction was rolled back)
		var count int

		err = dbClient.DB().QueryRowContext(
			ctx, rebind("SELECT COUNT(*) FROM narinfos WHERE hash = ?"), testdata.Nar1.NarInfoHash,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count, "Rollback should have removed the narinfo")
	}
}

func testMigrateNarInfoMissingNarFile(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		dbClient, store, dir, _, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Pre-populate ONLY narinfo (no nar file)
		narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

		// Verify nar file does NOT exist
		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		assert.NoFileExists(t, narPath)

		// Migration should still succeed (the nar file might be fetched later)
		ni, err := store.GetNarInfo(ctx, testdata.Nar1.NarInfoHash)
		require.NoError(t, err)

		err = testhelper.MigrateNarInfoToDatabase(ctx, dbClient, testdata.Nar1.NarInfoHash, ni)
		require.NoError(t, err)

		// Verify in database
		var count int

		err = dbClient.DB().QueryRowContext(
			ctx, rebind("SELECT COUNT(*) FROM narinfos WHERE hash = ?"), testdata.Nar1.NarInfoHash,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	}
}

func testMigrateNarInfoProgressTracking(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		dbClient, store, dir, _, _, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Pre-populate storage with multiple narinfos
		entries := []testdata.Entry{testdata.Nar1, testdata.Nar2, testdata.Nar3}

		for _, entry := range entries {
			narInfoPath := filepath.Join(dir, "store", "narinfo", entry.NarInfoPath)
			require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
			require.NoError(t, os.WriteFile(narInfoPath, []byte(entry.NarInfoText), 0o600))

			narPath := filepath.Join(dir, "store", "nar", entry.NarPath)
			require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
			require.NoError(t, os.WriteFile(narPath, []byte(entry.NarText), 0o600))
		}

		// Track progress during migration
		// WalkNarInfos is implemented by local.Store

		var processed int32

		var succeeded int32

		var failed int32

		startTime := time.Now()

		err := store.WalkNarInfos(ctx, func(hash string) error {
			currentProcessed := atomic.AddInt32(&processed, 1)

			ni, err := store.GetNarInfo(ctx, hash)
			if err != nil {
				atomic.AddInt32(&failed, 1)
				t.Logf("Progress: %d/%d processed (failed: %s)", currentProcessed, len(entries), hash)

				return nil //nolint:nilerr // Continue processing other narinfos
			}

			if err := testhelper.MigrateNarInfoToDatabase(ctx, dbClient, hash, ni); err != nil {
				atomic.AddInt32(&failed, 1)
				t.Logf("Progress: %d/%d processed (failed: %s)", currentProcessed, len(entries), hash)

				return nil //nolint:nilerr // Continue processing other narinfos
			}

			atomic.AddInt32(&succeeded, 1)
			t.Logf("Progress: %d/%d processed (succeeded: %s)", currentProcessed, len(entries), hash)

			return nil
		})
		require.NoError(t, err)

		duration := time.Since(startTime)

		t.Logf("Migration completed:")
		t.Logf("  Total processed: %d", atomic.LoadInt32(&processed))
		t.Logf("  Succeeded: %d", atomic.LoadInt32(&succeeded))
		t.Logf("  Failed: %d", atomic.LoadInt32(&failed))
		t.Logf("  Duration: %v", duration)
		t.Logf("  Throughput: %.2f narinfos/sec", float64(atomic.LoadInt32(&processed))/duration.Seconds())

		//nolint:gosec // Test data size is controlled and safe to convert
		assert.Equal(t, int32(len(entries)), atomic.LoadInt32(&processed))
		//nolint:gosec // Test data size is controlled and safe to convert
		assert.Equal(t, int32(len(entries)), atomic.LoadInt32(&succeeded))
		assert.Equal(t, int32(0), atomic.LoadInt32(&failed))
	}
}

func testMigrateNarInfoLargeNarInfo(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		dbClient, store, dir, _, rebind, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Create a "large" narinfo — i.e., one with many more references and signatures
		// than the typical narinfo (which has 2-5 refs and 1-3 sigs). 20 / 10 is plenty
		// to exercise the bulk-insert path without paying for 150 INSERTs per run.
		const numReferences = 20

		const numSignatures = 10

		hash := "0a90gw9sdyz3680wfncd5xf0qg6zh27w"
		narHash := "024wilh5y46xqqjnwp159s13kgvsh8zfr6g6znb8ix2vlyf61rwp"

		// Build references string
		var referencesBuilder strings.Builder

		referencesBuilder.WriteString("References:")

		for i := range numReferences {
			fmt.Fprintf(&referencesBuilder, " ref%d-abcdefgh1234567890abcdefgh1234567890", i)
		}

		referencesStr := referencesBuilder.String()

		// Build narinfo text with many references
		var narInfoBuilder strings.Builder

		narInfoBuilder.WriteString("StorePath: /nix/store/")
		narInfoBuilder.WriteString(hash)
		narInfoBuilder.WriteString("-large-package\n")
		narInfoBuilder.WriteString("URL: nar/")
		narInfoBuilder.WriteString(narHash)
		narInfoBuilder.WriteString(".nar.xz\n")
		narInfoBuilder.WriteString("Compression: xz\n")
		narInfoBuilder.WriteString("FileHash: sha256:")
		narInfoBuilder.WriteString(narHash)
		narInfoBuilder.WriteString("\n")
		narInfoBuilder.WriteString("FileSize: 999999\n")
		narInfoBuilder.WriteString("NarHash: sha256:")
		narInfoBuilder.WriteString(narHash)
		narInfoBuilder.WriteString("\n")
		narInfoBuilder.WriteString("NarSize: 999999\n")
		narInfoBuilder.WriteString(referencesStr)
		narInfoBuilder.WriteString("\n")

		// Add many signatures
		for i := range numSignatures {
			fmt.Fprintf(
				&narInfoBuilder,
				"Sig: cache.test.org-%d:MadTCU1OSFCGUw4aqCKpLCZJpqBc7AbLvO7wgdlls0eq1DwaSnF/82SZE+wJGEiwlHbnZR+14daSaec0W3XoBQ==\n",
				i,
			)
		}

		narInfoText := narInfoBuilder.String()

		// Write to storage
		nifP, err := narinfo.FilePath(hash)
		require.NoError(t, err)

		narInfoPath := filepath.Join(dir, "store", "narinfo", nifP)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(narInfoText), 0o600))

		nFP, err := nar.FilePath(narHash, "xz")
		require.NoError(t, err)

		narPath := filepath.Join(dir, "store", "nar", nFP)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		// Migration walks narinfos and reads narinfo files; the NAR body itself is never
		// opened, so a small placeholder is enough — no need to spend ~1MB of randomness
		// per run.
		require.NoError(t, os.WriteFile(narPath, []byte(testhelper.MustRandString(1024)), 0o600))

		// Run migration
		err = store.WalkNarInfos(ctx, func(h string) error {
			ni, err := store.GetNarInfo(ctx, h)
			if err != nil {
				return fmt.Errorf("failed to get narinfo: %w", err)
			}

			return testhelper.MigrateNarInfoToDatabase(ctx, dbClient, h, ni)
		})
		require.NoError(t, err)

		// Verify in database
		var count int

		err = dbClient.DB().QueryRowContext(
			ctx, rebind("SELECT COUNT(*) FROM narinfos WHERE hash = ?"), hash,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "Should have exactly one narinfo record")

		// Get narinfo ID
		nir, err := dbClient.Ent().NarInfo.Query().
			Where(entnarinfo.HashEQ(hash)).
			Only(ctx)
		require.NoError(t, err)

		// Verify all references were migrated
		references, err := dbClient.Ent().NarInfoReference.Query().
			Where(entnarinforeference.NarinfoIDEQ(nir.ID)).
			Select(entnarinforeference.FieldReference).
			Strings(ctx)
		require.NoError(t, err)
		assert.Len(t, references, numReferences, "Should have all references migrated")

		// Verify all signatures were migrated
		signatures, err := dbClient.Ent().NarInfoSignature.Query().
			Where(entnarinfosignature.NarinfoIDEQ(nir.ID)).
			Select(entnarinfosignature.FieldSignature).
			Strings(ctx)
		require.NoError(t, err)
		assert.Len(t, signatures, numSignatures, "Should have all signatures migrated")

		// Verify a few specific references (spot check first, middle, last).
		assert.Contains(t, references, "ref0-abcdefgh1234567890abcdefgh1234567890")
		assert.Contains(t, references, "ref10-abcdefgh1234567890abcdefgh1234567890")
		assert.Contains(t, references, "ref19-abcdefgh1234567890abcdefgh1234567890")

		// Verify a few specific signatures (spot check first, middle, last).
		assert.Contains(
			t,
			signatures,
			"cache.test.org-0:MadTCU1OSFCGUw4aqCKpLCZJpqBc7AbLvO7wgdlls0eq1DwaSnF/82SZE+wJGEiwlHbnZR+14daSaec0W3XoBQ==",
		)
		assert.Contains(
			t,
			signatures,
			"cache.test.org-5:MadTCU1OSFCGUw4aqCKpLCZJpqBc7AbLvO7wgdlls0eq1DwaSnF/82SZE+wJGEiwlHbnZR+14daSaec0W3XoBQ==",
		)
		assert.Contains(
			t,
			signatures,
			"cache.test.org-9:MadTCU1OSFCGUw4aqCKpLCZJpqBc7AbLvO7wgdlls0eq1DwaSnF/82SZE+wJGEiwlHbnZR+14daSaec0W3XoBQ==",
		)

		t.Logf("Successfully migrated large narinfo with %d references and %d signatures", numReferences, numSignatures)
	}
}

func BenchmarkMigrateNarInfo(b *testing.B) {
	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Setup
	dir := b.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(b, dbFile)

	dbClient, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(b, err)

	store, err := local.New(ctx, dir)
	require.NoError(b, err)

	// Pre-populate storage
	narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
	require.NoError(b, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
	require.NoError(b, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

	narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
	require.NoError(b, os.MkdirAll(filepath.Dir(narPath), 0o755))
	require.NoError(b, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

	ni, err := store.GetNarInfo(ctx, testdata.Nar1.NarInfoHash)
	require.NoError(b, err)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Clean database between iterations
		_, err := dbClient.DB().ExecContext(ctx, "DELETE FROM narinfos")
		require.NoError(b, err)

		err = testhelper.MigrateNarInfoToDatabase(ctx, dbClient, testdata.Nar1.NarInfoHash, ni)
		require.NoError(b, err)
	}
}

func setupNarInfoMigrationSQLite(t *testing.T) (
	*database.Client,
	*local.Store,
	string,
	string,
	func(string) string,
	func(),
) {
	t.Helper()

	ctx := context.Background()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	dbClient, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

	cleanup := func() {
		dbClient.DB().Close()
	}

	dbURL := "sqlite:" + dbFile
	rebind := func(query string) string { return query }

	return dbClient, store, dir, dbURL, rebind, cleanup
}

func setupNarInfoMigrationPostgres(t *testing.T) (
	*database.Client,
	*local.Store,
	string,
	string,
	func(string) string,
	func(),
) {
	t.Helper()

	ctx := context.Background()
	dir := t.TempDir()

	dbClient, dbURL, dbCleanup := testhelper.SetupPostgres(t)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

	cleanup := func() {
		dbCleanup()
	}

	rebind := func(query string) string {
		// Simple replacement of ? with $1, $2, etc.
		// detailed parsing is not required for these tests as we know the queries are simple.
		// We can just iterate and replace.
		var builder strings.Builder

		paramCount := 0

		for _, r := range query {
			if r == '?' {
				paramCount++
				fmt.Fprintf(&builder, "$%d", paramCount)
			} else {
				builder.WriteRune(r)
			}
		}

		return builder.String()
	}

	return dbClient, store, dir, dbURL, rebind, cleanup
}

func setupNarInfoMigrationMySQL(t *testing.T) (
	*database.Client,
	*local.Store,
	string,
	string,
	func(string) string,
	func(),
) {
	t.Helper()

	ctx := context.Background()
	dir := t.TempDir()

	dbClient, dbURL, dbCleanup := testhelper.SetupMySQL(t)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

	cleanup := func() {
		dbCleanup()
	}

	rebind := func(query string) string { return query }

	return dbClient, store, dir, dbURL, rebind, cleanup
}
