package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"entgo.io/ent/dialect"
	"github.com/pressly/goose/v3/database"

	entsql "entgo.io/ent/dialect/sql"
	entschema "entgo.io/ent/dialect/sql/schema"
	entmigrate "github.com/kalbasit/ncps/ent/migrate"
	ncpsdb "github.com/kalbasit/ncps/pkg/database"
)

// The Schema.Create serialisation mutex lives in pkg/database
// (ncpsdb.SchemaCreateMu) so every caller in the process — this
// package, pkg/database's own tests, and testhelper's
// CreateMigrateDatabase — shares one source of truth.

// freshInstall produces the entire Ent-expected schema on an empty
// database via Ent's runtime `Schema.Create`, then seeds the goose
// tracking table (`schema_migrations`) with every version present in
// migrationsFS marked is_applied=true. Goose subsequently sees zero
// pending migrations.
//
// Per design D6 (Option E), this path is taken when StateEmpty is
// observed. The end-state must match what applying every
// `migrations/<dialect>/*.sql` in order would produce, a property
// gated by the §8 schema-equivalence test.
//
// To avoid shape conflicts with goose's own version-table CREATE we
// delegate the tracking-table creation and inserts to goose's
// `database.Store` API, then call `goose.Up` (which is now a no-op
// because every version is already recorded).
func freshInstall(
	ctx context.Context,
	db *sql.DB,
	d ncpsdb.Type,
	migrationsFS fs.FS,
) error {
	entDialect, err := entDialectFor(d)
	if err != nil {
		return err
	}

	// 1. Ent's Schema.Create produces the application tables in one shot.
	//    Serialise across goroutines — Ent mutates the package-level
	//    migrate.Tables slice during Create, so concurrent callers race.
	ncpsdb.SchemaCreateMu.Lock()

	drv := entsql.OpenDB(entDialect, db)

	m, err := entschema.NewMigrate(
		drv,
		entschema.WithDialect(entDialect),
	)
	if err != nil {
		ncpsdb.SchemaCreateMu.Unlock()

		return fmt.Errorf("NewMigrate: %w", err)
	}

	createErr := m.Create(ctx, entmigrate.Tables...)

	ncpsdb.SchemaCreateMu.Unlock()

	if createErr != nil {
		return fmt.Errorf("Schema.Create: %w", createErr)
	}

	// 2. Use goose's own store to create schema_migrations + insert
	//    versions. Going through goose's API guarantees the table
	//    shape exactly matches what goose subsequently expects.
	gooseDia, err := gooseStoreDialectFor(d)
	if err != nil {
		return err
	}

	store, err := database.NewStore(gooseDia, "schema_migrations")
	if err != nil {
		return fmt.Errorf("goose NewStore: %w", err)
	}

	if err := store.CreateVersionTable(ctx, db); err != nil {
		return fmt.Errorf("create version table: %w", err)
	}

	// Goose's CreateVersionTable inserts no sentinel row of its own; it
	// expects subsequent code (its own Up flow normally) to insert
	// version 0 as the "table initialized" marker. We do that here so
	// goose's later existence-check fallback (which queries for
	// version 0) finds the row and skips its own CREATE TABLE attempt.
	if err := store.Insert(ctx, db, database.InsertRequest{Version: 0}); err != nil {
		return fmt.Errorf("insert sentinel version 0: %w", err)
	}

	versions, err := listEmbeddedVersions(migrationsFS)
	if err != nil {
		return fmt.Errorf("list embedded versions: %w", err)
	}

	for _, v := range versions {
		if err := store.Insert(ctx, db, database.InsertRequest{Version: v}); err != nil {
			return fmt.Errorf("insert version %d: %w", v, err)
		}
	}

	return nil
}

// entDialectFor maps ncps's dialect enum to ent's dialect string.
func entDialectFor(d ncpsdb.Type) (string, error) {
	switch d {
	case ncpsdb.TypeSQLite:
		return dialect.SQLite, nil
	case ncpsdb.TypePostgreSQL:
		return dialect.Postgres, nil
	case ncpsdb.TypeMySQL:
		return dialect.MySQL, nil
	case ncpsdb.TypeUnknown:
		fallthrough
	default:
		return "", fmt.Errorf("entDialectFor: %w %v", ErrUnknownDialect, d)
	}
}

// gooseStoreDialectFor maps ncps's dialect enum to goose's database
// dialect identifier (the one used by `database.NewStore`).
func gooseStoreDialectFor(d ncpsdb.Type) (database.Dialect, error) {
	switch d {
	case ncpsdb.TypeSQLite:
		return database.DialectSQLite3, nil
	case ncpsdb.TypePostgreSQL:
		return database.DialectPostgres, nil
	case ncpsdb.TypeMySQL:
		return database.DialectMySQL, nil
	case ncpsdb.TypeUnknown:
		fallthrough
	default:
		return "", fmt.Errorf("gooseStoreDialectFor: %w %v", ErrUnknownDialect, d)
	}
}

// listEmbeddedVersions walks the dialect-specific sub-FS and returns the
// numeric timestamp prefix of every `*.sql` file. Goose stores versions
// as int64 derived from the leading 14-char timestamp.
func listEmbeddedVersions(sub fs.FS) ([]int64, error) {
	var versions []int64

	err := fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !strings.HasSuffix(p, ".sql") {
			return nil
		}

		base := p
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}

		if len(base) < 14 {
			//nolint:err113 // diagnostic
			return fmt.Errorf("migration filename too short: %q", p)
		}

		v, err := strconv.ParseInt(base[:14], 10, 64)
		if err != nil {
			return fmt.Errorf("parse timestamp from %q: %w", p, err)
		}

		versions = append(versions, v)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return versions, nil
}
