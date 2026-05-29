package migrations_test

import (
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/migrations"

	_ "github.com/mattn/go-sqlite3"
)

// TestGooseApplySQLite proves the translated SQLite migrations apply
// cleanly via goose against a fresh database, in timestamp order, with
// the canonical `schema_migrations` tracking table name (preserving the
// dbmate-era identifier per design D6).
//
// This is a smoke test for §7. The full schema-equivalence guarantee
// (post-apply schema matches the Ent expected shape via `atlas migrate diff`)
// across all three dialects is enforced in §8 once
// cmd/generate-migrations is in place.
func TestGooseApplySQLite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")

	db, err := sql.Open("sqlite3", "file:"+dbFile+"?_fk=1&_journal_mode=WAL")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	sub, err := fs.Sub(migrations.FS, "sqlite")
	require.NoError(t, err)

	provider, err := goose.NewProvider(
		goose.DialectSQLite3, db, sub,
		goose.WithTableName("schema_migrations"),
	)
	require.NoError(t, err)

	results, err := provider.Up(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, results, "expected at least one migration to apply")
	t.Logf("applied %d migration(s)", len(results))

	// Verify the tracking table has rows with is_applied=true.
	var applied int

	err = db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM "schema_migrations" WHERE "is_applied" = 1`).
		Scan(&applied)
	require.NoError(t, err)
	require.GreaterOrEqual(t, applied, 14, "expected at least 14 SQLite migrations recorded")

	// Verify the load-bearing tables exist (sanity check; full shape parity
	// is enforced in §8).
	for _, table := range []string{"narinfos", "nar_files", "chunks", "pinned_closures"} {
		var name string

		err := db.QueryRowContext(t.Context(),
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		require.NoErrorf(t, err, "table %q not present after migrations", table)
	}
}
