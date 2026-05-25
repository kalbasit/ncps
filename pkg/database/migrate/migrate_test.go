package migrate_test

import (
	"context"
	"database/sql"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/kalbasit/ncps/migrations"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/database/migrate"
	"github.com/kalbasit/ncps/testhelper"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"
)

// TestMigrateUp covers Option E's end-to-end adoption matrix from
// design D6: fresh install, v0.4-era partial dbmate history, full
// dbmate state, and re-running migrate up on an adopted database.
// MySQL adds crash-recovery scenarios.
func TestMigrateUp(t *testing.T) {
	t.Parallel()

	cases := []dialectFixture{
		{
			name: "SQLite", dialect: database.TypeSQLite, gooseDia: "sqlite",
			openAdmin: openSQLiteAdmin, openTest: openSQLiteTest,
		},
		{
			name: "PostgreSQL", envVar: "NCPS_TEST_ADMIN_POSTGRES_URL",
			dialect: database.TypePostgreSQL, gooseDia: "postgres",
			openAdmin: openPostgresAdmin, openTest: openPostgresTest,
		},
		{
			name: "MySQL", envVar: "NCPS_TEST_ADMIN_MYSQL_URL",
			dialect: database.TypeMySQL, gooseDia: "mysql",
			openAdmin: openMySQLAdmin, openTest: openMySQLTest,
		},
	}

	for _, dx := range cases {
		t.Run(dx.name, func(t *testing.T) {
			t.Parallel()
			dx.skipIfNoEnv(t)

			t.Run("fresh_install", func(t *testing.T) {
				t.Parallel()
				testFreshInstall(t, dx)
			})

			t.Run("dbmate_full_history_upgrade", func(t *testing.T) {
				t.Parallel()
				testDbmateFullHistoryUpgrade(t, dx)
			})

			t.Run("dbmate_partial_v04_era_upgrade", func(t *testing.T) {
				t.Parallel()

				if dx.dialect != database.TypeSQLite {
					t.Skip("partial v0.4-era history only applies to SQLite")
				}

				testPartialV04Upgrade(t, dx)
			})

			t.Run("re_run_is_no_op", func(t *testing.T) {
				t.Parallel()
				testRerunNoOp(t, dx)
			})
		})
	}
}

// TestMigrateUpMySQLCrashRecovery: crash-recovery scenarios specific
// to the MySQL state-machine adoption.
func TestMigrateUpMySQLCrashRecovery(t *testing.T) {
	t.Parallel()

	if os.Getenv("NCPS_TEST_ADMIN_MYSQL_URL") == "" {
		t.Skip("NCPS_TEST_ADMIN_MYSQL_URL not set")
	}

	dx := dialectFixture{
		name: "MySQL", envVar: "NCPS_TEST_ADMIN_MYSQL_URL",
		dialect: database.TypeMySQL, gooseDia: "mysql",
		openAdmin: openMySQLAdmin, openTest: openMySQLTest,
	}

	t.Run("S4_resume_from_rename", func(t *testing.T) {
		t.Parallel()
		testMySQLS4(t, dx)
	})

	t.Run("S5_resume_from_partial_insert", func(t *testing.T) {
		t.Parallel()
		testMySQLS5(t, dx)
	})

	t.Run("S6_impossible_aborts", func(t *testing.T) {
		t.Parallel()
		testMySQLS6(t, dx)
	})
}

// TestMigrateDown asserts the down command is a non-zero-exit pointer
// to the expand-contract recipe per design D10.
func TestMigrateDown(t *testing.T) {
	t.Parallel()

	err := migrate.Down(t.Context(), migrate.Options{
		DB:           &sql.DB{}, // unused — Down fails before touching DB
		Dialect:      database.TypeSQLite,
		MigrationsFS: emptyFS{},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, migrate.ErrDownNotSupported)
}

// ---------- scenario helpers ----------

func testFreshInstall(t *testing.T, dx dialectFixture) {
	t.Helper()
	db, cleanup := dx.openTest(t)
	t.Cleanup(cleanup)

	sub := mustSubFS(t, dx.gooseDia)

	err := migrate.Up(t.Context(), migrate.Options{
		DB: db, Dialect: dx.dialect, MigrationsFS: sub,
	})
	require.NoError(t, err)

	assertSchemaPresent(t, db, dx.dialect)
	assertVersionsAllApplied(t, db, dx.dialect, sub)

	// Re-running must be a no-op.
	require.NoError(t, migrate.Up(t.Context(), migrate.Options{
		DB: db, Dialect: dx.dialect, MigrationsFS: sub,
	}))
}

func testDbmateFullHistoryUpgrade(t *testing.T, dx dialectFixture) {
	t.Helper()
	db, cleanup := dx.openTest(t)
	t.Cleanup(cleanup)

	// Seed the DB to the dbmate-era end state by running every
	// translated migration via raw SQL (i.e. without goose tracking),
	// then create a dbmate-shape schema_migrations and INSERT all
	// versions into it.
	seedDbmateFullState(t, db, dx)

	sub := mustSubFS(t, dx.gooseDia)
	require.NoError(t, migrate.Up(t.Context(), migrate.Options{
		DB: db, Dialect: dx.dialect, MigrationsFS: sub,
	}))

	assertSchemaPresent(t, db, dx.dialect)
	assertVersionsAllApplied(t, db, dx.dialect, sub)
}

func testPartialV04Upgrade(t *testing.T, dx dialectFixture) {
	t.Helper()
	db, cleanup := dx.openTest(t)
	t.Cleanup(cleanup)

	// v0.4-era SQLite installs applied only the first few migrations
	// before the unified 20260101000000_init_schema.sql. Replay just
	// those.
	sub := mustSubFS(t, dx.gooseDia)
	files := listMigrationFiles(t, sub)
	require.GreaterOrEqual(t, len(files), 4, "need at least 4 sqlite migrations to simulate v0.4")
	partial := files[:4] // 4 oldest sqlite-only migrations

	applyMigrationsRaw(t, db, dx.dialect, sub, partial)
	seedDbmateSchemaMigrations(t, db, dx.dialect, fileVersions(partial))

	require.NoError(t, migrate.Up(t.Context(), migrate.Options{
		DB: db, Dialect: dx.dialect, MigrationsFS: sub,
	}))

	assertSchemaPresent(t, db, dx.dialect)
	assertVersionsAllApplied(t, db, dx.dialect, sub)
}

func testRerunNoOp(t *testing.T, dx dialectFixture) {
	t.Helper()
	db, cleanup := dx.openTest(t)
	t.Cleanup(cleanup)

	sub := mustSubFS(t, dx.gooseDia)
	require.NoError(t, migrate.Up(t.Context(), migrate.Options{
		DB: db, Dialect: dx.dialect, MigrationsFS: sub,
	}))

	// Capture state, re-run, verify state is byte-equivalent.
	before := snapshotVersions(t, db, dx.dialect)
	require.NoError(t, migrate.Up(t.Context(), migrate.Options{
		DB: db, Dialect: dx.dialect, MigrationsFS: sub,
	}))
	after := snapshotVersions(t, db, dx.dialect)
	assert.Equal(t, before, after, "rerun changed schema_migrations contents")
}

func testMySQLS4(t *testing.T, dx dialectFixture) {
	t.Helper()
	db, cleanup := dx.openTest(t)
	t.Cleanup(cleanup)

	sub := mustSubFS(t, dx.gooseDia)
	seedDbmateFullState(t, db, dx)

	// Simulate crash after RENAME: backup exists, schema_migrations doesn't.
	mustExec(t, db, "ALTER TABLE `schema_migrations` RENAME TO `schema_migrations_dbmate_backup`")

	// migrate.Up should resume from S4: create the new table, copy rows, drop backup.
	require.NoError(t, migrate.Up(t.Context(), migrate.Options{
		DB: db, Dialect: dx.dialect, MigrationsFS: sub,
	}))
	assertVersionsAllApplied(t, db, dx.dialect, sub)
}

func testMySQLS5(t *testing.T, dx dialectFixture) {
	t.Helper()
	db, cleanup := dx.openTest(t)
	t.Cleanup(cleanup)

	sub := mustSubFS(t, dx.gooseDia)
	seedDbmateFullState(t, db, dx)

	// Simulate crash after CREATE + partial INSERT.
	mustExec(t, db, "ALTER TABLE `schema_migrations` RENAME TO `schema_migrations_dbmate_backup`")
	mustExec(t, db, "CREATE TABLE `schema_migrations` ("+
		"`id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,"+
		"`version_id` BIGINT NOT NULL,"+
		"`is_applied` BOOLEAN NOT NULL,"+
		"`tstamp` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,"+
		"PRIMARY KEY (`id`)"+
		") ENGINE=InnoDB")
	// Insert only ONE row (simulating crash after first batch).
	mustExec(t, db,
		"INSERT INTO `schema_migrations` (`version_id`, `is_applied`, `tstamp`) "+
			"VALUES (20241210054814, 1, NOW())")

	require.NoError(t, migrate.Up(t.Context(), migrate.Options{
		DB: db, Dialect: dx.dialect, MigrationsFS: sub,
	}))
	assertVersionsAllApplied(t, db, dx.dialect, sub)
}

func testMySQLS6(t *testing.T, dx dialectFixture) {
	t.Helper()
	db, cleanup := dx.openTest(t)
	t.Cleanup(cleanup)

	// Construct the impossible S6: both schema_migrations (dbmate shape)
	// AND the backup table exist.
	seedDbmateFullState(t, db, dx)
	mustExec(t, db, "CREATE TABLE `schema_migrations_dbmate_backup` (version VARCHAR(128) PRIMARY KEY)")

	err := migrate.Up(t.Context(), migrate.Options{
		DB: db, Dialect: dx.dialect, MigrationsFS: mustSubFS(t, dx.gooseDia),
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, migrate.ErrImpossibleState)
}

// ---------- fixture infrastructure ----------

type dialectFixture struct {
	name      string
	envVar    string
	dialect   database.Type
	gooseDia  string // "sqlite" | "postgres" | "mysql"
	openAdmin func(t *testing.T) *sql.DB
	openTest  func(t *testing.T) (*sql.DB, func())
}

func (dx dialectFixture) skipIfNoEnv(t *testing.T) {
	if dx.envVar != "" && os.Getenv(dx.envVar) == "" {
		t.Skipf("Skipping %s: %s not set", dx.name, dx.envVar)
	}
}

func openSQLiteAdmin(_ *testing.T) *sql.DB { return nil }

func openSQLiteTest(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "db.sqlite")
	db, err := sql.Open("sqlite3", "file:"+dbFile+"?_fk=1&_journal_mode=WAL")
	require.NoError(t, err)

	return db, func() { db.Close() }
}

func openPostgresAdmin(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("pgx", os.Getenv("NCPS_TEST_ADMIN_POSTGRES_URL"))
	require.NoError(t, err)

	return db
}

func openPostgresTest(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	admin := openPostgresAdmin(t)
	name := "test-" + testhelper.MustRandString(58)
	_, err := admin.ExecContext(t.Context(), "SELECT create_test_db($1);", name)
	require.NoError(t, err)

	u, err := url.Parse(os.Getenv("NCPS_TEST_ADMIN_POSTGRES_URL"))
	require.NoError(t, err)

	u.Path = "/" + name

	db, err := sql.Open("pgx", u.String())
	require.NoError(t, err)

	cleanup := func() {
		db.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _ = admin.ExecContext(ctx, "SELECT drop_test_db($1);", name)
		admin.Close()
	}

	return db, cleanup
}

func openMySQLAdmin(t *testing.T) *sql.DB {
	t.Helper()

	dsn, err := mysqlURLToDSN(os.Getenv("NCPS_TEST_ADMIN_MYSQL_URL"), "")
	require.NoError(t, err)
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)

	return db
}

func openMySQLTest(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	admin := openMySQLAdmin(t)
	name := "test_" + testhelper.MustRandString(20)
	_, err := admin.ExecContext(t.Context(),
		"CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
	require.NoError(t, err)
	dsn, err := mysqlURLToDSN(os.Getenv("NCPS_TEST_ADMIN_MYSQL_URL"), name)
	require.NoError(t, err)
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)

	cleanup := func() {
		db.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _ = admin.ExecContext(ctx, "DROP DATABASE IF EXISTS `"+name+"`")
		admin.Close()
	}

	return db, cleanup
}

// mysqlURLToDSN converts a URL-style mysql DSN to go-sql-driver's
// native DSN form via mysqldriver.NewConfig so passwords containing
// `:` or `@` don't break naive concatenation.
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

	if _, ok := cfg.Params["multiStatements"]; !ok {
		cfg.MultiStatements = true
	}

	return cfg.FormatDSN(), nil
}

// ---------- migration helpers ----------

func mustSubFS(t *testing.T, dialect string) fs.FS {
	t.Helper()

	sub, err := fs.Sub(migrations.FS, dialect)
	require.NoError(t, err)

	return sub
}

func listMigrationFiles(t *testing.T, sub fs.FS) []string {
	t.Helper()

	var files []string

	err := fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && strings.HasSuffix(p, ".sql") {
			files = append(files, p)
		}

		return nil
	})
	require.NoError(t, err)
	sort.Strings(files)

	return files
}

func fileVersions(files []string) []int64 {
	out := make([]int64, 0, len(files))

	for _, f := range files {
		base := f
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}

		var v int64

		for i := 0; i < 14 && i < len(base); i++ {
			v = v*10 + int64(base[i]-'0')
		}

		out = append(out, v)
	}

	return out
}

// applyMigrationsRaw applies each named file's `-- +goose Up` section
// to the database without any goose tracking — used to fabricate
// pre-adoption schema states.
func applyMigrationsRaw(t *testing.T, db *sql.DB, dialect database.Type, sub fs.FS, files []string) {
	t.Helper()

	for _, f := range files {
		body, err := fs.ReadFile(sub, f)
		require.NoError(t, err)

		upSQL := extractUpSection(string(body))

		stmts := splitStatements(upSQL, dialect)
		for _, s := range stmts {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}

			_, err := db.ExecContext(t.Context(), s)
			require.NoErrorf(t, err, "applying %s stmt: %s", f, s)
		}
	}
}

// seedDbmateFullState applies every translated migration via raw SQL
// (no goose), then creates the dbmate-shape schema_migrations and
// inserts every version stamp.
func seedDbmateFullState(t *testing.T, db *sql.DB, dx dialectFixture) {
	t.Helper()
	sub := mustSubFS(t, dx.gooseDia)

	all := listMigrationFiles(t, sub)
	// Exclude the §8 bridge files when seeding the dbmate-era state —
	// the bridge migrations belong to the goose-shape future.
	pre := filterDbmateEra(all)
	applyMigrationsRaw(t, db, dx.dialect, sub, pre)
	seedDbmateSchemaMigrations(t, db, dx.dialect, fileVersions(pre))
}

// bridgeEraStart is the timestamp of the first §8 bridge migration
// (20260520000000). Any migration with a version >= this value is
// post-bridge and must be excluded from the dbmate-era seed.
const bridgeEraStart int64 = 20260520000000

func filterDbmateEra(files []string) []string {
	out := make([]string, 0, len(files))

	for _, f := range files {
		base := f
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}

		var v int64

		for i := 0; i < 14 && i < len(base); i++ {
			v = v*10 + int64(base[i]-'0')
		}

		if v >= bridgeEraStart {
			continue
		}

		out = append(out, f)
	}

	return out
}

// seedDbmateSchemaMigrations creates the dbmate-shape schema_migrations
// table and inserts each provided version stamp.
func seedDbmateSchemaMigrations(t *testing.T, db *sql.DB, dialect database.Type, versions []int64) {
	t.Helper()

	var createStmt string

	switch dialect {
	case database.TypeSQLite:
		createStmt = `CREATE TABLE "schema_migrations" (version varchar(128) PRIMARY KEY)`
	case database.TypePostgreSQL:
		createStmt = `CREATE TABLE schema_migrations (version varchar(128) PRIMARY KEY)`
	case database.TypeMySQL:
		createStmt = "CREATE TABLE `schema_migrations` (version VARCHAR(128) PRIMARY KEY)"
	case database.TypeUnknown:
		fallthrough
	default:
		t.Fatalf("seedDbmateSchemaMigrations: unsupported dialect %v", dialect)
	}

	mustExec(t, db, createStmt)

	for _, v := range versions {
		var stmt string

		switch dialect {
		case database.TypePostgreSQL:
			stmt = "INSERT INTO schema_migrations (version) VALUES ($1)"
		case database.TypeSQLite, database.TypeMySQL:
			stmt = "INSERT INTO schema_migrations (version) VALUES (?)"
		case database.TypeUnknown:
			fallthrough
		default:
			t.Fatalf("seedDbmateSchemaMigrations: insert: unsupported dialect %v", dialect)
		}

		_, err := db.ExecContext(t.Context(), stmt, intToStr(v))
		require.NoErrorf(t, err, "insert version %d", v)
	}
}

// snapshotVersions returns the sorted list of (version_id, is_applied)
// from schema_migrations for equality comparison across re-runs.
func snapshotVersions(t *testing.T, db *sql.DB, dialect database.Type) []string {
	t.Helper()

	var q string

	switch dialect {
	case database.TypeMySQL:
		q = "SELECT `version_id`, `is_applied` FROM `schema_migrations` ORDER BY `version_id`"
	case database.TypeSQLite, database.TypePostgreSQL:
		q = `SELECT "version_id", "is_applied" FROM "schema_migrations" ORDER BY "version_id"`
	case database.TypeUnknown:
		fallthrough
	default:
		t.Fatalf("snapshotVersions: unsupported dialect %v", dialect)
	}

	rows, err := db.QueryContext(t.Context(), q)
	require.NoError(t, err)

	defer rows.Close()

	var out []string

	for rows.Next() {
		var (
			vid     int64
			applied any
		)
		require.NoError(t, rows.Scan(&vid, &applied))
		out = append(out, intToStr(vid)+":"+toStr(applied))
	}

	require.NoError(t, rows.Err())

	return out
}

func toStr(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "1"
		}

		return "0"
	case int64:
		return intToStr(x)
	case []byte:
		return string(x)
	case nil:
		return ""
	default:
		return ""
	}
}

func assertSchemaPresent(t *testing.T, db *sql.DB, dialect database.Type) {
	t.Helper()

	for _, table := range []string{
		"narinfos", "nar_files", "chunks", "pinned_closures",
		"narinfo_references", "narinfo_signatures", "narinfo_nar_files", "nar_file_chunks",
		"config",
	} {
		var q string

		switch dialect {
		case database.TypeSQLite:
			q = `SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = '` + table + `'`
		case database.TypePostgreSQL:
			q = `SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='` + table + `'`
		case database.TypeMySQL:
			q = "SELECT 1 FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = '" + table + "'"
		case database.TypeUnknown:
			fallthrough
		default:
			t.Fatalf("assertSchemaPresent: unsupported dialect %v", dialect)
		}

		var ok int

		err := db.QueryRowContext(t.Context(), q).Scan(&ok)
		require.NoErrorf(t, err, "table %q missing or query failed", table)
	}
}

func assertVersionsAllApplied(t *testing.T, db *sql.DB, dialect database.Type, sub fs.FS) {
	t.Helper()

	files := listMigrationFiles(t, sub)
	want := make(map[int64]bool, len(files))

	for _, v := range fileVersions(files) {
		want[v] = true
	}

	var q string

	switch dialect {
	case database.TypeMySQL:
		q = "SELECT `version_id` FROM `schema_migrations` WHERE `is_applied` = 1"
	case database.TypeSQLite:
		q = `SELECT "version_id" FROM "schema_migrations" WHERE "is_applied" = 1`
	case database.TypePostgreSQL:
		q = `SELECT "version_id" FROM "schema_migrations" WHERE "is_applied" = true`
	case database.TypeUnknown:
		fallthrough
	default:
		t.Fatalf("assertVersionsAllApplied: unsupported dialect %v", dialect)
	}

	rows, err := db.QueryContext(t.Context(), q)
	require.NoError(t, err)

	defer rows.Close()

	got := map[int64]bool{}

	for rows.Next() {
		var v int64
		require.NoError(t, rows.Scan(&v))
		got[v] = true
	}

	require.NoError(t, rows.Err())

	for v := range want {
		assert.Truef(t, got[v], "expected version %d to be applied", v)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), query)
	require.NoErrorf(t, err, "exec %q", query)
}

// extractUpSection returns the body between `-- +goose Up` and
// `-- +goose Down` markers (case-sensitive, line-prefix match).
func extractUpSection(body string) string {
	upIdx := strings.Index(body, "-- +goose Up")
	if upIdx < 0 {
		return body
	}

	body = body[upIdx+len("-- +goose Up"):]

	downIdx := strings.Index(body, "-- +goose Down")
	if downIdx >= 0 {
		body = body[:downIdx]
	}

	return body
}

// splitStatements breaks a multi-statement script into individual SQL
// statements. SQLite/MySQL/Postgres all use ';' as the separator;
// we strip line comments and split on top-level ';'.
func splitStatements(script string, _ database.Type) []string {
	// Strip line comments to keep the splitter simple.
	var b strings.Builder

	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	parts := strings.Split(b.String(), ";")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}

	return out
}

func intToStr(v int64) string {
	if v == 0 {
		return "0"
	}

	neg := v < 0
	if neg {
		v = -v
	}

	var buf [20]byte

	pos := len(buf)
	for v > 0 {
		pos--
		buf[pos] = byte('0' + v%10)
		v /= 10
	}

	if neg {
		pos--
		buf[pos] = '-'
	}

	return string(buf[pos:])
}

// emptyFS is an io/fs.FS that has no files — used by TestMigrateDown
// which never actually reads the FS.
type emptyFS struct{}

func (emptyFS) Open(_ string) (fs.File, error) { return nil, fs.ErrNotExist }
