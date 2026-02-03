package ncps_test

import (
	"context"
	"os"
	"path/filepath"
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
	t.Run("DryRun", testMigrateNarToChunksDryRun(factory))
	t.Run("Idempotency", testMigrateNarToChunksIdempotency(factory))
	t.Run("MultipleNARs", testMigrateNarToChunksMultipleNARs(factory))
}

func testMigrateNarToChunksSuccess(factory narToChunksMigrationFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		// Setup
		ctx := zerolog.New(os.Stderr).WithContext(context.Background())
		db, _, dir, dbURL, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Pre-populate storage with NarInfo and NAR (unmigrated in DB)
		narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
		require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

		// Run the migration command
		args := []string{
			"ncps", "migrate-nar-to-chunks",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--cache-cdc-enabled",
		}

		app, err := ncps.New()
		require.NoError(t, err)

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

		// Run command with --dry-run
		args := []string{
			"ncps", "migrate-nar-to-chunks",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--cache-cdc-enabled",
			"--dry-run",
		}

		app, err := ncps.New()
		require.NoError(t, err)

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

		args := []string{
			"ncps", "migrate-nar-to-chunks",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--cache-cdc-enabled",
		}

		app, err := ncps.New()
		require.NoError(t, err)

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

		args := []string{
			"ncps", "migrate-nar-to-chunks",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--cache-cdc-enabled",
		}

		app, err := ncps.New()
		require.NoError(t, err)

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
