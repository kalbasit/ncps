package ncps_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zeebo/blake3"

	entchunk "github.com/kalbasit/ncps/ent/chunk"
	entnarfile "github.com/kalbasit/ncps/ent/narfile"
	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"
	locklocal "github.com/kalbasit/ncps/pkg/lock/local"
	chunkstore "github.com/kalbasit/ncps/pkg/storage/chunk"
	localstorage "github.com/kalbasit/ncps/pkg/storage/local"
	narinfopkg "github.com/nix-community/go-nix/pkg/narinfo"
	nixbase32 "github.com/nix-community/go-nix/pkg/nixbase32"

	"github.com/kalbasit/ncps/ent"
	"github.com/kalbasit/ncps/pkg/config"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/ncps"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// fsckSetupFn returns (dbClient, localStore, storageDir, dbURL, cleanup).
type fsckSetupFn func(t *testing.T) (*database.Client, *localstorage.Store, string, string, func())

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
	t.Run("VerifiedSince", testFsckVerifiedSince(setup))
	t.Run("CDC", testFsckCDCSuite(setup))
}

// testFsckClean verifies that a clean (consistent) state results in 0 issues.
func testFsckClean(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		dbClient, store, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Seed a fully consistent narinfo+narfile in DB and storage.
		writeFsckNar1ToStorage(t, dir)

		ni := getFsckNarInfo(ctx, t, store, testdata.Nar1.NarInfoHash)

		require.NoError(t, testhelper.MigrateNarInfoToDatabase(ctx, dbClient, testdata.Nar1.NarInfoHash, ni))

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

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Insert a narinfo in DB with a URL but with no linked nar_file.
		ni := parseFsckNarInfoText(t, testdata.Nar1.NarInfoText)

		// Create narinfo in DB with a URL (migrated) but not yet linked to nar_file.
		_, err := createNarInfoFromParsed(ctx, dbClient, testdata.Nar1.NarInfoHash, ni)
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

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Create a nar_file in DB with no linked narinfo.
		narURL := nar.URL{
			Hash:        testdata.Nar1.NarHash,
			Compression: testdata.Nar1.NarCompression,
		}

		_, err := dbClient.Ent().NarFile.Create().
			SetHash(narURL.Hash).
			SetCompression(narURL.Compression.String()).
			SetQuery(narURL.Query.Encode()).
			SetFileSize(uint64(len(testdata.Nar1.NarText))).
			Save(ctx)
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

		dbClient, store, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Write narinfo+nar to storage and fully migrate to DB.
		writeFsckNar1ToStorage(t, dir)

		ni := getFsckNarInfo(ctx, t, store, testdata.Nar1.NarInfoHash)

		require.NoError(t, testhelper.MigrateNarInfoToDatabase(ctx, dbClient, testdata.Nar1.NarInfoHash, ni))

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

		dbClient, store, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Seed a fully consistent narinfo+narfile in DB and storage.
		writeFsckNar1ToStorage(t, dir)

		ni := getFsckNarInfo(ctx, t, store, testdata.Nar1.NarInfoHash)

		require.NoError(t, testhelper.MigrateNarInfoToDatabase(ctx, dbClient, testdata.Nar1.NarInfoHash, ni))

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

		dbClient, store, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Seed a consistent entry.
		writeFsckNar1ToStorage(t, dir)

		ni := getFsckNarInfo(ctx, t, store, testdata.Nar1.NarInfoHash)

		require.NoError(t, testhelper.MigrateNarInfoToDatabase(ctx, dbClient, testdata.Nar1.NarInfoHash, ni))

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

		dbClient, store, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Seed a consistent entry.
		writeFsckNar1ToStorage(t, dir)

		ni := getFsckNarInfo(ctx, t, store, testdata.Nar1.NarInfoHash)

		require.NoError(t, testhelper.MigrateNarInfoToDatabase(ctx, dbClient, testdata.Nar1.NarInfoHash, ni))

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
		_, dbErr := dbClient.Ent().NarInfo.Query().
			Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).
			Only(ctx)
		assert.NoError(t, dbErr, "dry-run should not delete DB records")
	}
}

// testFsckVerifiedSince verifies that --verified-since skips recently checked NARs.
func testFsckVerifiedSince(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		dbClient, store, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Seed a fully consistent narinfo+narfile in DB and storage.
		writeFsckNar1ToStorage(t, dir)
		ni := getFsckNarInfo(ctx, t, store, testdata.Nar1.NarInfoHash)
		require.NoError(t, testhelper.MigrateNarInfoToDatabase(ctx, dbClient, testdata.Nar1.NarInfoHash, ni))

		nf, err := getNarFile(ctx, dbClient, testdata.Nar1.NarHash, testdata.Nar1.NarCompression.String(), "")
		require.NoError(t, err)
		assert.Nil(t, nf.VerifiedAt, "verified_at should be NULL initially")

		app, err := ncps.New()
		require.NoError(t, err)

		// 1. Run fsck - should populate verified_at
		args := []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
		}
		require.NoError(t, app.Run(ctx, args))

		nf, err = getNarFile(ctx, dbClient, testdata.Nar1.NarHash, testdata.Nar1.NarCompression.String(), "")
		require.NoError(t, err)
		require.NotNil(t, nf.VerifiedAt, "verified_at should be populated after fsck")
		verifiedAt1 := *nf.VerifiedAt

		// 2. Run fsck with --verified-since 1h - should skip checking.
		// MySQL TIMESTAMP has second-level precision; sleep >1s so step 3's fsck
		// lands in a different second than step 1's verifiedAt1.
		time.Sleep(1100 * time.Millisecond)

		args = []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--verified-since", "1h",
		}
		require.NoError(t, app.Run(ctx, args))

		nf, err = getNarFile(ctx, dbClient, testdata.Nar1.NarHash, testdata.Nar1.NarCompression.String(), "")
		require.NoError(t, err)
		require.NotNil(t, nf.VerifiedAt)
		assert.Equal(t, verifiedAt1, *nf.VerifiedAt, "verified_at should NOT be updated when skipped")

		// 3. Run fsck with --verified-since 1ms - should NOT skip checking.
		args = []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--verified-since", "1ms",
		}
		require.NoError(t, app.Run(ctx, args))

		nf, err = getNarFile(ctx, dbClient, testdata.Nar1.NarHash, testdata.Nar1.NarCompression.String(), "")
		require.NoError(t, err)
		require.NotNil(t, nf.VerifiedAt)
		assert.NotEqual(t, verifiedAt1, *nf.VerifiedAt, "verified_at SHOULD be updated when NOT skipped")
	}
}

// setupFsckSQLite creates a SQLite-backed test environment.
func setupFsckSQLite(t *testing.T) (*database.Client, *localstorage.Store, string, string, func()) {
	t.Helper()

	ctx := context.Background()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	dbClient, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := localstorage.New(ctx, dir)
	require.NoError(t, err)

	cleanup := func() {
		_ = dbClient.Close()
	}

	return dbClient, store, dir, "sqlite:" + dbFile, cleanup
}

// setupFsckPostgres creates a PostgreSQL-backed test environment.
func setupFsckPostgres(t *testing.T) (*database.Client, *localstorage.Store, string, string, func()) {
	t.Helper()

	ctx := context.Background()
	dir := t.TempDir()

	dbClient, dbURL, dbCleanup := testhelper.SetupPostgres(t)

	store, err := localstorage.New(ctx, dir)
	require.NoError(t, err)

	return dbClient, store, dir, dbURL, dbCleanup
}

// setupFsckMySQL creates a MySQL-backed test environment.
func setupFsckMySQL(t *testing.T) (*database.Client, *localstorage.Store, string, string, func()) {
	t.Helper()

	ctx := context.Background()
	dir := t.TempDir()

	dbClient, dbURL, dbCleanup := testhelper.SetupMySQL(t)

	store, err := localstorage.New(ctx, dir)
	require.NoError(t, err)

	return dbClient, store, dir, dbURL, dbCleanup
}

// getNarFile is a test helper that fetches a single NarFile by its (hash, compression, query) triple.
//
// signature mirrors the three-field uniqueness constraint and is kept open for
// future tests that exercise non-empty queries.
//
//nolint:unparam // The query parameter is always "" in current callers, but the
func getNarFile(
	ctx context.Context,
	dbClient *database.Client,
	hash, compression, query string,
) (*ent.NarFile, error) {
	return dbClient.Ent().NarFile.Query().
		Where(
			entnarfile.HashEQ(hash),
			entnarfile.CompressionEQ(compression),
			entnarfile.QueryEQ(query),
		).
		Only(ctx)
}

// createNarInfoFromParsed inserts a narinfo record into the DB using the
// fields parsed from a narinfo text. Mirrors the legacy CreateNarInfo
// helper that used sqlc-generated params.
func createNarInfoFromParsed(
	ctx context.Context,
	dbClient *database.Client,
	hash string,
	ni *narinfopkg.NarInfo,
) (*ent.NarInfo, error) {
	builder := dbClient.Ent().NarInfo.Create().SetHash(hash)

	if ni.StorePath != "" {
		builder = builder.SetStorePath(ni.StorePath)
	}

	if ni.URL != "" {
		builder = builder.SetURL(ni.URL)
	}

	if ni.Compression != "" {
		builder = builder.SetCompression(ni.Compression)
	}

	if ni.FileHash != nil {
		builder = builder.SetFileHash(ni.FileHash.String())
	}

	//nolint:gosec
	builder = builder.SetFileSize(int64(ni.FileSize))

	if ni.NarHash != nil {
		builder = builder.SetNarHash(ni.NarHash.String())
	}

	//nolint:gosec
	builder = builder.SetNarSize(int64(ni.NarSize))

	if ni.Deriver != "" {
		builder = builder.SetDeriver(ni.Deriver)
	}

	if ni.System != "" {
		builder = builder.SetSystem(ni.System)
	}

	if ni.CA != "" {
		builder = builder.SetCa(ni.CA)
	}

	return builder.Save(ctx)
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
func configureFsckCDCInDatabase(ctx context.Context, t *testing.T, dbClient *database.Client) {
	t.Helper()

	rwLocker := locklocal.NewRWLocker()
	cfg := config.New(dbClient, rwLocker)

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
	dbClient *database.Client,
	cs chunkstore.Store,
	narInfoHash string,
	narInfoText string,
	narHash string,
	narCompression nar.CompressionType,
	numChunks int,
) *ent.NarFile {
	t.Helper()

	ni := parseFsckNarInfoText(t, narInfoText)

	// Create narinfo in DB.
	narInfo, err := createNarInfoFromParsed(ctx, dbClient, narInfoHash, ni)
	require.NoError(t, err)

	// Create nar_file in DB. FileSize is set to ni.NarSize so that the correctly-chunked
	// nar_file is NOT flagged by GetCDCNarFilesWithSizeMismatch (file_size == nar_size).
	narFile, err := dbClient.Ent().NarFile.Create().
		SetHash(narHash).
		SetCompression(narCompression.String()).
		SetQuery("").
		SetFileSize(ni.NarSize).
		SetTotalChunks(int64(numChunks)).
		Save(ctx)
	require.NoError(t, err)

	// Link narinfo to nar_file.
	_, err = dbClient.Ent().NarInfoNarFile.Create().
		SetNarinfoID(narInfo.ID).
		SetNarFileID(narFile.ID).
		Save(ctx)
	require.NoError(t, err)

	// Create chunks in DB and storage.
	for i := range numChunks {
		// Use a simple deterministic hash: replace last char with a letter based on index.
		chunkHash := narHash[:len(narHash)-2] + string(rune('a'+(i/26))) + string(rune('a'+(i%26)))

		data := []byte(strings.Repeat("x", 64) + string(rune(i)))
		_, _, err := cs.PutChunk(ctx, chunkHash, data)
		require.NoError(t, err, "failed to put chunk %d", i)

		chunk, err := dbClient.Ent().Chunk.Create().
			SetHash(chunkHash).
			SetSize(uint32(len(data))).               //nolint:gosec
			SetCompressedSize(uint32(len(data) / 2)). //nolint:gosec
			Save(ctx)
		require.NoError(t, err)

		_, err = dbClient.Ent().NarFileChunk.Create().
			SetNarFileID(narFile.ID).
			SetChunkID(chunk.ID).
			SetChunkIndex(i).
			Save(ctx)
		require.NoError(t, err)
	}

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
		t.Run("ChunkedNarFilesNotFlaggedAsMissingWithoutCDCConfig",
			testFsckCDCChunkedNarFilesNotFlaggedAsMissingWithoutCDCConfig(setup))
		t.Run("SizeMismatchDetected", testFsckCDCSizeMismatchDetected(setup))
		t.Run("SizeMismatchRepair", testFsckCDCSizeMismatchRepair(setup))
		t.Run("CorrectSizeNotFlagged", testFsckCDCCorrectSizeNotFlagged(setup))
		t.Run("SizeMismatchRespectsVerifiedSince", testFsckCDCSizeMismatchRespectsVerifiedSince(setup))
		t.Run("SizeMismatchDoesNotUpdateVerifiedAt", testFsckCDCSizeMismatchDoesNotUpdateVerifiedAt(setup))
		t.Run("VerifyContentSkipsWhenFlagAbsent", testFsckCDCVerifyContentSkipsWhenFlagAbsent(setup))
		t.Run("CorruptChunkDetected", testFsckCDCCorruptChunkDetected(setup))
		t.Run("CleanChunksPassContentVerification", testFsckCDCCleanChunksPassContentVerification(setup))
		t.Run("HashMismatchDetected", testFsckCDCHashMismatchDetected(setup))
		t.Run("RepairCorruptChunk", testFsckCDCRepairCorruptChunk(setup))
		t.Run("RepairHashMismatch", testFsckCDCRepairHashMismatch(setup))
	}
}

// testFsckCDCClean verifies a fully-consistent CDC state produces 0 issues.
func testFsckCDCClean(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		setupFsckCDCNarFile(ctx, t, dbClient, cs,
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

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// Create nar_file with total_chunks=2 but only link 1 chunk.
		narFile := setupFsckCDCNarFile(ctx, t, dbClient, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 1)

		// Force total_chunks to 2 while only 1 chunk is linked.
		_, updateErr := dbClient.Ent().NarFile.UpdateOneID(narFile.ID).
			SetTotalChunks(2).
			SetFileSize(narFile.FileSize).
			Save(ctx)
		require.NoError(t, updateErr)

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

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// Create nar_file with 2 chunks.
		setupFsckCDCNarFile(ctx, t, dbClient, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 2)

		// Delete one chunk file from storage.
		allChunks, err := dbClient.Ent().Chunk.Query().All(ctx)
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

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

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

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// Create Nar1 with 2 chunks.
		setupFsckCDCNarFile(ctx, t, dbClient, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 2)

		// Get the chunks for Nar1.
		nar1Chunks, err := dbClient.Ent().Chunk.Query().All(ctx)
		require.NoError(t, err)
		require.Len(t, nar1Chunks, 2)

		ni2 := parseFsckNarInfoText(t, testdata.Nar2.NarInfoText)

		// Create Nar2 sharing one chunk with Nar1.
		// FileSize must equal ni2.NarSize (uncompressed) so Nar2 is not flagged as size-mismatched.
		nar2File, err := dbClient.Ent().NarFile.Create().
			SetHash(testdata.Nar2.NarHash).
			SetCompression(testdata.Nar2.NarCompression.String()).
			SetQuery("").
			SetFileSize(ni2.NarSize).
			SetTotalChunks(1).
			Save(ctx)
		require.NoError(t, err)

		narInfo2, err := createNarInfoFromParsed(ctx, dbClient, testdata.Nar2.NarInfoHash, ni2)
		require.NoError(t, err)

		_, err = dbClient.Ent().NarInfoNarFile.Create().
			SetNarinfoID(narInfo2.ID).
			SetNarFileID(nar2File.ID).
			Save(ctx)
		require.NoError(t, err)

		// Link Nar2 to the first chunk of Nar1 (shared chunk).
		sharedChunk := nar1Chunks[0]

		_, err = dbClient.Ent().NarFileChunk.Create().
			SetNarFileID(nar2File.ID).
			SetChunkID(sharedChunk.ID).
			SetChunkIndex(0).
			Save(ctx)
		require.NoError(t, err)

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
		_, err = dbClient.Ent().NarInfo.Query().
			Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).
			Only(ctx)
		assert.True(t, database.IsNotFoundError(err), "Nar1 narinfo should be deleted")

		_, err = getNarFile(ctx, dbClient, testdata.Nar1.NarHash, testdata.Nar1.NarCompression.String(), "")
		assert.True(t, database.IsNotFoundError(err), "Nar1 nar_file should be deleted")

		// Verify the broken chunk (chunk[1]) is removed from DB and storage.
		_, err = dbClient.Ent().Chunk.Query().
			Where(entchunk.HashEQ(brokenChunk.Hash)).
			Only(ctx)
		assert.True(t, database.IsNotFoundError(err), "broken chunk should be deleted from DB")

		exists, err := cs.HasChunk(ctx, brokenChunk.Hash)
		require.NoError(t, err)
		assert.False(t, exists, "broken chunk file should not exist in storage")

		// Verify Nar2 and its shared chunk still exist.
		_, err = dbClient.Ent().NarInfo.Query().
			Where(entnarinfo.HashEQ(testdata.Nar2.NarInfoHash)).
			Only(ctx)
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

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

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

// testFsckCDCChunkedNarFilesNotFlaggedAsMissingWithoutCDCConfig is a regression test for the
// false-positive bug: chunked nar_files (total_chunks > 0) must NOT be reported as "missing from
// storage" even when the cdc_enabled DB config key is absent. The fix removes the cdcMode gate
// on the TotalChunks > 0 check and adds data-based CDC auto-detection as a fallback.
func testFsckCDCChunkedNarFilesNotFlaggedAsMissingWithoutCDCConfig(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		// Intentionally do NOT call configureFsckCDCInDatabase — simulates a DB where the
		// cdc_enabled config key is missing (Bug 1 trigger condition).

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// Create a chunked nar_file with total_chunks > 0 and write chunks to chunk storage.
		// The whole NAR file does NOT exist in nar storage (correct for CDC-migrated files).
		setupFsckCDCNarFile(ctx, t, dbClient, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 2)

		app, err := ncps.New()
		require.NoError(t, err)

		// fsck should succeed with no issues: the chunked nar_file should not be flagged as
		// "missing from storage" and chunk verification should pass.
		args := []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
		}

		require.NoError(t, app.Run(ctx, args))
	}
}

// testFsckCDCSizeMismatchDetected verifies that a CDC nar_file with file_size smaller than
// the linked narinfo's nar_size is reported as a "CDC NARs w/ size mismatch" issue.
func testFsckCDCSizeMismatchDetected(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// Create a correctly-chunked nar_file then set file_size to a truncated value.
		narFile := setupFsckCDCNarFile(ctx, t, dbClient, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 2)

		const truncatedSize = 490516

		_, updateErr := dbClient.Ent().NarFile.UpdateOneID(narFile.ID).
			SetFileSize(truncatedSize).
			Save(ctx)
		require.NoError(t, updateErr)

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

// testFsckCDCSizeMismatchRepair verifies that --repair deletes a size-mismatched CDC
// nar_file and its orphaned narinfo, leaving a clean state on the next fsck run.
func testFsckCDCSizeMismatchRepair(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		narFile := setupFsckCDCNarFile(ctx, t, dbClient, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 2)

		const truncatedSize = 490516

		_, updateErr := dbClient.Ent().NarFile.UpdateOneID(narFile.ID).
			SetFileSize(truncatedSize).
			Save(ctx)
		require.NoError(t, updateErr)

		app, err := ncps.New()
		require.NoError(t, err)

		// Repair run — should delete the mismatched nar_file.
		require.NoError(t, app.Run(ctx, []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--repair",
		}))

		// Verify nar_file is gone.
		_, dbErr := getNarFile(ctx, dbClient, testdata.Nar1.NarHash, testdata.Nar1.NarCompression.String(), "")
		assert.True(t, database.IsNotFoundError(dbErr), "nar_file should be deleted after repair")

		// Second fsck run must report 0 issues.
		require.NoError(t, app.Run(ctx, []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
		}))
	}
}

// testFsckCDCCorrectSizeNotFlagged verifies that a CDC nar_file with file_size exactly
// equal to the linked narinfo's nar_size is NOT reported as a size mismatch.
func testFsckCDCCorrectSizeNotFlagged(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// setupFsckCDCNarFile sets file_size == ni.NarSize — a correctly-chunked row.
		setupFsckCDCNarFile(ctx, t, dbClient, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 2)

		app, err := ncps.New()
		require.NoError(t, err)

		// No issues expected — correctly-chunked rows must not be flagged.
		require.NoError(t, app.Run(ctx, []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
		}))
	}
}

// testFsckCDCSizeMismatchRespectsVerifiedSince verifies that the size-mismatch check
// follows the same verified_at skip semantics as the other nar_file checks.
func testFsckCDCSizeMismatchRespectsVerifiedSince(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		narFile := setupFsckCDCNarFile(ctx, t, dbClient, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 2)

		const truncatedSize = 490516

		_, updateErr := dbClient.Ent().NarFile.UpdateOneID(narFile.ID).
			SetFileSize(truncatedSize).
			Save(ctx)
		require.NoError(t, updateErr)

		_, verifyErr := dbClient.Ent().NarFile.UpdateOneID(narFile.ID).
			SetVerifiedAt(time.Now()).
			Save(ctx)
		require.NoError(t, verifyErr)

		app, err := ncps.New()
		require.NoError(t, err)

		require.NoError(t, app.Run(ctx, []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--verified-since=1h",
		}))
	}
}

// setupFsckCDCNarFileWithRealHashes creates a CDC NAR file whose chunks are stored with
// correct BLAKE3 hashes. The narinfo is populated with a NarHash that is the SHA-256 of
// the concatenated chunk data. This helper is required for content-verification tests.
//
// chunkDataParts is the raw content for each chunk. The data is stored verbatim and the
// BLAKE3 of each part is used as the chunk hash. The DB narinfo's NarHash field is set to
// sha256:<nixbase32(sha256(all parts concatenated))> so that isNarFileHashMismatched
// returns false for a correctly assembled archive.
func setupFsckCDCNarFileWithRealHashes(
	ctx context.Context,
	t *testing.T,
	dbClient *database.Client,
	cs chunkstore.Store,
	narInfoHash string,
	chunkDataParts [][]byte,
) {
	t.Helper()

	// Compute per-chunk BLAKE3 hashes and combined SHA-256 for NarHash.
	chunkHashes := make([]string, len(chunkDataParts))
	combinedHash := sha256.New()

	for i, data := range chunkDataParts {
		sum := blake3.Sum256(data)
		chunkHashes[i] = hex.EncodeToString(sum[:])

		combinedHash.Write(data)
	}

	narHashBytes := combinedHash.Sum(nil)
	narHashStr := "sha256:" + nixbase32.EncodeToString(narHashBytes)

	// Compute total size.
	var totalSize int64
	for _, d := range chunkDataParts {
		totalSize += int64(len(d))
	}

	// Build a minimal narinfo text. References must have a non-empty value or be omitted —
	// the narinfo parser rejects "References:" with no trailing space+value.
	narInfoText := fmt.Sprintf(
		"StorePath: /nix/store/%s-content-verify-test\nURL: nar/%s.nar\nCompression: none\n"+
			"FileHash: %s\nFileSize: %d\nNarHash: %s\nNarSize: %d\nReferences: %s-content-verify-test\n",
		narInfoHash, narInfoHash, narHashStr, totalSize, narHashStr, totalSize, narInfoHash,
	)

	ni := parseFsckNarInfoText(t, narInfoText)

	narInfo, err := createNarInfoFromParsed(ctx, dbClient, narInfoHash, ni)
	require.NoError(t, err)

	narHash := narInfoHash // reuse narInfoHash as nar_file hash for simplicity
	narFile, err := dbClient.Ent().NarFile.Create().
		SetHash(narHash).
		SetCompression("none").
		SetQuery("").
		SetFileSize(uint64(totalSize)).
		SetTotalChunks(int64(len(chunkDataParts))).
		Save(ctx)
	require.NoError(t, err)

	_, err = dbClient.Ent().NarInfoNarFile.Create().
		SetNarinfoID(narInfo.ID).
		SetNarFileID(narFile.ID).
		Save(ctx)
	require.NoError(t, err)

	for i, data := range chunkDataParts {
		_, _, err := cs.PutChunk(ctx, chunkHashes[i], data)
		require.NoError(t, err)

		chunk, err := dbClient.Ent().Chunk.Create().
			SetHash(chunkHashes[i]).
			SetSize(uint32(len(data))).               //nolint:gosec
			SetCompressedSize(uint32(len(data) / 2)). //nolint:gosec
			Save(ctx)
		require.NoError(t, err)

		_, err = dbClient.Ent().NarFileChunk.Create().
			SetNarFileID(narFile.ID).
			SetChunkID(chunk.ID).
			SetChunkIndex(i).
			Save(ctx)
		require.NoError(t, err)
	}
}

// testFsckCDCSizeMismatchDoesNotUpdateVerifiedAt verifies that a dry-run fsck does
// not mark a size-mismatched CDC nar_file as verified while reporting the issue.
func testFsckCDCSizeMismatchDoesNotUpdateVerifiedAt(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		narFile := setupFsckCDCNarFile(ctx, t, dbClient, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 2)

		const truncatedSize = 490516

		_, updateErr := dbClient.Ent().NarFile.UpdateOneID(narFile.ID).
			SetFileSize(truncatedSize).
			Save(ctx)
		require.NoError(t, updateErr)

		app, err := ncps.New()
		require.NoError(t, err)

		runErr := app.Run(ctx, []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--dry-run",
		})
		require.ErrorIs(t, runErr, ncps.ErrFsckIssuesFound)

		narFileAfter, dbErr := getNarFile(ctx, dbClient, testdata.Nar1.NarHash, testdata.Nar1.NarCompression.String(), "")
		require.NoError(t, dbErr)
		assert.Nil(t, narFileAfter.VerifiedAt, "size-mismatched nar_file must not be marked verified")
	}
}

// testFsckCDCVerifyContentSkipsWhenFlagAbsent verifies that chunks with wrong BLAKE3 hashes
// are NOT detected without --verify-content.
func testFsckCDCVerifyContentSkipsWhenFlagAbsent(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// Create a CDC NAR with fake (non-BLAKE3) chunk hashes — content is corrupt
		// but structural checks (count + existence) pass.
		setupFsckCDCNarFile(ctx, t, dbClient, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 2)

		app, err := ncps.New()
		require.NoError(t, err)

		// Without --verify-content, corrupt chunk content is not detected.
		require.NoError(t, app.Run(ctx, []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
		}))
	}
}

// testFsckCDCCorruptChunkDetected verifies that a chunk stored under the wrong BLAKE3 hash is
// detected when --verify-content is set.
func testFsckCDCCorruptChunkDetected(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// setupFsckCDCNarFile stores chunks under hashes that do NOT reflect their actual
		// BLAKE3 content — this simulates bit-rot / corruption.
		setupFsckCDCNarFile(ctx, t, dbClient, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 2)

		app, err := ncps.New()
		require.NoError(t, err)

		err = app.Run(ctx, []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--verify-content",
			"--dry-run",
		})
		assert.ErrorIs(t, err, ncps.ErrFsckIssuesFound)
	}
}

// testFsckCDCCleanChunksPassContentVerification verifies that chunks stored with correct BLAKE3
// hashes and a matching NarHash produce 0 issues with --verify-content.
func testFsckCDCCleanChunksPassContentVerification(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// Create two chunks whose BLAKE3 hashes are correct and whose combined SHA-256
		// matches the NarHash stored in the narinfo.
		chunkData := [][]byte{
			[]byte("chunk-zero-data-for-content-verify-test"),
			[]byte("chunk-one-data-for-content-verify-test"),
		}

		setupFsckCDCNarFileWithRealHashes(ctx, t, dbClient, cs, testdata.Nar1.NarInfoHash, chunkData)

		app, err := ncps.New()
		require.NoError(t, err)

		require.NoError(t, app.Run(ctx, []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--verify-content",
		}))
	}
}

// testFsckCDCHashMismatchDetected verifies that a NAR file whose chunks have correct BLAKE3
// hashes but whose assembled SHA-256 does not match the narinfo NarHash is detected.
func testFsckCDCHashMismatchDetected(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// Build chunks with correct BLAKE3 hashes so the corrupt-chunk check passes.
		chunkData := [][]byte{
			[]byte("chunk-zero-for-hash-mismatch-test"),
			[]byte("chunk-one-for-hash-mismatch-test"),
		}

		setupFsckCDCNarFileWithRealHashes(ctx, t, dbClient, cs, testdata.Nar1.NarInfoHash, chunkData)

		// Corrupt the narinfo's NarHash in the DB so the assembled SHA-256 no longer matches.
		narInfoHash := testdata.Nar1.NarInfoHash
		ni, dbErr := dbClient.Ent().NarInfo.Query().
			Where(entnarinfo.HashEQ(narInfoHash)).
			Only(ctx)
		require.NoError(t, dbErr)

		// Set a deliberately wrong NarHash so the assembled SHA-256 can never match.
		_, updateErr := dbClient.Ent().NarInfo.UpdateOneID(ni.ID).
			SetNarHash("sha256:0000000000000000000000000000000000000000000000000000").
			Save(ctx)
		require.NoError(t, updateErr)

		app, err := ncps.New()
		require.NoError(t, err)

		err = app.Run(ctx, []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--verify-content",
			"--dry-run",
		})
		assert.ErrorIs(t, err, ncps.ErrFsckIssuesFound)
	}
}

// testFsckCDCRepairCorruptChunk verifies that repair deletes a CDC nar_file with corrupt chunks,
// its linked narinfo, and the orphaned chunks from DB and storage.
func testFsckCDCRepairCorruptChunk(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		// Chunks stored with fake BLAKE3 hashes (corrupt content).
		setupFsckCDCNarFile(ctx, t, dbClient, cs,
			testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
			testdata.Nar1.NarHash, testdata.Nar1.NarCompression, 2)

		app, err := ncps.New()
		require.NoError(t, err)

		// Run with --repair.
		require.NoError(t, app.Run(ctx, []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--verify-content",
			"--repair",
		}))

		// Second run should be clean.
		require.NoError(t, app.Run(ctx, []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--verify-content",
		}))

		// The nar_file must be gone from DB.
		_, dbErr := getNarFile(ctx, dbClient, testdata.Nar1.NarHash, testdata.Nar1.NarCompression.String(), "")
		assert.True(t, database.IsNotFoundError(dbErr), "nar_file must be deleted")
	}
}

// testFsckCDCRepairHashMismatch verifies that repair deletes a CDC nar_file whose assembled
// SHA-256 does not match the narinfo NarHash.
func testFsckCDCRepairHashMismatch(setup fsckSetupFn) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := zerolog.New(os.Stderr).WithContext(context.Background())

		dbClient, _, dir, dbURL, cleanup := setup(t)
		t.Cleanup(cleanup)

		configureFsckCDCInDatabase(ctx, t, dbClient)

		cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
		require.NoError(t, err)

		chunkData := [][]byte{
			[]byte("chunk-zero-for-repair-hash-mismatch-test"),
			[]byte("chunk-one-for-repair-hash-mismatch-test"),
		}

		setupFsckCDCNarFileWithRealHashes(ctx, t, dbClient, cs, testdata.Nar1.NarInfoHash, chunkData)

		// Corrupt the stored NarHash to force a mismatch.
		ni, dbErr := dbClient.Ent().NarInfo.Query().
			Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).
			Only(ctx)
		require.NoError(t, dbErr)

		_, updateErr := dbClient.Ent().NarInfo.UpdateOneID(ni.ID).
			SetNarHash("sha256:0000000000000000000000000000000000000000000000000000").
			Save(ctx)
		require.NoError(t, updateErr)

		app, err := ncps.New()
		require.NoError(t, err)

		require.NoError(t, app.Run(ctx, []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--verify-content",
			"--repair",
		}))

		// Second run clean.
		require.NoError(t, app.Run(ctx, []string{
			"ncps", "fsck",
			"--cache-database-url", dbURL,
			"--cache-storage-local", dir,
			"--verify-content",
		}))

		// The nar_file must be gone.
		_, dbErr = getNarFile(ctx, dbClient, testdata.Nar1.NarInfoHash, "none", "")
		assert.True(t, database.IsNotFoundError(dbErr), "nar_file must be deleted")
	}
}

// TestQueryCDCNarFilesWithSizeMismatch_LargePostgreSQL is a regression test for
// the "extended protocol limited to 65535 parameters" failure that the
// pre-batching implementation hit on production-sized caches. It seeds more CDC
// nar_file rows than the PostgreSQL extended-protocol parameter cap and then
// confirms the function completes and returns exactly the seeded mismatched rows.
func TestQueryCDCNarFilesWithSizeMismatch_LargePostgreSQL(t *testing.T) {
	t.Parallel()

	if os.Getenv("NCPS_TEST_ADMIN_POSTGRES_URL") == "" {
		t.Skip("Skipping: NCPS_TEST_ADMIN_POSTGRES_URL not set")
	}

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	dbClient, _, _, _, cleanup := setupFsckPostgres(t)
	t.Cleanup(cleanup)

	// totalRows must exceed PostgreSQL's 65535 extended-protocol parameter cap so
	// that the pre-fix code path (single `WHERE nar_file_id IN ($1...$N)` Ent
	// eager-load) would fail. mismatchCount stays small so the assertion is cheap.
	const (
		totalRows         = 70_000
		mismatchCount     = 5
		matchingNarSize   = int64(1000)
		mismatchedSize    = uint64(2000)
		nfInsertBatchSize = 5000
		linkInsertBatch   = 10_000
	)

	// One shared narinfo. Matching nar_files set file_size == narSize; mismatched
	// ones set file_size != narSize.
	narInfo, err := dbClient.Ent().NarInfo.Create().
		SetHash("largepg-shared-narinfo").
		SetURL("nar/largepg.nar.xz").
		SetNarSize(matchingNarSize).
		Save(ctx)
	require.NoError(t, err)

	// Bulk insert CDC nar_files in batches sized to stay below the parameter cap
	// for the multi-row INSERT itself (5 columns per row * 5000 rows = 25k params).
	mismatchedHashes := make(map[string]struct{}, mismatchCount)

	for start := 0; start < totalRows; start += nfInsertBatchSize {
		end := start + nfInsertBatchSize
		if end > totalRows {
			end = totalRows
		}

		creates := make([]*ent.NarFileCreate, 0, end-start)

		for i := start; i < end; i++ {
			hash := fmt.Sprintf("largepg-narfile-%06d", i)

			size := uint64(matchingNarSize)
			if i < mismatchCount {
				size = mismatchedSize
				mismatchedHashes[hash] = struct{}{}
			}

			creates = append(creates, dbClient.Ent().NarFile.Create().
				SetHash(hash).
				SetCompression("xz").
				SetQuery("").
				SetFileSize(size).
				SetTotalChunks(2))
		}

		_, err := dbClient.Ent().NarFile.CreateBulk(creates...).Save(ctx)
		require.NoError(t, err, "bulk-create nar_files batch starting at %d", start)
	}

	// Fetch all seeded nar_file IDs so we can bulk-link them to the shared narinfo.
	seeded, err := dbClient.Ent().NarFile.Query().
		Where(entnarfile.CompressionEQ("xz"), entnarfile.HashHasPrefix("largepg-narfile-")).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, seeded, totalRows)

	for start := 0; start < len(seeded); start += linkInsertBatch {
		end := start + linkInsertBatch
		if end > len(seeded) {
			end = len(seeded)
		}

		links := make([]*ent.NarInfoNarFileCreate, 0, end-start)
		for _, nf := range seeded[start:end] {
			links = append(links, dbClient.Ent().NarInfoNarFile.Create().
				SetNarinfoID(narInfo.ID).
				SetNarFileID(nf.ID))
		}

		_, err := dbClient.Ent().NarInfoNarFile.CreateBulk(links...).Save(ctx)
		require.NoError(t, err, "bulk-create narinfo_nar_file links batch starting at %d", start)
	}

	got, err := ncps.QueryCDCNarFilesWithSizeMismatchForTest(ctx, dbClient)
	require.NoError(t, err, "queryCDCNarFilesWithSizeMismatch must succeed on a large CDC cache")

	gotHashes := make(map[string]struct{}, len(got))
	for _, nf := range got {
		gotHashes[nf.Hash] = struct{}{}
	}

	assert.Equal(t, mismatchedHashes, gotHashes,
		"returned set must equal exactly the seeded mismatched hashes")
}

// TestChunksForNarFile_LargePostgreSQL is a regression test for the same
// 65535-parameter cap, exercised through the `WithChunk()` eager-load that the
// pre-fix `chunksForNarFile` issued for a single oversized CDC NAR.
func TestChunksForNarFile_LargePostgreSQL(t *testing.T) {
	t.Parallel()

	if os.Getenv("NCPS_TEST_ADMIN_POSTGRES_URL") == "" {
		t.Skip("Skipping: NCPS_TEST_ADMIN_POSTGRES_URL not set")
	}

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	dbClient, _, _, _, cleanup := setupFsckPostgres(t)
	t.Cleanup(cleanup)

	// chunkCount must exceed 65535 so the pre-fix `WithChunk()` follow-up would
	// emit `WHERE id IN ($1...$M)` with M > 65535 and trip the cap.
	const (
		chunkCount       = 70_000
		chunkInsertBatch = 10_000
		linkInsertBatch  = 10_000
	)

	narFile, err := dbClient.Ent().NarFile.Create().
		SetHash("largepg-chunky-narfile").
		SetCompression("xz").
		SetQuery("").
		SetFileSize(1).
		SetTotalChunks(int64(chunkCount)).
		Save(ctx)
	require.NoError(t, err)

	// Bulk-insert chunks (3 columns per row * 10000 rows = 30k params per batch).
	for start := 0; start < chunkCount; start += chunkInsertBatch {
		end := start + chunkInsertBatch
		if end > chunkCount {
			end = chunkCount
		}

		creates := make([]*ent.ChunkCreate, 0, end-start)
		for i := start; i < end; i++ {
			creates = append(creates, dbClient.Ent().Chunk.Create().
				SetHash(fmt.Sprintf("largepg-chunk-%06d", i)).
				SetSize(uint32(64)).
				SetCompressedSize(uint32(32)))
		}

		_, err := dbClient.Ent().Chunk.CreateBulk(creates...).Save(ctx)
		require.NoError(t, err, "bulk-create chunks batch starting at %d", start)
	}

	// Fetch seeded chunk IDs in hash order — hash order matches the index we will
	// assign in the link table, which lets the test check ordering deterministically.
	chunks, err := dbClient.Ent().Chunk.Query().
		Where(entchunk.HashHasPrefix("largepg-chunk-")).
		Order(ent.Asc(entchunk.FieldHash)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, chunks, chunkCount)

	for start := 0; start < chunkCount; start += linkInsertBatch {
		end := start + linkInsertBatch
		if end > chunkCount {
			end = chunkCount
		}

		links := make([]*ent.NarFileChunkCreate, 0, end-start)
		for i := start; i < end; i++ {
			links = append(links, dbClient.Ent().NarFileChunk.Create().
				SetNarFileID(narFile.ID).
				SetChunkID(chunks[i].ID).
				SetChunkIndex(i))
		}

		_, err := dbClient.Ent().NarFileChunk.CreateBulk(links...).Save(ctx)
		require.NoError(t, err, "bulk-create nar_file_chunks batch starting at %d", start)
	}

	got, err := ncps.ChunksForNarFileForTest(ctx, dbClient, narFile.ID)
	require.NoError(t, err, "chunksForNarFile must succeed on a NAR with > 65535 chunks")
	require.Len(t, got, chunkCount, "returned chunk count must match seeded count")

	for i, ch := range got {
		require.NotNil(t, ch, "chunk at index %d must not be nil", i)
		assert.Equal(t, chunks[i].ID, ch.ID, "chunk at index %d must be in chunk_index order", i)
	}
}
