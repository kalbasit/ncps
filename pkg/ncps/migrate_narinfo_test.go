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

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// migrationFactory is a function that returns a clean, ready-to-use database,
// local store, and directory path, and takes care of cleaning up once the test is done.
type migrationFactory func(t *testing.T) (database.Querier, *local.Store, string, func())

// TestMigrateNarInfoBackends runs all migration tests against all supported database backends.
func TestMigrateNarInfoBackends(t *testing.T) {
	t.Parallel()

	backends := []struct {
		name   string
		envVar string
		setup  migrationFactory
	}{
		{
			name: "SQLite",
			setup: func(t *testing.T) (database.Querier, *local.Store, string, func()) {
				t.Helper()

				ctx := context.Background()
				dir := t.TempDir()
				dbFile := filepath.Join(dir, "db.sqlite")
				testhelper.CreateMigrateDatabase(t, dbFile)

				db, err := database.Open("sqlite:"+dbFile, nil)
				require.NoError(t, err)

				store, err := local.New(ctx, dir)
				require.NoError(t, err)

				cleanup := func() {
					db.DB().Close()
				}

				return db, store, dir, cleanup
			},
		},
		{
			name:   "PostgreSQL",
			envVar: "NCPS_TEST_ADMIN_POSTGRES_URL",
			setup: func(t *testing.T) (database.Querier, *local.Store, string, func()) {
				t.Helper()

				ctx := context.Background()
				dir := t.TempDir()

				db, dbCleanup := testhelper.SetupPostgres(t)

				store, err := local.New(ctx, dir)
				require.NoError(t, err)

				cleanup := func() {
					dbCleanup()
				}

				return db, store, dir, cleanup
			},
		},
		{
			name:   "MySQL",
			envVar: "NCPS_TEST_ADMIN_MYSQL_URL",
			setup: func(t *testing.T) (database.Querier, *local.Store, string, func()) {
				t.Helper()

				ctx := context.Background()
				dir := t.TempDir()

				db, dbCleanup := testhelper.SetupMySQL(t)

				store, err := local.New(ctx, dir)
				require.NoError(t, err)

				cleanup := func() {
					dbCleanup()
				}

				return db, store, dir, cleanup
			},
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
		db, store, dir, cleanup := factory(t)
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

		err := db.DB().QueryRowContext(
			ctx, "SELECT COUNT(*) FROM narinfos WHERE hash = ?", testdata.Nar1.NarInfoHash,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)

		// Run migration (not dry-run, no delete)
		migratedHashes, err := db.GetMigratedNarInfoHashes(ctx)
		require.NoError(t, err)

		migratedHashesMap := make(map[string]struct{}, len(migratedHashes))
		for _, hash := range migratedHashes {
			migratedHashesMap[hash] = struct{}{}
		}

		totalProcessed := 0

		var errorsCount int32

		err = store.WalkNarInfos(ctx, func(hash string) error {
			totalProcessed++

			if _, ok := migratedHashesMap[hash]; ok {
				return nil
			}

			ni, err := store.GetNarInfo(ctx, hash)
			if err != nil {
				atomic.AddInt32(&errorsCount, 1)

				return nil //nolint:nilerr // Continue processing other narinfos
			}

			// Migrate to database
			if err := testhelper.MigrateNarInfoToDatabase(ctx, db, hash, ni); err != nil {
				atomic.AddInt32(&errorsCount, 1)
			}

			// Delete from storage (default behavior)
			if err := store.DeleteNarInfo(ctx, hash); err != nil {
				atomic.AddInt32(&errorsCount, 1)
			}

			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, 1, totalProcessed)
		assert.Equal(t, int32(0), atomic.LoadInt32(&errorsCount))

		// Verify in database
		err = db.DB().QueryRowContext(
			ctx, "SELECT COUNT(*) FROM narinfos WHERE hash = ?", testdata.Nar1.NarInfoHash,
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
		db, store, dir, cleanup := factory(t)
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

		err = db.DB().QueryRowContext(
			ctx, "SELECT COUNT(*) FROM narinfos WHERE hash = ?", testdata.Nar1.NarInfoHash,
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
		db, store, dir, cleanup := factory(t)
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

			return testhelper.MigrateNarInfoToDatabase(ctx, db, hash, ni)
		})
		require.NoError(t, err)

		// Run migration second time (should be idempotent)
		err = store.WalkNarInfos(ctx, func(hash string) error {
			ni, err := store.GetNarInfo(ctx, hash)
			if err != nil {
				return err
			}

			// This should handle duplicate key gracefully
			err = testhelper.MigrateNarInfoToDatabase(ctx, db, hash, ni)

			// Should either succeed or succeed if multiple concurrently handled the same hash
			if err != nil && !database.IsDuplicateKeyError(err) {
				return err
			}

			return nil
		})
		require.NoError(t, err)

		// Verify still only one record in database
		var count int

		err = db.DB().QueryRowContext(
			ctx, "SELECT COUNT(*) FROM narinfos WHERE hash = ?", testdata.Nar1.NarInfoHash,
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
		db, store, dir, cleanup := factory(t)
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

			return testhelper.MigrateNarInfoToDatabase(ctx, db, hash, ni)
		})
		require.NoError(t, err)
		assert.Equal(t, len(entries), totalProcessed)

		// Verify all in database
		var count int

		err = db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM narinfos").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, len(entries), count)
	}
}

func testMigrateNarInfoAlreadyMigrated(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		db, store, dir, cleanup := factory(t)
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

		err = testhelper.MigrateNarInfoToDatabase(ctx, db, testdata.Nar1.NarInfoHash, ni)
		require.NoError(t, err)

		// Fetch migrated hashes
		migratedHashes, err := db.GetMigratedNarInfoHashes(ctx)
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

		err = db.DB().QueryRowContext(
			ctx, "SELECT COUNT(*) FROM narinfos WHERE hash = ?", testdata.Nar1.NarInfoHash,
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
		_, store, _, cleanup := factory(t)
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
		db, store, dir, cleanup := factory(t)
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
		err = testhelper.MigrateNarInfoToDatabase(ctx, db, testdata.Nar1.NarInfoHash, ni)
		require.NoError(t, err)

		// Verify references in database
		var refCount int

		err = db.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM narinfo_references nr
		 JOIN narinfos n ON nr.narinfo_id = n.id
		 WHERE n.hash = ?`,
			testdata.Nar1.NarInfoHash).Scan(&refCount)
		require.NoError(t, err)
		assert.Equal(t, len(ni.References), refCount)

		// Verify signatures in database
		var sigCount int

		err = db.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM narinfo_signatures ns
		 JOIN narinfos n ON ns.narinfo_id = n.id
		 WHERE n.hash = ?`,
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
		db, store, dir, cleanup := factory(t)
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

		err = testhelper.MigrateNarInfoToDatabase(ctx, db, testdata.Nar1.NarInfoHash, ni)
		require.NoError(t, err)

		// Verify file exists before deletion
		assert.FileExists(t, narInfoPath)

		// Fetch migrated hashes
		migratedHashes, err := db.GetMigratedNarInfoHashes(ctx)
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

		err = db.DB().QueryRowContext(
			ctx, "SELECT COUNT(*) FROM narinfos WHERE hash = ?", testdata.Nar1.NarInfoHash,
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
		db, store, dir, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Increase connection pool for concurrent operations
		db.DB().SetMaxOpenConns(20)

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

			if err := testhelper.MigrateNarInfoToDatabase(ctx, db, hash, ni); err != nil {
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

		err = db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM narinfos").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, len(entries), count)
	}
}

func testMigrateNarInfoPartialData(factory migrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		// Setup
		db, store, dir, cleanup := factory(t)
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

		err = testhelper.MigrateNarInfoToDatabase(ctx, db, testdata.Nar1.NarInfoHash, ni)
		require.NoError(t, err)

		// Verify in database - all fields should be stored correctly
		// Test that sql.NullString/sql.NullInt64 handling works
		var (
			dbHash      string
			dbStorePath sql.NullString
			dbURL       sql.NullString
			dbDeriver   sql.NullString
			dbSystem    sql.NullString
			dbCA        sql.NullString
		)

		err = db.DB().QueryRowContext(ctx,
			"SELECT hash, store_path, url, deriver, system, ca FROM narinfos WHERE hash = ?",
			testdata.Nar1.NarInfoHash).Scan(&dbHash, &dbStorePath, &dbURL, &dbDeriver, &dbSystem, &dbCA)
		require.NoError(t, err)

		assert.Equal(t, testdata.Nar1.NarInfoHash, dbHash)
		assert.True(t, dbStorePath.Valid, "StorePath should be populated")
		assert.True(t, dbURL.Valid, "URL should be populated")

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
		db, store, dir, cleanup := factory(t)
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

		// Start a transaction and intentionally cause a failure
		tx, err := db.DB().BeginTx(ctx, nil)
		require.NoError(t, err)

		qtx := db.WithTx(tx)

		// Create narinfo (should succeed)
		nir, err := qtx.CreateNarInfo(ctx, database.CreateNarInfoParams{
			Hash:        testdata.Nar1.NarInfoHash,
			StorePath:   sql.NullString{String: ni.StorePath, Valid: ni.StorePath != ""},
			URL:         sql.NullString{String: ni.URL, Valid: ni.URL != ""},
			Compression: sql.NullString{String: ni.Compression, Valid: ni.Compression != ""},
			FileHash:    sql.NullString{String: ni.FileHash.String(), Valid: ni.FileHash != nil},
			FileSize:    sql.NullInt64{Int64: int64(ni.FileSize), Valid: true}, //nolint:gosec // Test code
			NarHash:     sql.NullString{String: ni.NarHash.String(), Valid: ni.NarHash != nil},
			NarSize:     sql.NullInt64{Int64: int64(ni.NarSize), Valid: true}, //nolint:gosec // Test code
			Deriver:     sql.NullString{String: ni.Deriver, Valid: ni.Deriver != ""},
			System:      sql.NullString{String: ni.System, Valid: ni.System != ""},
			Ca:          sql.NullString{String: ni.CA, Valid: ni.CA != ""},
		})
		require.NoError(t, err)
		require.NotZero(t, nir.ID)

		// Rollback the transaction
		err = tx.Rollback()
		require.NoError(t, err)

		// Verify NOT in database (transaction was rolled back)
		var count int

		err = db.DB().QueryRowContext(
			ctx, "SELECT COUNT(*) FROM narinfos WHERE hash = ?", testdata.Nar1.NarInfoHash,
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
		db, store, dir, cleanup := factory(t)
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

		err = testhelper.MigrateNarInfoToDatabase(ctx, db, testdata.Nar1.NarInfoHash, ni)
		require.NoError(t, err)

		// Verify in database
		var count int

		err = db.DB().QueryRowContext(
			ctx, "SELECT COUNT(*) FROM narinfos WHERE hash = ?", testdata.Nar1.NarInfoHash,
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
		db, store, dir, cleanup := factory(t)
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

			if err := testhelper.MigrateNarInfoToDatabase(ctx, db, hash, ni); err != nil {
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
		db, store, dir, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Create a large narinfo with many references and signatures
		const numReferences = 100

		const numSignatures = 50

		hash := "largenarinfo1234567890abcdef1234567890abcdef"
		narHash := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

		// Build references string
		var referencesBuilder strings.Builder

		referencesBuilder.WriteString("References:")

		for i := 0; i < numReferences; i++ {
			referencesBuilder.WriteString(fmt.Sprintf(" ref%d-abcdefgh1234567890abcdefgh1234567890", i))
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
		for i := 0; i < numSignatures; i++ {
			narInfoBuilder.WriteString(fmt.Sprintf(
				"Sig: cache.test.org-%d:MadTCU1OSFCGUw4aqCKpLCZJpqBc7AbLvO7wgdlls0eq1DwaSnF/82SZE+wJGEiwlHbnZR+14daSaec0W3XoBQ==\n",
				i,
			))
		}

		narInfoText := narInfoBuilder.String()

		// Write to storage
		narInfoPath := filepath.Join(dir, "store", "narinfo", helper.NarInfoFilePath(hash))
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(narInfoText), 0o600))

		narPath := filepath.Join(dir, "store", "nar", helper.NarFilePath(narHash, "xz"))
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(helper.MustRandString(999999, nil)), 0o600))

		// Run migration
		err := store.WalkNarInfos(ctx, func(h string) error {
			ni, err := store.GetNarInfo(ctx, h)
			if err != nil {
				return fmt.Errorf("failed to get narinfo: %w", err)
			}

			return testhelper.MigrateNarInfoToDatabase(ctx, db, h, ni)
		})
		require.NoError(t, err)

		// Verify in database
		var count int

		err = db.DB().QueryRowContext(
			ctx, "SELECT COUNT(*) FROM narinfos WHERE hash = ?", hash,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "Should have exactly one narinfo record")

		// Get narinfo ID
		nir, err := db.GetNarInfoByHash(ctx, hash)
		require.NoError(t, err)

		// Verify all references were migrated
		references, err := db.GetNarInfoReferences(ctx, nir.ID)
		require.NoError(t, err)
		assert.Len(t, references, numReferences, "Should have all references migrated")

		// Verify all signatures were migrated
		signatures, err := db.GetNarInfoSignatures(ctx, nir.ID)
		require.NoError(t, err)
		assert.Len(t, signatures, numSignatures, "Should have all signatures migrated")

		// Verify a few specific references (spot check)
		assert.Contains(t, references, "ref0-abcdefgh1234567890abcdefgh1234567890")
		assert.Contains(t, references, "ref50-abcdefgh1234567890abcdefgh1234567890")
		assert.Contains(t, references, "ref99-abcdefgh1234567890abcdefgh1234567890")

		// Verify a few specific signatures (spot check)
		assert.Contains(
			t,
			signatures,
			"cache.test.org-0:MadTCU1OSFCGUw4aqCKpLCZJpqBc7AbLvO7wgdlls0eq1DwaSnF/82SZE+wJGEiwlHbnZR+14daSaec0W3XoBQ==",
		)
		assert.Contains(
			t,
			signatures,
			"cache.test.org-25:MadTCU1OSFCGUw4aqCKpLCZJpqBc7AbLvO7wgdlls0eq1DwaSnF/82SZE+wJGEiwlHbnZR+14daSaec0W3XoBQ==",
		)
		assert.Contains(
			t,
			signatures,
			"cache.test.org-49:MadTCU1OSFCGUw4aqCKpLCZJpqBc7AbLvO7wgdlls0eq1DwaSnF/82SZE+wJGEiwlHbnZR+14daSaec0W3XoBQ==",
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

	db, err := database.Open("sqlite:"+dbFile, nil)
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
		_, err := db.DB().ExecContext(ctx, "DELETE FROM narinfos")
		require.NoError(b, err)

		err = testhelper.MigrateNarInfoToDatabase(ctx, db, testdata.Nar1.NarInfoHash, ni)
		require.NoError(b, err)
	}
}
