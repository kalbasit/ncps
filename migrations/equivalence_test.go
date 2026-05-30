package migrations_test

import (
	"context"
	"database/sql"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ariga.io/atlas/sql/sqltool"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/stretchr/testify/require"

	atlasmigrate "ariga.io/atlas/sql/migrate"
	atlasschema "ariga.io/atlas/sql/schema"
	entsql "entgo.io/ent/dialect/sql"
	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/kalbasit/ncps/ent/migrate"
	"github.com/kalbasit/ncps/migrations"
	"github.com/kalbasit/ncps/testhelper"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"
)

// TestSchemaEquivalence is the load-bearing guarantee that the on-disk
// migrations under migrations/<dialect>/ — translated dbmate-era files
// (§7) plus the ent_baseline bridge (§8) — produce a schema byte-equivalent
// to what Ent expects from ent/schema/ (§4). For each dialect:
//
//  1. Copy migrations/<dialect>/*.sql into a temp directory + bootstrap
//     atlas.sum so Atlas's replay validator accepts the dir.
//  2. Open a fresh empty database (in-memory SQLite, or an ephemeral
//     PostgreSQL / MySQL database via testhelper).
//  3. Run schema.NewMigrate with schema.WithMigrationMode(ModeReplay) +
//     sqltool.GooseFormatter. Atlas internally replays every migration
//     file into the dev database, then diffs the result against
//     migrate.Tables and writes a new .sql file IFF the diff is non-empty.
//  4. Assert no new file appeared.
//
// If any dialect produces a diff, the schemas have drifted and either
// the translation, the bridge, or the Ent schema must be fixed until the
// diff is empty.
// atlasReplayMu serializes the Ent/Atlas schema-replay section across the
// dialect subtests. schema.NewMigrate/NamedDiff -> planReplay -> realm ->
// tables -> aIndexes reads process-global state in entgo.io/ent's Atlas
// integration that is not safe for concurrent use; running the dialect
// subtests in parallel (each performing a replay-diff) trips a data race in
// entgo.io/ent/dialect/sql/schema/atlas.go under the race detector. Holding
// this lock only around the replay keeps t.Parallel() for setup/teardown.
//
//nolint:gochecknoglobals // serializes shared Ent/Atlas replay globals across parallel subtests
var atlasReplayMu sync.Mutex

func TestSchemaEquivalence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		dialect     string
		goDialect   string
		envVar      string
		openFreshDB func(t *testing.T) (db *sql.DB, cleanup func())
		diffHooks   []schema.DiffHook
	}{
		{
			dialect:     "sqlite",
			goDialect:   dialect.SQLite,
			openFreshDB: openFreshSQLite,
			// SQLite-only: Ent's atDefault wraps an Expr default in
			// outer parens ("CURRENT_TIMESTAMP" -> "(CURRENT_TIMESTAMP)"),
			// but SQLite's introspector returns the raw stored form
			// without parens. Atlas's SQLite defaultChanged compares
			// the two strings directly (no MayWrap normalization), so
			// every column with a DefaultExpr produces a phantom
			// ModifyTable diff on this dialect. PG and MySQL aren't
			// affected (their introspectors preserve the form Atlas
			// emits). Normalize the desired-side defaults here so the
			// schema-equivalence assertion sees apples-to-apples values.
			diffHooks: []schema.DiffHook{stripParenWrappedExprDefaults},
		},
		{
			dialect:     "postgres",
			goDialect:   dialect.Postgres,
			envVar:      "NCPS_TEST_ADMIN_POSTGRES_URL",
			openFreshDB: openFreshPostgres,
		},
		{
			dialect:     "mysql",
			goDialect:   dialect.MySQL,
			envVar:      "NCPS_TEST_ADMIN_MYSQL_URL",
			openFreshDB: openFreshMySQL,
		},
	}

	for _, tc := range cases {
		t.Run(tc.dialect, func(t *testing.T) {
			t.Parallel()

			if tc.envVar != "" && os.Getenv(tc.envVar) == "" {
				t.Skipf("Skipping %s: %s not set", tc.dialect, tc.envVar)
			}

			db, cleanup := tc.openFreshDB(t)
			t.Cleanup(cleanup)

			tmpDir := copyMigrations(t, tc.dialect)
			gdir, err := sqltool.NewGooseDir(tmpDir)
			require.NoError(t, err)
			sum, err := gdir.Checksum()
			require.NoError(t, err)
			require.NoError(t, atlasmigrate.WriteSumFile(gdir, sum))

			before := mustListSQLFiles(t, tmpDir)

			drv := entsql.OpenDB(tc.goDialect, db)

			opts := []schema.MigrateOption{
				schema.WithDir(gdir),
				schema.WithMigrationMode(schema.ModeReplay),
				schema.WithDialect(tc.goDialect),
				schema.WithFormatter(sqltool.GooseFormatter),
			}
			if len(tc.diffHooks) > 0 {
				opts = append(opts, schema.WithDiffHook(tc.diffHooks...))
			}

			// Serialize the replay-diff: Ent/Atlas shares process-global state
			// here that races across the parallel dialect subtests. Capture
			// errors under the lock and assert after unlocking so a failed
			// require never leaves the mutex held.
			atlasReplayMu.Lock()
			m, mErr := schema.NewMigrate(drv, opts...)

			var diffErr error
			if mErr == nil {
				diffErr = m.NamedDiff(t.Context(), "equivalence_check", migrate.Tables...)
			}
			atlasReplayMu.Unlock()

			require.NoError(t, mErr)
			require.NoError(t, diffErr)

			after := mustListSQLFiles(t, tmpDir)

			diffFiles := setDiff(after, before)
			if len(diffFiles) > 0 {
				for _, f := range diffFiles {
					body, _ := os.ReadFile(filepath.Join(tmpDir, f))
					t.Logf("=== diff file %s ===\n%s", f, string(body))
				}

				t.Fatalf("schema drift detected for %s: Atlas produced new migration file(s): %v",
					tc.dialect, diffFiles)
			}
		})
	}
}

func openFreshSQLite(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	db, err := sql.Open("sqlite3", "file:"+dbFile+"?_fk=1&_journal_mode=WAL")
	require.NoError(t, err)

	return db, func() { db.Close() }
}

func openFreshPostgres(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	adminURL := os.Getenv("NCPS_TEST_ADMIN_POSTGRES_URL")
	require.NotEmpty(t, adminURL)

	adminDB, err := sql.Open("pgx", adminURL)
	require.NoError(t, err)

	dbName := "test-" + testhelper.MustRandString(58)
	_, err = adminDB.ExecContext(t.Context(), "SELECT create_test_db($1);", dbName)
	require.NoError(t, err)

	u, err := url.Parse(adminURL)
	require.NoError(t, err)

	u.Path = "/" + dbName

	db, err := sql.Open("pgx", u.String())
	require.NoError(t, err)

	cleanup := func() {
		db.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5e9)
		defer cancel()

		_, _ = adminDB.ExecContext(ctx, "SELECT drop_test_db($1);", dbName)
		adminDB.Close()
	}

	return db, cleanup
}

func openFreshMySQL(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	adminURL := os.Getenv("NCPS_TEST_ADMIN_MYSQL_URL")
	require.NotEmpty(t, adminURL)

	adminDSN, err := mysqlURLToDSN(adminURL, "")
	require.NoError(t, err)
	adminDB, err := sql.Open("mysql", adminDSN)
	require.NoError(t, err)

	dbName := "test_" + testhelper.MustRandString(20)
	_, err = adminDB.ExecContext(t.Context(),
		"CREATE DATABASE `"+dbName+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
	require.NoError(t, err)

	freshDSN, err := mysqlURLToDSN(adminURL, dbName)
	require.NoError(t, err)
	db, err := sql.Open("mysql", freshDSN)
	require.NoError(t, err)

	cleanup := func() {
		db.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5e9)
		defer cancel()

		_, _ = adminDB.ExecContext(ctx, "DROP DATABASE IF EXISTS `"+dbName+"`")
		adminDB.Close()
	}

	return db, cleanup
}

// mysqlURLToDSN converts a URL-style mysql DSN
// ("mysql://user:pass@host:port/db?params") to go-sql-driver's native
// DSN form. Uses mysqldriver.NewConfig to build the DSN robustly —
// passwords containing `:` or `@` would break naive concatenation. If
// newDBName is non-empty, it replaces the path component (used for
// ephemeral DBs).
func mysqlURLToDSN(rawURL, newDBName string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	cfg := mysqldriver.NewConfig()
	cfg.User = u.User.Username()
	cfg.Passwd, _ = u.User.Password()
	cfg.Net = "tcp"
	cfg.Addr = u.Host

	cfg.DBName = newDBName
	if cfg.DBName == "" {
		cfg.DBName = strings.TrimPrefix(u.Path, "/")
	}

	cfg.Params = map[string]string{}

	for k, v := range u.Query() {
		if len(v) > 0 {
			cfg.Params[k] = v[0]
		}
	}

	if _, ok := cfg.Params["parseTime"]; !ok {
		cfg.ParseTime = true
	}

	if _, ok := cfg.Params["loc"]; !ok {
		cfg.Loc = time.UTC
	}

	return cfg.FormatDSN(), nil
}

func copyMigrations(t *testing.T, dialect string) string {
	t.Helper()
	dst := t.TempDir()

	sub, err := fs.Sub(migrations.FS, dialect)
	require.NoError(t, err)

	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		in, err := sub.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()

		// Ensure the parent directory exists before creating the file.
		// Today migrations/<dialect>/ is flat, but a future refactor
		// could nest files; MkdirAll keeps this helper robust.
		outPath := filepath.Join(dst, p)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}

		out, err := os.Create(outPath)
		if err != nil {
			return err
		}
		defer out.Close()

		_, err = io.Copy(out, in)

		return err
	})
	require.NoError(t, err)

	return dst
}

func mustListSQLFiles(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	var out []string

	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			out = append(out, e.Name())
		}
	}

	return out
}

func setDiff(after, before []string) []string {
	inBefore := make(map[string]struct{}, len(before))
	for _, b := range before {
		inBefore[b] = struct{}{}
	}

	var out []string

	for _, a := range after {
		if _, ok := inBefore[a]; !ok {
			out = append(out, a)
		}
	}

	return out
}

// stripParenWrappedExprDefaults is a SQLite-only DiffHook that strips a
// single layer of outer parentheses from RawExpr column defaults on the
// desired side of the diff. Ents schema/atlas.go atDefault always wraps
// an entsql.Annotation{DefaultExpr: ...} value in parens before handing
// it to Atlas (e.g. "CURRENT_TIMESTAMP" -> "(CURRENT_TIMESTAMP)"). SQLites
// schema introspector returns the unwrapped form. Atlas's SQLite
// defaultChanged compares the two RawExpr strings byte-for-byte without
// the MayWrap normalization it uses elsewhere, so every DefaultExpr
// column trips a phantom diff. Postgres/MySQL preserve the wrapped form
// during introspection and do not need this hook.
func stripParenWrappedExprDefaults(next schema.Differ) schema.Differ {
	return schema.DiffFunc(func(current, desired *atlasschema.Schema) ([]atlasschema.Change, error) {
		for _, tbl := range desired.Tables {
			for _, col := range tbl.Columns {
				re, ok := col.Default.(*atlasschema.RawExpr)
				if !ok || re == nil {
					continue
				}

				x := re.X
				if len(x) >= 2 && x[0] == '(' && x[len(x)-1] == ')' {
					re.X = x[1 : len(x)-1]
				}
			}
		}

		return next.Diff(current, desired)
	})
}
