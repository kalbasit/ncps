package database_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/stretchr/testify/require"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/kalbasit/ncps/ent/migrate"
	"github.com/kalbasit/ncps/pkg/database"

	_ "github.com/mattn/go-sqlite3"
)

// TestEntSchemaParity verifies that the Ent schemas declared under
// ent/schema/ produce a database whose shape passes the same parity
// assertions the dbmate-migrated databases pass in TestSchemaParity. It
// drives migrate.Tables through Ent's schema.NewMigrate against an empty
// SQLite database and then runs the parity suite against the result.
//
// This is a one-shot check tied to task §4.11 of the
// migrate-to-ent-and-atlas change. Once the translated migrations
// (§7) and the runtime `ncps migrate up` (§9) land, the schema-parity
// guarantee is enforced via the schema-equivalence golden test (§8) and
// this test becomes redundant.
func TestEntSchemaParity(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "ent-schema-parity-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })

	dbFile := filepath.Join(dir, "ent.sqlite")
	dsn := "file:" + dbFile + "?_fk=1&_busy_timeout=10000&_journal_mode=WAL"

	sqlDB, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })

	// Apply the schema via Ent's runtime migrator. This is a temporary
	// fast-path; the production apply uses goose against translated
	// migration files (see §7/§9).
	drv := entsql.OpenDB(dialect.SQLite, sqlDB)
	m, err := schema.NewMigrate(drv)
	require.NoError(t, err)

	database.SchemaCreateMu.Lock()

	createErr := m.Create(context.Background(), migrate.Tables...)

	database.SchemaCreateMu.Unlock()

	require.NoError(t, createErr)

	// Wrap the freshly-built database in a Querier for the parity helpers.
	q, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)
	t.Cleanup(func() { q.DB().Close() })

	runSchemaParity(t, q, database.TypeSQLite)
}
