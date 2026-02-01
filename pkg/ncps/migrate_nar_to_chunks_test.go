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

func TestMigrateNarToChunks_Success(t *testing.T) {
	t.Parallel()
	// Setup
	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	dir := t.TempDir()

	// 1. Setup Database
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)
	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	// 2. Setup traditional storage
	_, err = local.New(ctx, dir)
	require.NoError(t, err)

	// 3. Pre-populate storage with NarInfo and NAR (unmigrated in DB)
	narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

	narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
	require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

	// 4. Register the command and run it
	// NOTE: We need a way to run the command. For now, since it's TDD,
	// we expect the command to exist in the ncps package.

	args := []string{
		"ncps", "migrate-nar-to-chunks",
		"--cache-database-url", "sqlite:" + dbFile,
		"--cache-storage-local", dir,
		"--cache-cdc-enabled",
		"--cache-hostname", "cache.example.com",
	}

	app, err := ncps.New()
	require.NoError(t, err)

	err = app.Run(ctx, args)
	require.NoError(t, err)

	// 5. Verification
	// Chunks should be created in {dir}/store/chunks (or similar, based on local chunk store)
	// Actually, let's check the database.
	var count int

	err = db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&count)
	require.NoError(t, err)
	assert.Positive(t, count, "Chunks should have been created")

	// The NAR should be deleted from traditional storage
	assert.NoFileExists(t, narPath, "Original NAR should have been deleted")
}

func TestMigrateNarToChunks_DryRun(t *testing.T) {
	t.Parallel()
	// Setup
	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	dir := t.TempDir()

	// 1. Setup Database
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)
	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	// 2. Setup traditional storage
	_, err = local.New(ctx, dir)
	require.NoError(t, err)

	// 3. Pre-populate storage with NarInfo and NAR
	narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

	narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
	require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

	// 4. Run command with --dry-run
	args := []string{
		"ncps", "migrate-nar-to-chunks",
		"--cache-database-url", "sqlite:" + dbFile,
		"--cache-storage-local", dir,
		"--cache-cdc-enabled",
		"--cache-hostname", "cache.example.com",
		"--dry-run",
	}

	app, err := ncps.New()
	require.NoError(t, err)

	err = app.Run(ctx, args)
	require.NoError(t, err)

	// 5. Verification
	var count int

	err = db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "No chunks should have been created in dry-run")

	assert.FileExists(t, narPath, "Original NAR should NOT have been deleted in dry-run")
}

func TestMigrateNarToChunks_Idempotency(t *testing.T) {
	t.Parallel()
	// Setup
	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	dir := t.TempDir()

	// 1. Setup Database
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)
	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	// 2. Setup traditional storage
	_, err = local.New(ctx, dir)
	require.NoError(t, err)

	// 3. Pre-populate storage with NarInfo and NAR
	narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

	narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
	require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

	args := []string{
		"ncps", "migrate-nar-to-chunks",
		"--cache-database-url", "sqlite:" + dbFile,
		"--cache-storage-local", dir,
		"--cache-cdc-enabled",
		"--cache-hostname", "cache.example.com",
	}

	app, err := ncps.New()
	require.NoError(t, err)

	// 4. Run command first time
	err = app.Run(ctx, args)
	require.NoError(t, err)

	// Verify chunks created
	var count1 int

	err = db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&count1)
	require.NoError(t, err)
	assert.Positive(t, count1)

	// 5. Run command second time
	// The NAR is already deleted, but the command should still pass (skipping already chunked/non-existent NARs)
	// Wait, if the NAR is deleted, WalkNarInfos will still find the NarInfo, but GetNar will fail.
	// Actually, MigrateNarToChunks checks hasNarInChunks first.
	err = app.Run(ctx, args)
	require.NoError(t, err)

	// Verify chunks count remains same
	var count2 int

	err = db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&count2)
	require.NoError(t, err)
	assert.Equal(t, count1, count2, "Chunks count should remain same after second run")
}

func TestMigrateNarToChunks_MultipleNARs(t *testing.T) {
	t.Parallel()
	// Setup
	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	dir := t.TempDir()

	// 1. Setup Database
	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)
	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	// 2. Setup traditional storage
	_, err = local.New(ctx, dir)
	require.NoError(t, err)

	// 3. Pre-populate storage with multiple NARs
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
		"--cache-database-url", "sqlite:" + dbFile,
		"--cache-storage-local", dir,
		"--cache-cdc-enabled",
		"--cache-hostname", "cache.example.com",
	}

	app, err := ncps.New()
	require.NoError(t, err)

	// 4. Run command
	err = app.Run(ctx, args)
	require.NoError(t, err)

	// 5. Verification
	var count int

	err = db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&count)
	require.NoError(t, err)
	assert.Positive(t, count)

	for _, entry := range entries {
		narPath := filepath.Join(dir, "store", "nar", entry.NarPath)
		assert.NoFileExists(t, narPath, "NAR %s should have been deleted", entry.NarPath)
	}
}
