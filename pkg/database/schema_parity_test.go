package database_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/testhelper"
)

// TestSchemaParity asserts the *shape* of the database schema (table
// existence, column existence, constraint behaviour) against the canonical
// `data-model` specification. It is the TDD anchor for the
// migrate-to-ent-and-atlas change: every subsequent step (Ent schemas,
// translated migrations, generated migrations) must keep these assertions
// passing across SQLite, PostgreSQL, and MySQL.
func TestSchemaParity(t *testing.T) {
	t.Parallel()

	backends := []struct {
		name    string
		envVar  string
		setup   func(t *testing.T) (database.Querier, func())
		dialect database.Type
	}{
		{
			name: "SQLite",
			setup: func(t *testing.T) (database.Querier, func()) {
				t.Helper()

				dir, err := os.MkdirTemp("", "schema-parity-sqlite-")
				require.NoError(t, err)

				dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
				testhelper.CreateMigrateDatabase(t, dbFile)
				db, err := database.Open("sqlite:"+dbFile, nil)
				require.NoError(t, err)

				cleanup := func() {
					db.DB().Close()
					os.RemoveAll(dir)
				}

				return db, cleanup
			},
			dialect: database.TypeSQLite,
		},
		{
			name:   "PostgreSQL",
			envVar: "NCPS_TEST_ADMIN_POSTGRES_URL",
			setup: func(t *testing.T) (database.Querier, func()) {
				t.Helper()
				db, _, cleanup := testhelper.SetupPostgres(t)

				return db, cleanup
			},
			dialect: database.TypePostgreSQL,
		},
		{
			name:   "MySQL",
			envVar: "NCPS_TEST_ADMIN_MYSQL_URL",
			setup: func(t *testing.T) (database.Querier, func()) {
				t.Helper()
				db, _, cleanup := testhelper.SetupMySQL(t)

				return db, cleanup
			},
			dialect: database.TypeMySQL,
		},
	}

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			if b.envVar != "" && os.Getenv(b.envVar) == "" {
				t.Skipf("Skipping %s: %s not set", b.name, b.envVar)
			}

			db, cleanup := b.setup(t)
			t.Cleanup(cleanup)

			runSchemaParity(t, db, b.dialect)
		})
	}
}

func runSchemaParity(t *testing.T, q database.Querier, d database.Type) {
	t.Helper()

	ctx := context.Background()
	sqlDB := q.DB()

	t.Run("tables exist", func(t *testing.T) {
		t.Parallel()

		got := mustListTables(ctx, t, sqlDB, d)
		for _, want := range expectedTables {
			assert.Containsf(t, got, want, "table %q must exist", want)
		}
	})

	t.Run("columns exist", func(t *testing.T) {
		t.Parallel()

		for _, tbl := range expectedColumns {
			got := mustListColumns(ctx, t, sqlDB, d, tbl.table)
			for _, col := range tbl.columns {
				assert.Containsf(t, got, col, "column %q.%q must exist", tbl.table, col)
			}
		}
	})

	t.Run("CHECK narinfos.file_size rejects negative", func(t *testing.T) {
		t.Parallel()

		err := execf(ctx, sqlDB, d,
			`INSERT INTO narinfos ("hash", "file_size") VALUES (?, ?)`,
			testhelper.MustRandString(32), int64(-1))
		require.Error(t, err)
	})

	t.Run("CHECK narinfos.nar_size rejects negative", func(t *testing.T) {
		t.Parallel()

		err := execf(ctx, sqlDB, d,
			`INSERT INTO narinfos ("hash", "nar_size") VALUES (?, ?)`,
			testhelper.MustRandString(32), int64(-1))
		require.Error(t, err)
	})

	t.Run("CHECK chunks.size rejects negative", func(t *testing.T) {
		t.Parallel()

		err := execf(ctx, sqlDB, d,
			`INSERT INTO chunks ("hash", "size", "compressed_size") VALUES (?, ?, ?)`,
			testhelper.MustRandString(52), int64(-1), int64(0))
		require.Error(t, err)
	})

	t.Run("CHECK chunks.compressed_size rejects negative", func(t *testing.T) {
		t.Parallel()

		err := execf(ctx, sqlDB, d,
			`INSERT INTO chunks ("hash", "size", "compressed_size") VALUES (?, ?, ?)`,
			testhelper.MustRandString(52), int64(0), int64(-1))
		require.Error(t, err)
	})

	t.Run("UNIQUE narinfos.hash rejects duplicate", func(t *testing.T) {
		t.Parallel()

		h := testhelper.MustRandString(32)
		require.NoError(t, execf(ctx, sqlDB, d,
			`INSERT INTO narinfos ("hash") VALUES (?)`, h))
		err := execf(ctx, sqlDB, d,
			`INSERT INTO narinfos ("hash") VALUES (?)`, h)
		require.Error(t, err)
	})

	t.Run("UNIQUE chunks.hash rejects duplicate", func(t *testing.T) {
		t.Parallel()

		h := testhelper.MustRandString(52)
		require.NoError(t, execf(ctx, sqlDB, d,
			`INSERT INTO chunks ("hash", "size", "compressed_size") VALUES (?, ?, ?)`,
			h, int64(1), int64(1)))
		err := execf(ctx, sqlDB, d,
			`INSERT INTO chunks ("hash", "size", "compressed_size") VALUES (?, ?, ?)`,
			h, int64(2), int64(2))
		require.Error(t, err)
	})

	t.Run("UNIQUE pinned_closures.hash rejects duplicate", func(t *testing.T) {
		t.Parallel()

		h := testhelper.MustRandString(32)
		require.NoError(t, execf(ctx, sqlDB, d,
			`INSERT INTO pinned_closures ("hash") VALUES (?)`, h))
		err := execf(ctx, sqlDB, d,
			`INSERT INTO pinned_closures ("hash") VALUES (?)`, h)
		require.Error(t, err)
	})

	t.Run("UNIQUE config.key rejects duplicate", func(t *testing.T) {
		t.Parallel()

		k := testhelper.MustRandString(32)
		require.NoError(t, execf(ctx, sqlDB, d,
			`INSERT INTO config ("key", "value") VALUES (?, ?)`, k, "v1"))
		err := execf(ctx, sqlDB, d,
			`INSERT INTO config ("key", "value") VALUES (?, ?)`, k, "v2")
		require.Error(t, err)
	})

	t.Run("UNIQUE nar_files (hash, compression, query) rejects duplicate", func(t *testing.T) {
		t.Parallel()

		h := testhelper.MustRandString(32)
		require.NoError(t, execf(ctx, sqlDB, d,
			`INSERT INTO nar_files ("hash", "compression", "file_size", "query") VALUES (?, ?, ?, ?)`,
			h, "xz", int64(1), ""))
		err := execf(ctx, sqlDB, d,
			`INSERT INTO nar_files ("hash", "compression", "file_size", "query") VALUES (?, ?, ?, ?)`,
			h, "xz", int64(2), "")
		require.Error(t, err)
	})

	t.Run("ON DELETE CASCADE narinfo_references", func(t *testing.T) {
		t.Parallel()
		niID := insertNarinfoAndID(ctx, t, sqlDB, d, testhelper.MustRandString(32))
		require.NoError(t, execf(ctx, sqlDB, d,
			`INSERT INTO narinfo_references ("narinfo_id", "reference") VALUES (?, ?)`,
			niID, "ref-a"))
		require.NoError(t, execf(ctx, sqlDB, d,
			`INSERT INTO narinfo_references ("narinfo_id", "reference") VALUES (?, ?)`,
			niID, "ref-b"))
		require.Equal(t, 2, countRows(ctx, t, sqlDB, d,
			`SELECT COUNT(*) FROM narinfo_references WHERE "narinfo_id" = ?`, niID))
		require.NoError(t, execf(ctx, sqlDB, d,
			`DELETE FROM narinfos WHERE "id" = ?`, niID))
		assert.Equal(t, 0, countRows(ctx, t, sqlDB, d,
			`SELECT COUNT(*) FROM narinfo_references WHERE "narinfo_id" = ?`, niID))
	})

	t.Run("ON DELETE CASCADE narinfo_signatures", func(t *testing.T) {
		t.Parallel()
		niID := insertNarinfoAndID(ctx, t, sqlDB, d, testhelper.MustRandString(32))
		require.NoError(t, execf(ctx, sqlDB, d,
			`INSERT INTO narinfo_signatures ("narinfo_id", "signature") VALUES (?, ?)`,
			niID, "sig-a"))
		require.NoError(t, execf(ctx, sqlDB, d,
			`INSERT INTO narinfo_signatures ("narinfo_id", "signature") VALUES (?, ?)`,
			niID, "sig-b"))
		require.Equal(t, 2, countRows(ctx, t, sqlDB, d,
			`SELECT COUNT(*) FROM narinfo_signatures WHERE "narinfo_id" = ?`, niID))
		require.NoError(t, execf(ctx, sqlDB, d,
			`DELETE FROM narinfos WHERE "id" = ?`, niID))
		assert.Equal(t, 0, countRows(ctx, t, sqlDB, d,
			`SELECT COUNT(*) FROM narinfo_signatures WHERE "narinfo_id" = ?`, niID))
	})

	t.Run("ON DELETE CASCADE narinfo_nar_files via narinfo", func(t *testing.T) {
		t.Parallel()
		niID := insertNarinfoAndID(ctx, t, sqlDB, d, testhelper.MustRandString(32))
		nfID := insertNarFileAndID(ctx, t, sqlDB, d, testhelper.MustRandString(32), "xz", "")
		require.NoError(t, execf(ctx, sqlDB, d,
			`INSERT INTO narinfo_nar_files ("narinfo_id", "nar_file_id") VALUES (?, ?)`,
			niID, nfID))
		require.NoError(t, execf(ctx, sqlDB, d,
			`DELETE FROM narinfos WHERE "id" = ?`, niID))
		assert.Equal(t, 0, countRows(ctx, t, sqlDB, d,
			`SELECT COUNT(*) FROM narinfo_nar_files WHERE "narinfo_id" = ?`, niID))
	})

	t.Run("ON DELETE CASCADE nar_file_chunks via nar_file", func(t *testing.T) {
		t.Parallel()
		nfID := insertNarFileAndID(ctx, t, sqlDB, d, testhelper.MustRandString(32), "xz", "")
		chunkHash := testhelper.MustRandString(52)
		require.NoError(t, execf(ctx, sqlDB, d,
			`INSERT INTO chunks ("hash", "size", "compressed_size") VALUES (?, ?, ?)`,
			chunkHash, int64(1), int64(1)))
		chunkID := selectInt64(ctx, t, sqlDB, d,
			`SELECT "id" FROM chunks WHERE "hash" = ?`, chunkHash)
		require.NoError(t, execf(ctx, sqlDB, d,
			`INSERT INTO nar_file_chunks ("nar_file_id", "chunk_id", "chunk_index") VALUES (?, ?, ?)`,
			nfID, chunkID, 0))
		require.Equal(t, 1, countRows(ctx, t, sqlDB, d,
			`SELECT COUNT(*) FROM nar_file_chunks WHERE "nar_file_id" = ?`, nfID))
		require.NoError(t, execf(ctx, sqlDB, d,
			`DELETE FROM nar_files WHERE "id" = ?`, nfID))
		assert.Equal(t, 0, countRows(ctx, t, sqlDB, d,
			`SELECT COUNT(*) FROM nar_file_chunks WHERE "nar_file_id" = ?`, nfID))
	})
}

// ---------- expected shape ----------

//nolint:gochecknoglobals // immutable test fixture
var expectedTables = []string{
	"config",
	"narinfos",
	"narinfo_references",
	"narinfo_signatures",
	"nar_files",
	"narinfo_nar_files",
	"chunks",
	"nar_file_chunks",
	"pinned_closures",
}

type tableColumns struct {
	table   string
	columns []string
}

//nolint:gochecknoglobals // immutable test fixture
var expectedColumns = []tableColumns{
	{table: "config", columns: []string{"id", "key", "value", "created_at", "updated_at"}},
	{table: "narinfos", columns: []string{
		"id", "hash", "store_path", "url", "compression",
		"file_hash", "file_size", "nar_hash", "nar_size",
		"deriver", "system", "ca",
		"created_at", "updated_at", "last_accessed_at",
	}},
	{table: "narinfo_references", columns: []string{"narinfo_id", "reference"}},
	{table: "narinfo_signatures", columns: []string{"narinfo_id", "signature"}},
	{table: "nar_files", columns: []string{
		"id", "hash", "compression", "file_size", "query",
		"total_chunks", "chunking_started_at", "verified_at",
		"created_at", "updated_at", "last_accessed_at",
	}},
	{table: "narinfo_nar_files", columns: []string{"narinfo_id", "nar_file_id"}},
	{table: "chunks", columns: []string{"id", "hash", "size", "compressed_size", "created_at", "updated_at"}},
	{table: "nar_file_chunks", columns: []string{"nar_file_id", "chunk_id", "chunk_index"}},
	{table: "pinned_closures", columns: []string{"id", "hash", "created_at", "updated_at"}},
}

// ---------- dialect-aware helpers ----------

// execf executes a `?`-templated SQL statement after rewriting placeholders
// and identifier quoting for the target dialect:
//   - Postgres: `?` → `$1, $2, …` (double-quoted identifiers OK)
//   - SQLite:   unchanged
//   - MySQL:    `"<ident>"` → “ `<ident>` “  (backtick identifiers)
func execf(ctx context.Context, db *sql.DB, d database.Type, query string, args ...any) error {
	_, err := db.ExecContext(ctx, rewriteForDialect(d, query), args...)

	return err
}

// queryRowf is the QueryRow equivalent of execf.
func queryRowf(ctx context.Context, db *sql.DB, d database.Type, query string, args ...any) *sql.Row {
	return db.QueryRowContext(ctx, rewriteForDialect(d, query), args...)
}

// rewriteForDialect adjusts a `?`-templated, double-quoted-identifier SQL
// statement for the target dialect. Assumes neither `?` nor `"` appear
// inside string literals (true for all SQL in this file).
func rewriteForDialect(d database.Type, query string) string {
	switch d {
	case database.TypePostgreSQL:
		return rewritePlaceholders(query)
	case database.TypeMySQL:
		// MariaDB doesn't parse "ident" as an identifier unless ANSI_QUOTES
		// is set; backticks are the portable form.
		return strings.ReplaceAll(query, `"`, "`")
	case database.TypeSQLite, database.TypeUnknown:
		fallthrough
	default:
		return query
	}
}

// rewritePlaceholders replaces each `?` in query with $1,$2,... (Postgres).
func rewritePlaceholders(query string) string {
	var (
		out strings.Builder
		n   int
	)
	out.Grow(len(query) + 8)

	for _, r := range query {
		if r == '?' {
			n++
			fmt.Fprintf(&out, "$%d", n)

			continue
		}

		out.WriteRune(r)
	}

	return out.String()
}

func selectInt64(ctx context.Context, t *testing.T, db *sql.DB, d database.Type, query string, args ...any) int64 {
	t.Helper()

	var v int64
	require.NoError(t, queryRowf(ctx, db, d, query, args...).Scan(&v))

	return v
}

func countRows(ctx context.Context, t *testing.T, db *sql.DB, d database.Type, query string, args ...any) int {
	t.Helper()

	var n int
	require.NoError(t, queryRowf(ctx, db, d, query, args...).Scan(&n))

	return n
}

func insertNarinfoAndID(ctx context.Context, t *testing.T, db *sql.DB, d database.Type, hash string) int64 {
	t.Helper()
	require.NoError(t, execf(ctx, db, d,
		`INSERT INTO narinfos ("hash") VALUES (?)`, hash))

	return selectInt64(ctx, t, db, d,
		`SELECT "id" FROM narinfos WHERE "hash" = ?`, hash)
}

func insertNarFileAndID(
	ctx context.Context, t *testing.T, db *sql.DB, d database.Type,
	hash, compression, query string,
) int64 {
	t.Helper()
	require.NoError(t, execf(ctx, db, d,
		`INSERT INTO nar_files ("hash", "compression", "file_size", "query") VALUES (?, ?, ?, ?)`,
		hash, compression, int64(1), query))

	return selectInt64(ctx, t, db, d,
		`SELECT "id" FROM nar_files WHERE "hash" = ? AND "compression" = ? AND "query" = ?`,
		hash, compression, query)
}

// ---------- introspection ----------

func mustListTables(ctx context.Context, t *testing.T, db *sql.DB, d database.Type) []string {
	t.Helper()

	var query string

	switch d {
	case database.TypeSQLite:
		query = `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`
	case database.TypePostgreSQL:
		query = `SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'`
	case database.TypeMySQL:
		query = `SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()`
	case database.TypeUnknown:
		fallthrough
	default:
		t.Fatalf("unknown dialect: %v", d)
	}

	rows, err := db.QueryContext(ctx, query)
	require.NoError(t, err)

	defer rows.Close()

	var out []string

	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		// Normalize to lowercase: some MySQL configurations return
		// table names in their stored case which may differ from the
		// expectedTables list. Consistent with how column names are
		// normalized in mustListColumns.
		out = append(out, strings.ToLower(name))
	}

	require.NoError(t, rows.Err())
	sort.Strings(out)

	return out
}

func mustListColumns(ctx context.Context, t *testing.T, db *sql.DB, d database.Type, table string) []string {
	t.Helper()

	var (
		query string
		args  []any
	)

	switch d {
	case database.TypeSQLite:
		query = fmt.Sprintf(`PRAGMA table_info(%q)`, table)
	case database.TypePostgreSQL:
		query = `SELECT column_name FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1`
		args = []any{table}
	case database.TypeMySQL:
		query = `SELECT column_name FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = ?`
		args = []any{table}
	case database.TypeUnknown:
		fallthrough
	default:
		t.Fatalf("unknown dialect: %v", d)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	require.NoError(t, err)

	defer rows.Close()

	var out []string

	for rows.Next() {
		switch d {
		case database.TypeSQLite:
			// PRAGMA table_info: cid, name, type, notnull, dflt_value, pk
			var (
				cid         int
				name, ctype string
				notnull     int
				dflt        sql.NullString
				pk          int
			)

			require.NoError(t, rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk))
			// Lowercase for consistency with the Postgres/MySQL paths
			// below and with the expectedColumns list.
			out = append(out, strings.ToLower(name))
		case database.TypeMySQL, database.TypePostgreSQL, database.TypeUnknown:
			fallthrough
		default:
			var name string
			require.NoError(t, rows.Scan(&name))
			out = append(out, strings.ToLower(name))
		}
	}

	require.NoError(t, rows.Err())
	sort.Strings(out)

	return out
}
