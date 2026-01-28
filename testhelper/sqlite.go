package testhelper

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
)

// CreateMigrateDatabase will create all necessary directories, and will create
// the sqlite3 database (if necessary) and migrate it.
func CreateMigrateDatabase(t testing.TB, dbFile string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(dbFile), 0o700))

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	dbMigrationsDir := filepath.Join(
		filepath.Dir(filepath.Dir(thisFile)),
		"db",
		"migrations",
		"sqlite",
	)

	dbSchema := filepath.Join(
		filepath.Dir(filepath.Dir(thisFile)),
		"db",
		"schema",
		"sqlite.sql",
	)

	//nolint:gosec
	cmd := exec.CommandContext(context.Background(),
		"dbmate",
		"--no-dump-schema",
		"--url=sqlite:"+dbFile,
		"--migrations-dir="+dbMigrationsDir,
		"--schema-file="+dbSchema,
		"up",
	)

	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "Running %q has failed", cmd.String())

	t.Logf("%s: %s", cmd.String(), output)
}

// MigrateNarInfoToDatabase migrates a single narinfo to the database.
// This is a test helper that mimics the migration logic in pkg/ncps/migrate_narinfo.go.
func MigrateNarInfoToDatabase(ctx context.Context, db database.Querier, hash string, ni *narinfo.NarInfo) error {
	// Explicit transaction
	sqlDB := db.DB()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	qtx := db.WithTx(tx)

	// Get or Create NarInfo
	nir, err := getOrCreateNarInfo(ctx, qtx, hash, ni)
	if err != nil {
		return err
	}

	// References
	if len(ni.References) > 0 {
		err := qtx.AddNarInfoReferences(ctx, database.AddNarInfoReferencesParams{
			NarInfoID: nir.ID,
			Reference: ni.References,
		})
		if err != nil {
			// This can fail with a duplicate key error if the narinfo already existed, which is fine.
			if !database.IsDuplicateKeyError(err) {
				return fmt.Errorf("failed to add references: %w", err)
			}
		}
	}

	// Signatures
	sigStrings := make([]string, len(ni.Signatures))
	for i, sig := range ni.Signatures {
		sigStrings[i] = sig.String()
	}

	if len(sigStrings) > 0 {
		err := qtx.AddNarInfoSignatures(ctx, database.AddNarInfoSignaturesParams{
			NarInfoID: nir.ID,
			Signature: sigStrings,
		})
		if err != nil {
			// This can fail with a duplicate key error if the narinfo already existed, which is fine.
			if !database.IsDuplicateKeyError(err) {
				return fmt.Errorf("failed to add signatures: %w", err)
			}
		}
	}

	// NarFile
	narURL, err := nar.ParseURL(ni.URL)
	if err != nil {
		return fmt.Errorf("error parsing the nar URL: %w", err)
	}

	// Get or Create NarFile
	narFile, err := getOrCreateNarFile(ctx, qtx, &narURL, ni.FileSize)
	if err != nil {
		return err
	}

	// Link NarInfo to NarFile
	if err := qtx.LinkNarInfoToNarFile(ctx, database.LinkNarInfoToNarFileParams{
		NarInfoID: nir.ID,
		NarFileID: narFile.ID,
	}); err != nil {
		if !database.IsDuplicateKeyError(err) {
			return fmt.Errorf("failed to link narinfo to narfile: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func getOrCreateNarInfo(
	ctx context.Context,
	qtx database.Querier,
	hash string,
	ni *narinfo.NarInfo,
) (database.NarInfo, error) {
	// First, try to get the record.
	existing, err := qtx.GetNarInfoByHash(ctx, hash)
	if err == nil {
		// Found it, return.
		return existing, nil
	}

	// If the error is anything other than "not found", it's a real error.
	if !database.IsNotFoundError(err) {
		return database.NarInfo{}, fmt.Errorf("failed to get narinfo record: %w", err)
	}

	// Not found, so let's create it.
	nir, err := qtx.CreateNarInfo(ctx, database.CreateNarInfoParams{
		Hash:        hash,
		StorePath:   sql.NullString{String: ni.StorePath, Valid: ni.StorePath != ""},
		URL:         sql.NullString{String: ni.URL, Valid: ni.URL != ""},
		Compression: sql.NullString{String: ni.Compression, Valid: ni.Compression != ""},
		FileHash:    sql.NullString{String: ni.FileHash.String(), Valid: ni.FileHash != nil},
		FileSize:    sql.NullInt64{Int64: int64(ni.FileSize), Valid: true}, //nolint:gosec
		NarHash:     sql.NullString{String: ni.NarHash.String(), Valid: ni.NarHash != nil},
		NarSize:     sql.NullInt64{Int64: int64(ni.NarSize), Valid: true}, //nolint:gosec
		Deriver:     sql.NullString{String: ni.Deriver, Valid: ni.Deriver != ""},
		System:      sql.NullString{String: ni.System, Valid: ni.System != ""},
		Ca:          sql.NullString{String: ni.CA, Valid: ni.CA != ""},
	})
	if err != nil {
		// If we get a duplicate key error, it means another worker created it between our GET and CREATE.
		if database.IsDuplicateKeyError(err) {
			// Fetch the record again. This time it should exist.
			existing, errGet := qtx.GetNarInfoByHash(ctx, hash)
			if errGet != nil {
				return database.NarInfo{}, fmt.Errorf("failed to get existing record after race: %w", errGet)
			}

			return existing, nil
		}

		// Another error occurred during creation.
		return database.NarInfo{}, fmt.Errorf("failed to create narinfo record: %w", err)
	}

	return nir, nil
}

func getOrCreateNarFile(
	ctx context.Context,
	qtx database.Querier,
	narURL *nar.URL,
	narSize uint64,
) (database.NarFile, error) {
	// First, try to get the record.
	existing, err := qtx.GetNarFileByHashAndCompressionAndQuery(ctx, database.GetNarFileByHashAndCompressionAndQueryParams{
		Hash:        narURL.Hash,
		Compression: narURL.Compression.String(),
		Query:       narURL.Query.Encode(),
	})
	if err == nil {
		// Found it, return.
		return existing, nil
	}

	// If the error is anything other than "not found", it's a real error.
	if !database.IsNotFoundError(err) {
		return database.NarFile{}, fmt.Errorf("failed to get existing nar file record: %w", err)
	}

	// Not found, so let's create it.
	narFile, err := qtx.CreateNarFile(ctx, database.CreateNarFileParams{
		Hash:        narURL.Hash,
		Compression: narURL.Compression.String(),
		Query:       narURL.Query.Encode(),
		FileSize:    narSize,
	})
	if err != nil {
		// If we get a duplicate key error, it means another worker created it.
		if database.IsDuplicateKeyError(err) {
			// Fetch the record again. This time it should exist.
			existing, errGet := qtx.GetNarFileByHashAndCompressionAndQuery(
				ctx,
				database.GetNarFileByHashAndCompressionAndQueryParams{
					Hash:        narURL.Hash,
					Compression: narURL.Compression.String(),
					Query:       narURL.Query.Encode(),
				})
			if errGet != nil {
				return database.NarFile{}, fmt.Errorf("failed to get existing nar file record after race: %w", errGet)
			}

			return existing, nil
		}

		// Another error occurred during creation.
		return database.NarFile{}, fmt.Errorf("error creating the nar file record: %w", err)
	}

	return narFile, nil
}

// SetupSQLite sets up a new temporary SQLite database for testing.
// It returns a database connection and a cleanup function.
// This function has the same signature as SetupPostgres and SetupMySQL for consistency.
func SetupSQLite(t *testing.T) (database.Querier, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "sqlite-test-")
	require.NoError(t, err)

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	cleanup := func() {
		db.DB().Close()
		os.RemoveAll(dir)
	}

	return db, cleanup
}
