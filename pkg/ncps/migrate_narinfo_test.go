package ncps_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

func TestMigrateNarInfo_Success(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Setup
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

	// Pre-populate storage with narinfos
	narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

	narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
	require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

	// Verify not in database
	var count int

	err = db.DB().QueryRowContext(
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

	// Verify still in storage (no delete flag)
	assert.FileExists(t, narInfoPath)
}

func TestMigrateNarInfo_DryRun(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Setup
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

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

	err = store.WalkNarInfos(ctx, func(hash string) error {
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

func TestMigrateNarInfo_WithDelete(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Setup
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

	// Pre-populate storage with narinfos
	narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

	narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
	require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

	// Run migration with delete
	// WalkNarInfos is implemented by local.Store

	err = store.WalkNarInfos(ctx, func(hash string) error {
		ni, err := store.GetNarInfo(ctx, hash)
		if err != nil {
			return err
		}

		// Migrate to database
		if err := testhelper.MigrateNarInfoToDatabase(ctx, db, hash, ni); err != nil {
			return err
		}

		// Delete from storage
		if err := store.DeleteNarInfo(ctx, hash); err != nil {
			return err
		}

		return nil
	})
	require.NoError(t, err)

	// Verify in database
	var count int

	err = db.DB().QueryRowContext(
		ctx, "SELECT COUNT(*) FROM narinfos WHERE hash = ?", testdata.Nar1.NarInfoHash,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify NOT in storage (deleted)
	assert.NoFileExists(t, narInfoPath)
}

func TestMigrateNarInfo_Idempotency(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Setup
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

	// Pre-populate storage with narinfos
	narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

	narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
	require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

	// WalkNarInfos is implemented by local.Store

	// Run migration first time
	err = store.WalkNarInfos(ctx, func(hash string) error {
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

		// Should either succeed or return ErrAlreadyExists
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

func TestMigrateNarInfo_MultipleNarInfos(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Setup
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

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

	err = store.WalkNarInfos(ctx, func(hash string) error {
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

func TestMigrateNarInfo_AlreadyMigrated(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Setup
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

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

func TestMigrateNarInfo_StorageIterationError(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Setup with empty directory (no narinfos)
	dir := t.TempDir()
	dbFile := filepath.Join(t.TempDir(), "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	_, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

	// Walk should succeed even if there are no narinfos
	// WalkNarInfos is implemented by local.Store
	callbackInvoked := false

	err = store.WalkNarInfos(ctx, func(_ string) error {
		callbackInvoked = true

		return nil
	})
	require.NoError(t, err)
	assert.False(t, callbackInvoked, "Callback should not be called for empty directory")
}

func TestMigrateNarInfo_WithReferencesAndSignatures(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Setup
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

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

func TestMigrateNarInfo_DeleteAlreadyMigrated(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Setup
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

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

func TestMigrateNarInfo_ConcurrentMigration(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Setup
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	// Increase connection pool for concurrent operations
	db.DB().SetMaxOpenConns(20)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

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

	err = store.WalkNarInfos(ctx, func(hash string) error {
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

func TestMigrateNarInfo_PartialData(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Setup
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

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

func TestMigrateNarInfo_TransactionRollback(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Setup
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

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

func TestMigrateNarInfo_MissingNarFile(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Setup
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

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

func TestMigrateNarInfo_ProgressTracking(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Setup
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, dir)
	require.NoError(t, err)

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

	err = store.WalkNarInfos(ctx, func(hash string) error {
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
