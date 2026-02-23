package ncps_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	localstorage "github.com/kalbasit/ncps/pkg/storage/local"
	narinfopkg "github.com/nix-community/go-nix/pkg/narinfo"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/ncps"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// fsckSetupFn returns (db, localStore, storageDir, dbURL, cleanup).
type fsckSetupFn func(t *testing.T) (database.Querier, *localstorage.Store, string, string, func())

func TestFsckBackends(t *testing.T) {
	t.Parallel()

	backends := []struct {
		name   string
		envVar string
		setup  fsckSetupFn
	}{
		{
			name:  "SQLite",
			setup: setupFsckSQLite,
		},
		{
			name:   "PostgreSQL",
			envVar: "NCPS_TEST_ADMIN_POSTGRES_URL",
			setup:  setupFsckPostgres,
		},
		{
			name:   "MySQL",
			envVar: "NCPS_TEST_ADMIN_MYSQL_URL",
			setup:  setupFsckMySQL,
		},
	}

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			if b.envVar != "" && os.Getenv(b.envVar) == "" {
				t.Skipf("Skipping %s: %s not set", b.name, b.envVar)
			}

			runFsckSuite(t, b.setup)
		})
	}
}

func runFsckSuite(t *testing.T, setup fsckSetupFn) {
	t.Helper()

	t.Run("Clean", testFsckClean(setup))
	t.Run("NarInfosWithoutNarFiles", testFsckNarInfosWithoutNarFiles(setup))
	t.Run("OrphanedNarFilesInDB", testFsckOrphanedNarFilesInDB(setup))
	t.Run("NarFileMissingInStorage", testFsckNarFileMissingInStorage(setup))
	t.Run("NarFileMissingInStorageCascadeRepair", testFsckNarFileMissingCascadeRepair(setup))
	t.Run("OrphanedNarInStorage", testFsckOrphanedNarInStorage(setup))
	t.Run("Repair", testFsckRepair(setup))
	t.Run("DryRun", testFsckDryRun(setup))
}

// testFsckClean verifies that a clean (consistent) state results in 0 issues.
func testFsckClean(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		db, store, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Seed a fully consistent narinfo+narfile in DB and storage.
		writeFsckNar1ToStorage(t, dir)

		ni := getFsckNarInfo(ctx, t, store, testdata.Nar1.NarInfoHash)

		require.NoError(t, testhelper.MigrateNarInfoToDatabase(ctx, db, testdata.Nar1.NarInfoHash, ni))

		app, err := ncps.New()
		require.NoError(t, err)

		args := []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
		}

		require.NoError(t, app.Run(ctx, args))
	}
}

// testFsckNarInfosWithoutNarFiles verifies narinfos with no nar_file link are detected.
func testFsckNarInfosWithoutNarFiles(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		db, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Insert a narinfo in DB with a URL but with no linked nar_file.
		// UpdateNarInfo requires the narinfo to already exist; we use CreateNarInfo directly.
		ni := parseFsckNarInfoText(t, testdata.Nar1.NarInfoText)

		// Create narinfo in DB with a URL (migrated) but not yet linked to nar_file.
		_, err := db.CreateNarInfo(ctx, database.CreateNarInfoParams{
			Hash:        testdata.Nar1.NarInfoHash,
			StorePath:   sql.NullString{String: ni.StorePath, Valid: ni.StorePath != ""},
			URL:         sql.NullString{String: ni.URL, Valid: ni.URL != ""},
			Compression: sql.NullString{String: ni.Compression, Valid: ni.Compression != ""},
			NarHash:     sql.NullString{String: ni.NarHash.String(), Valid: ni.NarHash != nil},
			NarSize:     sql.NullInt64{Int64: int64(ni.NarSize), Valid: true}, //nolint:gosec
			FileHash:    sql.NullString{String: ni.FileHash.String(), Valid: ni.FileHash != nil},
			FileSize:    sql.NullInt64{Int64: int64(ni.FileSize), Valid: true}, //nolint:gosec
			Deriver:     sql.NullString{String: ni.Deriver, Valid: ni.Deriver != ""},
			System:      sql.NullString{String: ni.System, Valid: ni.System != ""},
			Ca:          sql.NullString{String: ni.CA, Valid: ni.CA != ""},
		})
		require.NoError(t, err)

		app, err := ncps.New()
		require.NoError(t, err)

		args := []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--dry-run",
		}

		err = app.Run(ctx, args)
		assert.ErrorIs(t, err, ncps.ErrFsckIssuesFound)
	}
}

// testFsckOrphanedNarFilesInDB verifies nar_files with no narinfo link are detected.
func testFsckOrphanedNarFilesInDB(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		db, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Create a nar_file in DB with no linked narinfo.
		narURL := nar.URL{
			Hash:        testdata.Nar1.NarHash,
			Compression: testdata.Nar1.NarCompression,
		}

		_, err := db.CreateNarFile(ctx, database.CreateNarFileParams{
			Hash:        narURL.Hash,
			Compression: narURL.Compression.String(),
			Query:       narURL.Query.Encode(),
			FileSize:    uint64(len(testdata.Nar1.NarText)),
		})
		require.NoError(t, err)

		// Also write the physical file so it doesn't show up as missing-in-storage.
		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

		app, err := ncps.New()
		require.NoError(t, err)

		args := []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--dry-run",
		}

		err = app.Run(ctx, args)
		assert.ErrorIs(t, err, ncps.ErrFsckIssuesFound)
	}
}

// testFsckNarFileMissingInStorage verifies nar_file DB records without a physical file are detected.
func testFsckNarFileMissingInStorage(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		db, store, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Write narinfo+nar to storage and fully migrate to DB.
		writeFsckNar1ToStorage(t, dir)

		ni := getFsckNarInfo(ctx, t, store, testdata.Nar1.NarInfoHash)

		require.NoError(t, testhelper.MigrateNarInfoToDatabase(ctx, db, testdata.Nar1.NarInfoHash, ni))

		// Delete the physical NAR file to simulate missing storage.
		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.Remove(narPath))

		app, err := ncps.New()
		require.NoError(t, err)

		args := []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--dry-run",
		}

		err = app.Run(ctx, args)
		assert.ErrorIs(t, err, ncps.ErrFsckIssuesFound)
	}
}

// testFsckNarFileMissingCascadeRepair verifies that repairing a nar_file missing from storage
// also cleans up the parent narinfo in a single pass (no second run required).
func testFsckNarFileMissingCascadeRepair(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		db, store, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Seed a fully consistent narinfo+narfile in DB and storage.
		writeFsckNar1ToStorage(t, dir)

		ni := getFsckNarInfo(ctx, t, store, testdata.Nar1.NarInfoHash)

		require.NoError(t, testhelper.MigrateNarInfoToDatabase(ctx, db, testdata.Nar1.NarInfoHash, ni))

		// Delete the physical NAR file to simulate missing storage.
		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.Remove(narPath))

		app, err := ncps.New()
		require.NoError(t, err)

		// First run: repair the missing nar_file (and its now-orphaned narinfo).
		repairArgs := []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--repair",
		}

		require.NoError(t, app.Run(ctx, repairArgs))

		// Second run without --repair: must find 0 issues (repair was complete in one pass).
		cleanArgs := []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
		}

		require.NoError(t, app.Run(ctx, cleanArgs))
	}
}

// testFsckOrphanedNarInStorage verifies NAR files in storage with no DB record are detected.
func testFsckOrphanedNarInStorage(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		_, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Write a NAR file to storage without any DB record.
		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
		require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

		app, err := ncps.New()
		require.NoError(t, err)

		args := []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--dry-run",
		}

		err = app.Run(ctx, args)
		assert.ErrorIs(t, err, ncps.ErrFsckIssuesFound)
	}
}

// testFsckRepair verifies that --repair fixes detected issues and a second run is clean.
func testFsckRepair(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		db, store, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Seed a consistent entry.
		writeFsckNar1ToStorage(t, dir)

		ni := getFsckNarInfo(ctx, t, store, testdata.Nar1.NarInfoHash)

		require.NoError(t, testhelper.MigrateNarInfoToDatabase(ctx, db, testdata.Nar1.NarInfoHash, ni))

		// Break it: delete the physical NAR file.
		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.Remove(narPath))

		// Also seed an orphaned NAR in storage (Nar2 has no DB record).
		orphanPath := filepath.Join(dir, "store", "nar", testdata.Nar2.NarPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(orphanPath), 0o755))
		require.NoError(t, os.WriteFile(orphanPath, []byte(testdata.Nar2.NarText), 0o600))

		app, err := ncps.New()
		require.NoError(t, err)

		// First run: repair.
		args := []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--repair",
		}

		require.NoError(t, app.Run(ctx, args))

		// Second run: should be clean.
		require.NoError(t, app.Run(ctx, args))
	}
}

// testFsckDryRun verifies that --dry-run reports issues but does not change anything.
func testFsckDryRun(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		db, store, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Seed a consistent entry.
		writeFsckNar1ToStorage(t, dir)

		ni := getFsckNarInfo(ctx, t, store, testdata.Nar1.NarInfoHash)

		require.NoError(t, testhelper.MigrateNarInfoToDatabase(ctx, db, testdata.Nar1.NarInfoHash, ni))

		// Break: delete physical NAR file.
		narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
		require.NoError(t, os.Remove(narPath))

		app, err := ncps.New()
		require.NoError(t, err)

		// Dry-run: should report issues.
		args := []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--dry-run",
		}

		err = app.Run(ctx, args)
		require.ErrorIs(t, err, ncps.ErrFsckIssuesFound)

		// Verify DB record is still there (dry-run should not delete it).
		_, dbErr := db.GetNarInfoByHash(ctx, testdata.Nar1.NarInfoHash)
		assert.NoError(t, dbErr, "dry-run should not delete DB records")
	}
}

// setupFsckSQLite creates a SQLite-backed test environment.
func setupFsckSQLite(t *testing.T) (database.Querier, *localstorage.Store, string, string, func()) {
	t.Helper()

	ctx := context.Background()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := localstorage.New(ctx, dir)
	require.NoError(t, err)

	cleanup := func() {
		db.DB().Close()
	}

	return db, store, dir, "sqlite:" + dbFile, cleanup
}

// setupFsckPostgres creates a PostgreSQL-backed test environment.
func setupFsckPostgres(t *testing.T) (database.Querier, *localstorage.Store, string, string, func()) {
	t.Helper()

	ctx := context.Background()
	dir := t.TempDir()

	db, dbURL, dbCleanup := testhelper.SetupPostgres(t)

	store, err := localstorage.New(ctx, dir)
	require.NoError(t, err)

	return db, store, dir, dbURL, dbCleanup
}

// setupFsckMySQL creates a MySQL-backed test environment.
func setupFsckMySQL(t *testing.T) (database.Querier, *localstorage.Store, string, string, func()) {
	t.Helper()

	ctx := context.Background()
	dir := t.TempDir()

	db, dbURL, dbCleanup := testhelper.SetupMySQL(t)

	store, err := localstorage.New(ctx, dir)
	require.NoError(t, err)

	return db, store, dir, dbURL, dbCleanup
}

// writeFsckNar1ToStorage writes Nar1's narinfo and NAR file to the storage directory.
func writeFsckNar1ToStorage(t *testing.T, dir string) {
	t.Helper()

	narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

	narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
	require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))
}

// getFsckNarInfo reads a narinfo from local storage.
func getFsckNarInfo(ctx context.Context, t *testing.T, store *localstorage.Store, hash string) *narinfopkg.NarInfo {
	t.Helper()

	ni, err := store.GetNarInfo(ctx, hash)
	require.NoError(t, err)

	return ni
}

// parseFsckNarInfoText parses a narinfo text string into a NarInfo struct.
func parseFsckNarInfoText(t *testing.T, text string) *narinfopkg.NarInfo {
	t.Helper()

	ni, err := narinfopkg.Parse(strings.NewReader(text))
	require.NoError(t, err)

	return ni
}
