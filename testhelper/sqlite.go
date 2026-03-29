package testhelper

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
)

// CreateMigrateDatabase will create all necessary directories, and will create
// the sqlite3 database (if necessary) and migrate it.
func CreateMigrateDatabase(t testing.TB, dbFile string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(dbFile), 0o700))

	//nolint:gosec
	cmd := exec.CommandContext(context.Background(),
		"ncps",
		"migrate",
		"up",
		"--cache-database-url=sqlite:"+dbFile,
	)

	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "Running %q has failed", cmd.String())

	t.Logf("%s: %s", cmd.String(), output)

	// IMPORTANT: Checkpoint the WAL to ensure all changes are written to the main database file.
	// While not strictly necessary for correctness (SQLite handles this automatically), it ensures
	// that all migration changes are immediately visible to new connections without relying on
	// WAL file reads. This can prevent subtle issues in test environments with rapid database
	// create-destroy cycles. See: https://www.sqlite.org/wal.html#checkpointing
	db, err := sql.Open("sqlite3", dbFile)
	require.NoError(t, err)

	defer db.Close()

	_, err = db.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)")
	require.NoError(t, err)
}

// MigrateNarInfoToDatabase migrates a single narinfo to the database.
// This is a test helper that mimics the migration logic in pkg/ncps/migrate_narinfo.go.
func MigrateNarInfoToDatabase(ctx context.Context, db *bun.DB, hash string, ni *narinfo.NarInfo) error {
	return database.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		// Get or Create NarInfo
		nir, err := getOrCreateNarInfo(ctx, tx, hash, ni)
		if err != nil {
			return err
		}

		// References
		if len(ni.References) > 0 {
			err := database.AddNarInfoReferences(ctx, tx, database.AddNarInfoReferencesParams{
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
			err := database.AddNarInfoSignatures(ctx, tx, database.AddNarInfoSignaturesParams{
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
		narFile, err := getOrCreateNarFile(ctx, tx, &narURL, ni.FileSize)
		if err != nil {
			return err
		}

		// Link NarInfo to NarFile
		if err := database.LinkNarInfoToNarFile(ctx, tx, database.LinkNarInfoToNarFileParams{
			NarInfoID: nir.ID,
			NarFileID: narFile.ID,
		}); err != nil {
			if !database.IsDuplicateKeyError(err) {
				return fmt.Errorf("failed to link narinfo to narfile: %w", err)
			}
		}

		return nil
	})
}

// RegisterNarInfoAsUnmigrated registers a narinfo in the database as unmigrated (no URL).
func RegisterNarInfoAsUnmigrated(ctx context.Context, db *bun.DB, hash string, ni *narinfo.NarInfo) error {
	_, err := database.CreateNarInfo(ctx, db, database.CreateNarInfoParams{
		Hash:        hash,
		StorePath:   sql.NullString{String: ni.StorePath, Valid: ni.StorePath != ""},
		Compression: sql.NullString{String: ni.Compression, Valid: ni.Compression != ""},
		FileHash:    sql.NullString{String: ni.FileHash.String(), Valid: ni.FileHash != nil},
		FileSize:    sql.NullInt64{Int64: int64(ni.FileSize), Valid: true}, //nolint:gosec
		NarHash:     sql.NullString{String: ni.NarHash.String(), Valid: ni.NarHash != nil},
		NarSize:     sql.NullInt64{Int64: int64(ni.NarSize), Valid: true}, //nolint:gosec
		Deriver:     sql.NullString{String: ni.Deriver, Valid: ni.Deriver != ""},
		System:      sql.NullString{String: ni.System, Valid: ni.System != ""},
		Ca:          sql.NullString{String: ni.CA, Valid: ni.CA != ""},
		// URL is intentionally omitted/NULL
	})

	return err
}

func getOrCreateNarInfo(
	ctx context.Context,
	db bun.IDB,
	hash string,
	ni *narinfo.NarInfo,
) (database.NarInfo, error) {
	// First, try to get the record.
	existing, err := database.GetNarInfoByHash(ctx, db, hash)
	if err == nil {
		// Found it, return.
		return existing, nil
	}

	// If the error is anything other than "not found", it's a real error.
	if !database.IsNotFoundError(err) {
		return database.NarInfo{}, fmt.Errorf("failed to get narinfo record: %w", err)
	}

	// Not found, so let's create it.
	nir, err := database.CreateNarInfo(ctx, db, database.CreateNarInfoParams{
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
			existing, errGet := database.GetNarInfoByHash(ctx, db, hash)
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
	db bun.IDB,
	narURL *nar.URL,
	narSize uint64,
) (database.NarFile, error) {
	// First, try to get the record.
	existing, err := database.GetNarFileByHashAndCompressionAndQuery(
		ctx, db, narURL.Hash, narURL.Compression.String(), narURL.Query.Encode(),
	)
	if err == nil {
		// Found it, return.
		return existing, nil
	}

	// If the error is anything other than "not found", it's a real error.
	if !database.IsNotFoundError(err) {
		return database.NarFile{}, fmt.Errorf("failed to get existing nar file record: %w", err)
	}

	// Not found, so let's create it.
	narFile, err := database.CreateNarFile(ctx, db, database.CreateNarFileParams{
		Hash:        narURL.Hash,
		Compression: narURL.Compression.String(),
		Query:       narURL.Query.Encode(),
		FileSize:    narSize,
	})
	if err != nil {
		// If we get a duplicate key error, it means another worker created it.
		if database.IsDuplicateKeyError(err) {
			// Fetch the record again. This time it should exist.
			existing, errGet := database.GetNarFileByHashAndCompressionAndQuery(
				ctx, db, narURL.Hash, narURL.Compression.String(), narURL.Query.Encode(),
			)
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
func SetupSQLite(t *testing.T) (*bun.DB, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "sqlite-test-")
	require.NoError(t, err)

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	cleanup := func() {
		db.DB.Close()
		os.RemoveAll(dir)
	}

	return db, cleanup
}
