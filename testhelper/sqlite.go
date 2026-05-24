package testhelper

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/stretchr/testify/require"

	entnarfile "github.com/kalbasit/ncps/ent/narfile"
	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"
	entnarinfonarfile "github.com/kalbasit/ncps/ent/narinfonarfile"
	entnarinforeference "github.com/kalbasit/ncps/ent/narinforeference"
	entnarinfosignature "github.com/kalbasit/ncps/ent/narinfosignature"

	"github.com/kalbasit/ncps/ent"
	"github.com/kalbasit/ncps/migrations"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/database/migrate"
	"github.com/kalbasit/ncps/pkg/nar"
)

// CreateMigrateDatabase creates the parent directory tree and a fresh
// SQLite database, then runs the same migrate.Up flow `ncps migrate up`
// uses in production. Empty DB → ent.Schema.Create → final schema
// including the §10b surrogate-id columns on weak entities. Tests can
// now perform Ent edge traversals (WithReferences/WithSignatures/
// WithNarInfoNarFiles) that the legacy dbmate-only schema didn't
// support.
func CreateMigrateDatabase(t testing.TB, dbFile string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(dbFile), 0o700))

	db, err := sql.Open("sqlite3",
		"file:"+dbFile+"?_fk=1&_journal_mode=WAL&_busy_timeout=10000")
	require.NoError(t, err)

	defer db.Close()

	sub, err := fs.Sub(migrations.FS, "sqlite")
	require.NoError(t, err)

	require.NoError(t, migrate.Up(context.Background(), migrate.Options{
		DB:           db,
		Dialect:      database.TypeSQLite,
		MigrationsFS: sub,
	}))

	// Checkpoint the WAL so on-disk file content is visible to other
	// connections immediately. Not strictly required for correctness
	// — SQLite handles WAL transparently — but prevents subtle
	// rapid-create/destroy timing issues in tests. See
	// https://www.sqlite.org/wal.html#checkpointing
	_, err = db.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)")
	require.NoError(t, err)
}

// MigrateNarInfoToDatabase migrates a single narinfo to the database
// via the Ent API. This is a test helper that mimics the migration
// logic in pkg/ncps/migrate_narinfo.go.
func MigrateNarInfoToDatabase(
	ctx context.Context,
	dbClient *database.Client,
	hash string,
	ni *narinfo.NarInfo,
) error {
	return dbClient.WithTransaction(ctx, "testhelper.MigrateNarInfoToDatabase", func(tx *ent.Tx) error {
		// Get or Create NarInfo
		nir, err := getOrCreateNarInfo(ctx, tx, hash, ni)
		if err != nil {
			return err
		}

		// References
		for _, ref := range ni.References {
			if err := tx.NarInfoReference.Create().
				SetNarinfoID(nir.ID).
				SetReference(ref).
				OnConflictColumns(
					entnarinforeference.FieldNarinfoID,
					entnarinforeference.FieldReference,
				).
				Ignore().
				Exec(ctx); err != nil {
				return fmt.Errorf("failed to add reference %q: %w", ref, err)
			}
		}

		// Signatures
		for _, sig := range ni.Signatures {
			s := sig.String()

			if err := tx.NarInfoSignature.Create().
				SetNarinfoID(nir.ID).
				SetSignature(s).
				OnConflictColumns(
					entnarinfosignature.FieldNarinfoID,
					entnarinfosignature.FieldSignature,
				).
				Ignore().
				Exec(ctx); err != nil {
				return fmt.Errorf("failed to add signature %q: %w", s, err)
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
		if err := tx.NarInfoNarFile.Create().
			SetNarinfoID(nir.ID).
			SetNarFileID(narFile.ID).
			OnConflictColumns(
				entnarinfonarfile.FieldNarinfoID,
				entnarinfonarfile.FieldNarFileID,
			).
			Ignore().
			Exec(ctx); err != nil {
			return fmt.Errorf("failed to link narinfo to narfile: %w", err)
		}

		return nil
	})
}

// RegisterNarInfoAsUnmigrated registers a narinfo in the database as
// unmigrated (no URL).
func RegisterNarInfoAsUnmigrated(
	ctx context.Context,
	dbClient *database.Client,
	hash string,
	ni *narinfo.NarInfo,
) error {
	builder := dbClient.Ent().NarInfo.Create().SetHash(hash)

	if ni.StorePath != "" {
		builder = builder.SetStorePath(ni.StorePath)
	}

	if ni.Compression != "" {
		builder = builder.SetCompression(ni.Compression)
	}

	if ni.FileHash != nil {
		builder = builder.SetFileHash(ni.FileHash.String())
	}

	// Mirror narFileSize(): compression=none narinfos omit FileSize (0);
	// NarSize is the correct fallback so nar_files.file_size is always non-zero.
	fileSize := ni.FileSize
	if fileSize == 0 {
		fileSize = ni.NarSize
	}

	//nolint:gosec
	builder = builder.SetFileSize(int64(fileSize))

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
	// URL is intentionally omitted/NULL

	_, err := builder.Save(ctx)

	return err
}

func getOrCreateNarInfo(
	ctx context.Context,
	tx *ent.Tx,
	hash string,
	ni *narinfo.NarInfo,
) (*ent.NarInfo, error) {
	// First, try to get the record.
	existing, err := tx.NarInfo.Query().Where(entnarinfo.HashEQ(hash)).Only(ctx)
	if err == nil {
		return existing, nil
	}

	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get narinfo record: %w", err)
	}

	// Not found, so let's create it.
	builder := tx.NarInfo.Create().SetHash(hash)

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

	// Mirror narFileSize(): compression=none narinfos omit FileSize (0);
	// NarSize is the correct fallback so nar_files.file_size is always non-zero.
	fileSize := ni.FileSize
	if fileSize == 0 {
		fileSize = ni.NarSize
	}

	//nolint:gosec
	builder = builder.SetFileSize(int64(fileSize))

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

	nir, err := builder.Save(ctx)
	if err != nil {
		// If we get a duplicate key error, another worker created it
		// between our GET and CREATE. Fetch the record again.
		if isDuplicateKey(err) {
			existing, errGet := tx.NarInfo.Query().Where(entnarinfo.HashEQ(hash)).Only(ctx)
			if errGet != nil {
				return nil, fmt.Errorf("failed to get existing record after race: %w", errGet)
			}

			return existing, nil
		}

		return nil, fmt.Errorf("failed to create narinfo record: %w", err)
	}

	return nir, nil
}

func getOrCreateNarFile(
	ctx context.Context,
	tx *ent.Tx,
	narURL *nar.URL,
	narSize uint64,
) (*ent.NarFile, error) {
	existing, err := tx.NarFile.Query().
		Where(
			entnarfile.HashEQ(narURL.Hash),
			entnarfile.CompressionEQ(narURL.Compression.String()),
			entnarfile.QueryEQ(narURL.Query.Encode()),
		).
		Only(ctx)
	if err == nil {
		return existing, nil
	}

	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get existing nar file record: %w", err)
	}

	narFile, err := tx.NarFile.Create().
		SetHash(narURL.Hash).
		SetCompression(narURL.Compression.String()).
		SetQuery(narURL.Query.Encode()).
		SetFileSize(narSize).
		Save(ctx)
	if err != nil {
		if isDuplicateKey(err) {
			existing, errGet := tx.NarFile.Query().
				Where(
					entnarfile.HashEQ(narURL.Hash),
					entnarfile.CompressionEQ(narURL.Compression.String()),
					entnarfile.QueryEQ(narURL.Query.Encode()),
				).
				Only(ctx)
			if errGet != nil {
				return nil, fmt.Errorf("failed to get existing nar file record after race: %w", errGet)
			}

			return existing, nil
		}

		return nil, fmt.Errorf("error creating the nar file record: %w", err)
	}

	return narFile, nil
}

// isDuplicateKey returns true if the error appears to be a unique-key
// conflict. Ent surfaces these as *ent.ConstraintError; tests don't
// need driver-specific distinctions.
func isDuplicateKey(err error) bool {
	var cerr *ent.ConstraintError

	return errors.As(err, &cerr)
}

// SetupSQLite sets up a new temporary SQLite database for testing.
// It returns the Ent-backed *database.Client and a cleanup function.
func SetupSQLite(t *testing.T) (*database.Client, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "sqlite-test-")
	require.NoError(t, err)

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	CreateMigrateDatabase(t, dbFile)

	dbClient, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	cleanup := func() {
		_ = dbClient.Close()

		os.RemoveAll(dir)
	}

	return dbClient, cleanup
}
