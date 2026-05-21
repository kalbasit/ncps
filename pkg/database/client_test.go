package database_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"entgo.io/ent/dialect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entsql "entgo.io/ent/dialect/sql"
	entschema "entgo.io/ent/dialect/sql/schema"
	entmigrate "github.com/kalbasit/ncps/ent/migrate"

	"github.com/kalbasit/ncps/ent"
	"github.com/kalbasit/ncps/pkg/database"

	_ "github.com/mattn/go-sqlite3"
)

// errCallerSentinel is a static error returned by test callbacks to
// verify rollback semantics.
var errCallerSentinel = errors.New("caller error")

func TestNewClient_NilDB(t *testing.T) {
	t.Parallel()

	c, err := database.NewClient(nil, database.TypeSQLite)
	require.Error(t, err)
	require.Nil(t, c)
}

func TestNewClient_UnknownDialect(t *testing.T) {
	t.Parallel()

	sdb, cleanup := freshSchemaSQLite(t)
	t.Cleanup(cleanup)

	c, err := database.NewClient(sdb, database.TypeUnknown)
	require.ErrorIs(t, err, database.ErrUnknownDialect)
	require.Nil(t, c)
}

func TestNewClient_Accessors(t *testing.T) {
	t.Parallel()

	sdb, cleanup := freshSchemaSQLite(t)
	t.Cleanup(cleanup)

	c, err := database.NewClient(sdb, database.TypeSQLite)
	require.NoError(t, err)
	require.NotNil(t, c)

	assert.NotNil(t, c.Ent())
	assert.Same(t, sdb, c.DB())
	assert.Equal(t, database.TypeSQLite, c.Type())
}

func TestWithTransaction_CommitsOnSuccess(t *testing.T) {
	t.Parallel()

	sdb, cleanup := freshSchemaSQLite(t)
	t.Cleanup(cleanup)

	c, err := database.NewClient(sdb, database.TypeSQLite)
	require.NoError(t, err)

	ctx := t.Context()

	err = c.WithTransaction(ctx, "insert-config", func(tx *ent.Tx) error {
		_, err := tx.ConfigEntry.Create().
			SetKey("commit-key").
			SetValue("commit-value").
			Save(ctx)

		return err
	})
	require.NoError(t, err)

	// Row visible outside the tx.
	got, err := c.Ent().ConfigEntry.Query().
		Where().
		All(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "commit-key", got[0].Key)
	assert.Equal(t, "commit-value", got[0].Value)
}

func TestWithTransaction_RollsBackOnError(t *testing.T) {
	t.Parallel()

	sdb, cleanup := freshSchemaSQLite(t)
	t.Cleanup(cleanup)

	c, err := database.NewClient(sdb, database.TypeSQLite)
	require.NoError(t, err)

	ctx := t.Context()

	err = c.WithTransaction(ctx, "insert-then-fail", func(tx *ent.Tx) error {
		if _, err := tx.ConfigEntry.Create().
			SetKey("rollback-key").
			SetValue("rollback-value").
			Save(ctx); err != nil {
			return err
		}

		return errCallerSentinel
	})
	require.ErrorIs(t, err, errCallerSentinel)

	// Row should NOT be visible — the tx rolled back.
	count, err := c.Ent().ConfigEntry.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestWithTransaction_RollsBackOnPanic(t *testing.T) {
	t.Parallel()

	sdb, cleanup := freshSchemaSQLite(t)
	t.Cleanup(cleanup)

	c, err := database.NewClient(sdb, database.TypeSQLite)
	require.NoError(t, err)

	ctx := t.Context()

	err = c.WithTransaction(ctx, "insert-then-panic", func(tx *ent.Tx) error {
		_, _ = tx.ConfigEntry.Create().
			SetKey("panic-key").
			SetValue("panic-value").
			Save(ctx)

		panic("boom")
	})
	require.Error(t, err)
	require.ErrorIs(t, err, database.ErrTransactionPanic)
	assert.Contains(t, err.Error(), "insert-then-panic")
	assert.Contains(t, err.Error(), "boom")

	count, err := c.Ent().ConfigEntry.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestWithTransaction_WrapsBeginError(t *testing.T) {
	t.Parallel()

	sdb, cleanup := freshSchemaSQLite(t)
	t.Cleanup(cleanup)

	c, err := database.NewClient(sdb, database.TypeSQLite)
	require.NoError(t, err)

	// Close the DB so Begin fails.
	require.NoError(t, sdb.Close())

	err = c.WithTransaction(t.Context(), "should-fail-begin", func(_ *ent.Tx) error {
		t.Fatalf("fn must not be invoked when Begin fails")

		return nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "begin transaction for should-fail-begin")
}

func TestClient_Close(t *testing.T) {
	t.Parallel()

	sdb, cleanup := freshSchemaSQLite(t)
	t.Cleanup(cleanup)

	c, err := database.NewClient(sdb, database.TypeSQLite)
	require.NoError(t, err)

	require.NoError(t, c.Close())

	// Underlying *sql.DB should now be closed.
	err = sdb.PingContext(t.Context())
	require.Error(t, err)
	// database/sql doesn't export a sentinel for the closed-DB error;
	// it's a wrapped errors.New("sql: database is closed"). Match by
	// message rather than ErrorIs.
	assert.Contains(t, err.Error(), "database is closed")
}

func TestEntDialectFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   database.Type
		want string
		err  bool
	}{
		{database.TypeSQLite, dialect.SQLite, false},
		{database.TypePostgreSQL, dialect.Postgres, false},
		{database.TypeMySQL, dialect.MySQL, false},
		{database.TypeUnknown, "", true},
	}

	for _, tc := range tests {
		got, err := database.EntDialectFor(tc.in)
		if tc.err {
			require.Error(t, err)
			assert.ErrorIs(t, err, database.ErrUnknownDialect)
		} else {
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		}
	}
}

// freshSchemaSQLite opens a brand-new SQLite database in a temp file
// and runs Ent's Schema.Create against it. Returns the *sql.DB and a
// cleanup func; the caller is responsible for invoking cleanup via
// t.Cleanup (which also covers the case where Close was already called
// by the test under inspection).
func freshSchemaSQLite(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	dbFile := filepath.Join(t.TempDir(), "db.sqlite")

	sdb, err := sql.Open("sqlite3", "file:"+dbFile+"?_fk=1&_journal_mode=WAL&_busy_timeout=10000")
	require.NoError(t, err)

	database.SchemaCreateMu.Lock()

	drv := entsql.OpenDB(dialect.SQLite, sdb)

	m, err := entschema.NewMigrate(drv, entschema.WithDialect(dialect.SQLite))
	if err != nil {
		database.SchemaCreateMu.Unlock()

		t.Fatalf("NewMigrate: %v", err)
	}

	if createErr := m.Create(t.Context(), entmigrate.Tables...); createErr != nil {
		database.SchemaCreateMu.Unlock()

		t.Fatalf("Schema.Create: %v", createErr)
	}

	database.SchemaCreateMu.Unlock()

	return sdb, func() { _ = sdb.Close() }
}
