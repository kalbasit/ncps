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

	locklocal "github.com/kalbasit/ncps/pkg/lock/local"
	chunkstore "github.com/kalbasit/ncps/pkg/storage/chunk"
	localstorage "github.com/kalbasit/ncps/pkg/storage/local"
	narinfopkg "github.com/nix-community/go-nix/pkg/narinfo"

	"github.com/kalbasit/ncps/pkg/config"
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
	t.Run("CDC", testFsckCDCSuite(setup))
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

// configureFsckCDCInDatabase sets up CDC configuration in the database for fsck tests.
func configureFsckCDCInDatabase(ctx context.Context, t *testing.T, db database.Querier) {
	t.Helper()

	rwLocker := locklocal.NewRWLocker()
	cfg := config.New(db, rwLocker)

	require.NoError(t, cfg.SetCDCEnabled(ctx, "true"), "failed to set CDC enabled")
	require.NoError(t, cfg.SetCDCMin(ctx, "16384"), "failed to set CDC min")
	require.NoError(t, cfg.SetCDCAvg(ctx, "65536"), "failed to set CDC avg")
	require.NoError(t, cfg.SetCDCMax(ctx, "262144"), "failed to set CDC max")
}

// setupFsckCDCNarFile creates a narinfo + nar_file + numChunks chunks in DB and chunk storage.
// Returns the created NarFile.
func setupFsckCDCNarFile(
	ctx context.Context,
	t *testing.T,
	db database.Querier,
	cs chunkstore.Store,
	narInfoHash string,
	narInfoText string,
	narHash string,
	narCompression nar.CompressionType,
	numChunks int,
) database.NarFile {
	t.Helper()

	ni := parseFsckNarInfoText(t, narInfoText)

	// Create narinfo in DB.
	narInfo, err := db.CreateNarInfo(ctx, database.CreateNarInfoParams{
		Hash:        narInfoHash,
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

	// Create nar_file in DB.
	narFile, err := db.CreateNarFile(ctx, database.CreateNarFileParams{
		Hash:        narHash,
		Compression: narCompression.String(),
		Query:       "",
		FileSize:    uint64(len(narInfoText)),
		TotalChunks: int64(numChunks),
	})
	require.NoError(t, err)

	// Link narinfo to nar_file.
	require.NoError(t, db.LinkNarInfoToNarFile(ctx, database.LinkNarInfoToNarFileParams{
		NarInfoID: narInfo.ID,
		NarFileID: narFile.ID,
	}))

	// Create chunks in DB and storage.
	chunkIDs := make([]int64, numChunks)
	chunkIndexes := make([]int64, numChunks)

	for i := range numChunks {
		// Use a simple deterministic hash: replace last char with a letter based on index.
		chunkHash := narHash[:len(narHash)-1] + string(rune('a'+i))

		data := []byte(strings.Repeat("x", 64) + string(rune(i)))
		_, _, err := cs.PutChunk(ctx, chunkHash, data)
		require.NoError(t, err, "failed to put chunk %d", i)

		chunk, err := db.CreateChunk(ctx, database.CreateChunkParams{
			Hash:           chunkHash,
			Size:           uint32(len(data)),     //nolint:gosec
			CompressedSize: uint32(len(data) / 2), //nolint:gosec
		})
		require.NoError(t, err)

		chunkIDs[i] = chunk.ID
		chunkIndexes[i] = int64(i)
	}

	require.NoError(t, db.LinkNarFileToChunks(ctx, database.LinkNarFileToChunksParams{
		NarFileID:  narFile.ID,
		ChunkID:    chunkIDs,
		ChunkIndex: chunkIndexes,
	}))

	return narFile
}

// testFsckCDCSuite returns a test function that runs all CDC fsck tests.
func testFsckCDCSuite(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		t.Run("Clean", testFsckCDCClean(setup))
		t.Run("NarFileChunkCountMismatch", testFsckCDCNarFileChunkCountMismatch(setup))
		t.Run("ChunkMissingFromStorage", testFsckCDCChunkMissingFromStorage(setup))
		t.Run("OrphanedChunkInStorage", testFsckCDCOrphanedChunkInStorage(setup))
		t.Run("RepairIncompleteNar", testFsckCDCRepairIncompleteNar(setup))
		t.Run("RepairOrphanedChunkInStorage", testFsckCDCRepairOrphanedChunkInStorage(setup))
	}
}

// testFsckCDCClean verifies a fully-consistent CDC state produces 0 issues.
func testFsckCDCClean(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		db, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, db)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		setupFsckCDCNarFile(ctx, t, db, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 2)

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

// testFsckCDCNarFileChunkCountMismatch verifies that a nar_file with fewer chunks than total_chunks is detected.
func testFsckCDCNarFileChunkCountMismatch(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		db, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, db)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// Create nar_file with total_chunks=2 but only link 1 chunk.
		narFile := setupFsckCDCNarFile(ctx, t, db, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 1)

		// Force total_chunks to 2 while only 1 chunk is linked.
		require.NoError(t, db.UpdateNarFileTotalChunks(ctx, database.UpdateNarFileTotalChunksParams{
			TotalChunks: 2,
			FileSize:    narFile.FileSize,
			ID:          narFile.ID,
		}))

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

// testFsckCDCChunkMissingFromStorage verifies that a nar_file with a chunk missing from storage is detected.
func testFsckCDCChunkMissingFromStorage(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		db, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, db)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// Create nar_file with 2 chunks.
		setupFsckCDCNarFile(ctx, t, db, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 2)

		// Delete one chunk file from storage.
		allChunks, err := db.GetAllChunks(ctx)
		require.NoError(t, err)
		require.Len(t, allChunks, 2)

		require.NoError(t, cs.DeleteChunk(ctx, allChunks[0].Hash))

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

// testFsckCDCOrphanedChunkInStorage verifies that a chunk file in storage with no DB record is detected.
func testFsckCDCOrphanedChunkInStorage(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		db, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, db)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// Write a chunk file directly to storage without any DB record.
		orphanHash := testdata.Nar1.NarHash[:len(testdata.Nar1.NarHash)-1] + "z"
		_, _, err = cs.PutChunk(ctx, orphanHash, []byte("orphan chunk data"))
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

// testFsckCDCRepairIncompleteNar verifies that repair removes a broken nar_file, its narinfo,
// and orphaned chunks; and that a second run is clean. Also verifies ref-counting: a chunk
// shared with another nar_file is NOT deleted.
func testFsckCDCRepairIncompleteNar(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		db, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, db)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// Create Nar1 with 2 chunks.
		setupFsckCDCNarFile(ctx, t, db, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 2)

		// Get the chunks for Nar1.
		nar1Chunks, err := db.GetAllChunks(ctx)
		require.NoError(t, err)
		require.Len(t, nar1Chunks, 2)

		// Create Nar2 sharing one chunk with Nar1.
		nar2File, err := db.CreateNarFile(ctx, database.CreateNarFileParams{
			Hash:        testdata.Nar2.NarHash,
			Compression: testdata.Nar2.NarCompression.String(),
			Query:       "",
			FileSize:    uint64(len(testdata.Nar2.NarText)),
			TotalChunks: 1,
		})
		require.NoError(t, err)

		ni2 := parseFsckNarInfoText(t, testdata.Nar2.NarInfoText)
		narInfo2, err := db.CreateNarInfo(ctx, database.CreateNarInfoParams{
			Hash:        testdata.Nar2.NarInfoHash,
			StorePath:   sql.NullString{String: ni2.StorePath, Valid: ni2.StorePath != ""},
			URL:         sql.NullString{String: ni2.URL, Valid: ni2.URL != ""},
			Compression: sql.NullString{String: ni2.Compression, Valid: ni2.Compression != ""},
			NarHash:     sql.NullString{String: ni2.NarHash.String(), Valid: ni2.NarHash != nil},
			NarSize:     sql.NullInt64{Int64: int64(ni2.NarSize), Valid: true}, //nolint:gosec
			FileHash:    sql.NullString{String: ni2.FileHash.String(), Valid: ni2.FileHash != nil},
			FileSize:    sql.NullInt64{Int64: int64(ni2.FileSize), Valid: true}, //nolint:gosec
			Deriver:     sql.NullString{String: ni2.Deriver, Valid: ni2.Deriver != ""},
			System:      sql.NullString{String: ni2.System, Valid: ni2.System != ""},
			Ca:          sql.NullString{String: ni2.CA, Valid: ni2.CA != ""},
		})
		require.NoError(t, err)

		require.NoError(t, db.LinkNarInfoToNarFile(ctx, database.LinkNarInfoToNarFileParams{
			NarInfoID: narInfo2.ID,
			NarFileID: nar2File.ID,
		}))

		// Link Nar2 to the first chunk of Nar1 (shared chunk).
		sharedChunk := nar1Chunks[0]

		require.NoError(t, db.LinkNarFileToChunks(ctx, database.LinkNarFileToChunksParams{
			NarFileID:  nar2File.ID,
			ChunkID:    []int64{sharedChunk.ID},
			ChunkIndex: []int64{0},
		}))

		// Delete chunk[1] of Nar1 from storage → Nar1 becomes broken.
		brokenChunk := nar1Chunks[1]
		require.NoError(t, cs.DeleteChunk(ctx, brokenChunk.Hash))

		app, err := ncps.New()
		require.NoError(t, err)

		repairArgs := []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--repair",
		}

		require.NoError(t, app.Run(ctx, repairArgs))

		// Verify Nar1 narinfo and nar_file are deleted.
		_, err = db.GetNarInfoByHash(ctx, testdata.Nar1.NarInfoHash)
		assert.True(t, database.IsNotFoundError(err), "Nar1 narinfo should be deleted")

		_, err = db.GetNarFileByHashAndCompressionAndQuery(ctx, database.GetNarFileByHashAndCompressionAndQueryParams{
			Hash:        testdata.Nar1.NarHash,
			Compression: testdata.Nar1.NarCompression.String(),
			Query:       "",
		})
		assert.True(t, database.IsNotFoundError(err), "Nar1 nar_file should be deleted")

		// Verify the broken chunk (chunk[1]) is removed from DB and storage.
		_, err = db.GetChunkByHash(ctx, brokenChunk.Hash)
		assert.True(t, database.IsNotFoundError(err), "broken chunk should be deleted from DB")

		exists, err := cs.HasChunk(ctx, brokenChunk.Hash)
		require.NoError(t, err)
		assert.False(t, exists, "broken chunk file should not exist in storage")

		// Verify Nar2 and its shared chunk still exist.
		_, err = db.GetNarInfoByHash(ctx, testdata.Nar2.NarInfoHash)
		require.NoError(t, err, "Nar2 narinfo should still exist")

		exists, err = cs.HasChunk(ctx, sharedChunk.Hash)
		require.NoError(t, err)
		assert.True(t, exists, "shared chunk should still exist in storage")

		// Second run: should be clean.
		cleanArgs := []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
		}

		require.NoError(t, app.Run(ctx, cleanArgs))
	}
}

// testFsckCDCRepairOrphanedChunkInStorage verifies that an orphaned chunk file in storage is removed.
func testFsckCDCRepairOrphanedChunkInStorage(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		db, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, db)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// Write an orphaned chunk to storage (no DB record).
		orphanHash := testdata.Nar1.NarHash[:len(testdata.Nar1.NarHash)-1] + "z"
		_, _, err = cs.PutChunk(ctx, orphanHash, []byte("orphan chunk data"))
		require.NoError(t, err)

		app, err := ncps.New()
		require.NoError(t, err)

		repairArgs := []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--repair",
		}

		require.NoError(t, app.Run(ctx, repairArgs))

		// Chunk file should be gone from storage.
		exists, err := cs.HasChunk(ctx, orphanHash)
		require.NoError(t, err)
		assert.False(t, exists, "orphaned chunk should be removed from storage")

		// Second run: should be clean.
		cleanArgs := []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
		}

		require.NoError(t, app.Run(ctx, cleanArgs))
	}
}
