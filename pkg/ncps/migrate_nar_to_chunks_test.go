package ncps_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/ncps"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// narToChunksMigrationFactory is a function that returns a clean, ready-to-use database,
// local store, directory path, and database URL string for CLI commands,
// and takes care of cleaning up once the test is done.
type narToChunksMigrationFactory func(t *testing.T) (database.Querier, *local.Store, string, string, func())

// TestMigrateNarToChunksBackends runs all NAR-to-chunks migration tests against all supported database backends.
func TestMigrateNarToChunksBackends(t *testing.T) {
	t.Parallel()

	backends := []struct {
		name   string
		envVar string
		setup  narToChunksMigrationFactory
	}{
		{
			name:  "SQLite",
			setup: setupNarToChunksMigrationSQLite,
		},
		{
			name:   "PostgreSQL",
			envVar: "NCPS_TEST_ADMIN_POSTGRES_URL",
			setup:  setupNarToChunksMigrationPostgres,
		},
		{
			name:   "MySQL",
			envVar: "NCPS_TEST_ADMIN_MYSQL_URL",
			setup:  setupNarToChunksMigrationMySQL,
		},
	}

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			if b.envVar != "" && os.Getenv(b.envVar) == "" {
				t.Skipf("Skipping %s: %s not set", b.name, b.envVar)
			}

			runMigrateNarToChunksSuite(t, b.setup)
		})
	}
}

func runMigrateNarToChunksSuite(t *testing.T, factory narToChunksMigrationFactory) {
	t.Helper()

	t.Run("Success", testMigrateNarToChunksSuccess(factory))
	t.Run("UnmigratedNarInfoFailure", testMigrateNarToChunksUnmigratedNarInfoFailure(factory))
	t.Run("DryRun", testMigrateNarToChunksDryRun(factory))
	t.Run("Idempotency", testMigrateNarToChunksIdempotency(factory))
	t.Run("MultipleNARs", testMigrateNarToChunksMultipleNARs(factory))
	t.Run("Deduplication", testMigrateNarToChunksDeduplication(factory))
}

func testMigrateNarToChunksSuccess(factory narToChunksMigrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		// Setup
		ctx := zerolog.New(os.Stderr).WithContext(context.Background())
		db, _, dir, dbURL, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Pre-populate storage with NarInfo and NAR
		narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

		app, err := ncps.New()
		require.NoError(t, err)

		// 1. Migrate NarInfo to DB first (now required)
		migrateNarInfoArgs := []string{
			"ncps", "migrate-narinfo",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
		}
		require.NoError(t, app.Run(ctx, migrateNarInfoArgs))

		// 2. Run the migration command
		args := []string{
			"ncps", "migrate-nar-to-chunks",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--cache-cdc-enabled",
		}

		err = app.Run(ctx, args)
		require.NoError(t, err)

		// Verification
		// Chunks should be created in the database
		var count int

		err = db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&count)
		require.NoError(t, err)
		assert.Positive(t, count, "Chunks should have been created")

		// The NAR should be deleted from traditional storage
		assert.NoFileExists(t, narPath, "Original NAR should have been deleted")
	}
}

func testMigrateNarToChunksUnmigratedNarInfoFailure(factory narToChunksMigrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		// Setup
		ctx := zerolog.New(os.Stderr).WithContext(context.Background())
		_, _, dir, dbURL, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Pre-populate storage with NarInfo (NOT migrated to DB)
		narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

		// Run the migration command - should fail because of unmigrated NarInfo
		args := []string{
			"ncps", "migrate-nar-to-chunks",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--cache-cdc-enabled",
		}

		app, err := ncps.New()
		require.NoError(t, err)

		err = app.Run(ctx, args)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmigrated narinfos")
		assert.Contains(t, err.Error(), "run 'migrate-narinfo' first")
	}
}

func testMigrateNarToChunksDryRun(factory narToChunksMigrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		// Setup
		ctx := zerolog.New(os.Stderr).WithContext(context.Background())
		db, _, dir, dbURL, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Pre-populate storage with NarInfo and NAR
		narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

		app, err := ncps.New()
		require.NoError(t, err)

		// Migrate NarInfo to DB first
		// Use --concurrency=1 to avoid MySQL deadlocks when migrating multiple narinfos in parallel
		migrateNarInfoArgs := []string{
			"ncps", "migrate-narinfo",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--concurrency", "1",
		}
		require.NoError(t, app.Run(ctx, migrateNarInfoArgs))

		// Run command with --dry-run
		args := []string{
			"ncps", "migrate-nar-to-chunks",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--cache-cdc-enabled",
			"--dry-run",
		}

		err = app.Run(ctx, args)
		require.NoError(t, err)

		// Verification
		var count int

		err = db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count, "No chunks should have been created in dry-run")

		assert.FileExists(t, narPath, "Original NAR should NOT have been deleted in dry-run")
	}
}

func testMigrateNarToChunksIdempotency(factory narToChunksMigrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		// Setup
		ctx := zerolog.New(os.Stderr).WithContext(context.Background())
		db, _, dir, dbURL, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Pre-populate storage with NarInfo and NAR
		narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

		app, err := ncps.New()
		require.NoError(t, err)

		// Migrate NarInfo to DB first
		// Use --concurrency=1 to avoid MySQL deadlocks when migrating multiple narinfos in parallel
		migrateNarInfoArgs := []string{
			"ncps", "migrate-narinfo",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--concurrency", "1",
		}
		require.NoError(t, app.Run(ctx, migrateNarInfoArgs))

		args := []string{
			"ncps", "migrate-nar-to-chunks",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--cache-cdc-enabled",
		}

		// Run command first time
		err = app.Run(ctx, args)
		require.NoError(t, err)

		// Verify chunks created
		var count1 int

		err = db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&count1)
		require.NoError(t, err)
		assert.Positive(t, count1)

		// Run command second time
		// The NAR is already deleted, but the command should still pass (skipping already chunked/non-existent NARs)
		// MigrateNarToChunks checks hasNarInChunks first
		err = app.Run(ctx, args)
		require.NoError(t, err)

		// Verify chunks count remains same
		var count2 int

		err = db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&count2)
		require.NoError(t, err)
		assert.Equal(t, count1, count2, "Chunks count should remain same after second run")
	}
}

func testMigrateNarToChunksDeduplication(factory narToChunksMigrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		// Setup
		ctx := zerolog.New(os.Stderr).WithContext(context.Background())
		db, _, dir, dbURL, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Create two narinfos pointing to the same NAR URL
		narInfo1Text := testdata.Nar1.NarInfoText
		// Use Nar2 but change its URL to Nar1's URL
		narInfo2Text := strings.Replace(
			testdata.Nar2.NarInfoText,
			"URL: nar/"+testdata.Nar2.NarHash+".nar.xz",
			"URL: nar/"+testdata.Nar1.NarHash+".nar.xz",
			1,
		)

		narInfo1Path := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfo1Path), 0o755))
		require.NoError(t, os.WriteFile(narInfo1Path, []byte(narInfo1Text), 0o600))

		narInfo2Path := filepath.Join(dir, "store", "narinfo", testdata.Nar2.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfo2Path), 0o755))
		require.NoError(t, os.WriteFile(narInfo2Path, []byte(narInfo2Text), 0o600))

		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

		app, err := ncps.New()
		require.NoError(t, err)

		// 1. Migrate NarInfo to DB first
		migrateNarInfoArgs := []string{
			"ncps", "migrate-narinfo",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
		}
		require.NoError(t, app.Run(ctx, migrateNarInfoArgs))

		// Verify we have 2 narinfos but 1 nar_file
		var niCount, nfCount int

		err = db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM narinfos").Scan(&niCount)
		require.NoError(t, err)
		assert.Equal(t, 2, niCount)

		err = db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM nar_files").Scan(&nfCount)
		require.NoError(t, err)
		assert.Equal(t, 1, nfCount)

		// 2. Run the migration command
		args := []string{
			"ncps", "migrate-nar-to-chunks",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--cache-cdc-enabled",
		}

		err = app.Run(ctx, args)
		require.NoError(t, err)

		// Verification
		// Chunks should be created for ONE file
		var chunkCount int

		err = db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&chunkCount)
		require.NoError(t, err)
		assert.Positive(t, chunkCount, "Chunks should have been created")

		// The NAR file record should have total_chunks > 0
		var totalChunks int

		err = db.DB().QueryRowContext(ctx, "SELECT total_chunks FROM nar_files LIMIT 1").Scan(&totalChunks)
		require.NoError(t, err)
		assert.Positive(t, totalChunks)
	}
}

func testMigrateNarToChunksMultipleNARs(factory narToChunksMigrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		// Setup
		ctx := zerolog.New(os.Stderr).WithContext(context.Background())
		db, _, dir, dbURL, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Pre-populate storage with multiple NARs
		entries := []testdata.Entry{testdata.Nar1, testdata.Nar2, testdata.Nar3}
		for _, entry := range entries {
			narInfoPath := filepath.Join(dir, "store", "narinfo", entry.NarInfoPath)
			require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
			require.NoError(t, os.WriteFile(narInfoPath, []byte(entry.NarInfoText), 0o600))

			narPath := filepath.Join(dir, "store", "nar", entry.NarPath)
			require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
			require.NoError(t, os.WriteFile(narPath, []byte(entry.NarText), 0o600))
		}

		app, err := ncps.New()
		require.NoError(t, err)

		// Migrate NarInfo to DB first
		// Use --concurrency=1 to avoid MySQL deadlocks when migrating multiple narinfos in parallel
		migrateNarInfoArgs := []string{
			"ncps", "migrate-narinfo",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--concurrency", "1",
		}
		require.NoError(t, app.Run(ctx, migrateNarInfoArgs))

		args := []string{
			"ncps", "migrate-nar-to-chunks",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--cache-cdc-enabled",
		}

		// Run command
		err = app.Run(ctx, args)
		require.NoError(t, err)

		// Verification
		var count int

		err = db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&count)
		require.NoError(t, err)
		assert.Positive(t, count)

		for _, entry := range entries {
			narPath := filepath.Join(dir, "store", "nar", entry.NarPath)
			assert.NoFileExists(t, narPath, "NAR %s should have been deleted", entry.NarPath)
		}
	}
}

func setupNarToChunksMigrationSQLite(t *testing.T) (database.Querier, *local.Store, string, string, func()) {
	t.Helper()

	ctx := context.Background()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

	dbURL := "sqlite:" + dbFile

	cleanup := func() {
		db.DB().Close()
	}

	return db, store, dir, dbURL, cleanup
}

func setupNarToChunksMigrationPostgres(t *testing.T) (database.Querier, *local.Store, string, string, func()) {
	t.Helper()

	ctx := context.Background()
	dir := t.TempDir()

	db, dbURL, dbCleanup := testhelper.SetupPostgres(t)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

	cleanup := func() {
		dbCleanup()
	}

	return db, store, dir, dbURL, cleanup
}

func setupNarToChunksMigrationMySQL(t *testing.T) (database.Querier, *local.Store, string, string, func()) {
	t.Helper()

	ctx := context.Background()
	dir := t.TempDir()

	db, dbURL, dbCleanup := testhelper.SetupMySQL(t)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

	cleanup := func() {
		dbCleanup()
	}

	return db, store, dir, dbURL, cleanup
}
