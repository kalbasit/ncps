// Command generate-migrations diffs the current Ent schemas under
// ent/schema/ against the latest applied state of migrations/<dialect>/
// and emits a new goose-formatted SQL migration per dialect — all under a
// single shared timestamp prefix.
//
// Atlas (ariga.io/atlas) is consumed as a Go library; no `atlas` CLI
// binary is required. For SQLite, the dev database is an in-memory
// sqlite3 instance. For PostgreSQL and MySQL/MariaDB, the dev URL is
// taken from the corresponding flag (or env var) and the database is
// destructively replayed against the embedded migration history before
// computing the diff — never point this at a production database.
//
// Two modes:
//
//	go run ./cmd/generate-migrations --name=<descriptive_name>
//	    schema-driven: diffs current Ent schemas against the latest
//	    applied state and writes one .sql per dialect under
//	    migrations/<dialect>/.
//
//	go run ./cmd/generate-migrations --sql-only --name=<descriptive_name>
//	    SQL-only: writes empty goose stubs (-- +goose Up / -- +goose Down)
//	    to each dialect directory. Used for data backfills and the
//	    step-3 "constraint lock-in" of the four-step NOT NULL recipe.
//
// `--name` is required in both modes and must be a descriptive
// snake_case identifier; placeholder names (auto, wip, tmp, empty)
// are rejected.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ariga.io/atlas/sql/sqltool"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql/schema"

	entsql "entgo.io/ent/dialect/sql"
	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/kalbasit/ncps/ent/migrate"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

const (
	defaultPostgresURL = "postgres://test-user:test-password@127.0.0.1:5432/test-db?sslmode=disable"
	defaultMySQLURL    = "test-user:test-password@tcp(127.0.0.1:3306)/test-db?parseTime=true&loc=UTC"
)

var errPlaceholderName = errors.New(
	"placeholder migration name is forbidden — provide a descriptive snake_case identifier")

func main() {
	var (
		name        = flag.String("name", "", "descriptive snake_case migration name (required)")
		sqlOnly     = flag.Bool("sql-only", false, "produce empty goose stubs instead of diffing against Ent schemas")
		root        = flag.String("root", ".", "repository root (contains migrations/)")
		postgresURL = flag.String("postgres-url", "",
			"dev PostgreSQL URL (default: $NCPS_GEN_POSTGRES_URL or localhost test-db)")
		mysqlURL = flag.String("mysql-url", "",
			"dev MySQL URL (default: $NCPS_GEN_MYSQL_URL or localhost test-db)")
	)

	flag.Parse()

	if err := validateName(*name); err != nil {
		log.Fatalf("generate-migrations: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pgURL := pickURL(*postgresURL, "NCPS_GEN_POSTGRES_URL", defaultPostgresURL)
	myURL := pickURL(*mysqlURL, "NCPS_GEN_MYSQL_URL", defaultMySQLURL)

	stamp := time.Now().UTC().Format("20060102150405")
	fname := fmt.Sprintf("%s_%s.sql", stamp, *name)

	for _, d := range []dialectSpec{
		{name: "sqlite", goDialect: dialect.SQLite, driver: "sqlite3", openDSN: "file::memory:?_fk=1&cache=shared"},
		{name: "postgres", goDialect: dialect.Postgres, driver: "pgx", openDSN: pgURL},
		{name: "mysql", goDialect: dialect.MySQL, driver: "mysql", openDSN: myURL},
	} {
		if err := runDialect(ctx, *root, d, fname, *name, *sqlOnly); err != nil {
			log.Fatalf("generate-migrations: %s: %v", d.name, err)
		}
	}
}

type dialectSpec struct {
	name      string
	goDialect string
	driver    string
	openDSN   string
}

func runDialect(ctx context.Context, root string, d dialectSpec, fname, migName string, sqlOnly bool) error {
	dir := filepath.Join(root, "migrations", d.name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	if sqlOnly {
		path := filepath.Join(dir, fname)
		stub := "-- +goose Up\n\n-- +goose Down\n"

		if err := os.WriteFile(path, []byte(stub), 0o600); err != nil {
			return fmt.Errorf("write stub: %w", err)
		}

		fmt.Println(path)

		return nil
	}

	db, err := openDevDB(d)
	if err != nil {
		return fmt.Errorf("open dev db: %w", err)
	}
	defer db.Close()

	if err := resetDevDB(ctx, db, d); err != nil {
		return fmt.Errorf("reset dev db: %w", err)
	}

	gdir, err := sqltool.NewGooseDir(dir)
	if err != nil {
		return fmt.Errorf("NewGooseDir(%s): %w", dir, err)
	}

	drv := entsql.OpenDB(d.goDialect, db)

	m, err := schema.NewMigrate(drv,
		schema.WithDir(gdir),
		schema.WithMigrationMode(schema.ModeReplay),
		schema.WithDialect(d.goDialect),
		schema.WithFormatter(sqltool.GooseFormatter),
	)
	if err != nil {
		return fmt.Errorf("NewMigrate: %w", err)
	}

	if err := m.NamedDiff(ctx, migName, migrate.Tables...); err != nil {
		return fmt.Errorf("NamedDiff: %w", err)
	}

	return nil
}

func openDevDB(d dialectSpec) (*sql.DB, error) {
	switch d.driver {
	case "mysql":
		// Force ANSI quotes off (we use backticks in MySQL DDL anyway) and
		// ensure the connection's DSN has parseTime + loc set.
		cfg, err := mysqldriver.ParseDSN(d.openDSN)
		if err != nil {
			return nil, fmt.Errorf("parse mysql dsn: %w", err)
		}

		cfg.ParseTime = true
		cfg.MultiStatements = true

		db, err := sql.Open("mysql", cfg.FormatDSN())
		if err != nil {
			return nil, err
		}

		return db, nil
	default:
		return sql.Open(d.driver, d.openDSN)
	}
}

// resetDevDB returns the dev database to an empty state by dropping the
// schema_migrations tracking table and every load-bearing ncps table. We
// drop *only* the ncps tables we know about (plus schema_migrations) so
// the operator can keep the test DB shared with other tooling.
//
// For SQLite, in-memory databases start empty so this is a no-op (the
// in-memory DSN is fresh on each open).
func resetDevDB(ctx context.Context, db *sql.DB, d dialectSpec) error {
	if d.name == "sqlite" {
		return nil
	}

	tables := []string{
		"nar_file_chunks", "nar_files", "narinfo_nar_files",
		"narinfo_references", "narinfo_signatures", "narinfos",
		"chunks", "config", "pinned_closures", "schema_migrations",
	}

	if d.name == "mysql" {
		if _, err := db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0"); err != nil {
			return fmt.Errorf("disable fk checks: %w", err)
		}

		for _, t := range tables {
			if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS `"+t+"`"); err != nil {
				return fmt.Errorf("drop %s: %w", t, err)
			}
		}

		if _, err := db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=1"); err != nil {
			return fmt.Errorf("enable fk checks: %w", err)
		}

		return nil
	}
	// Postgres: cascade-drop so FKs go with the tables.
	for _, t := range tables {
		if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS "`+t+`" CASCADE`); err != nil {
			return fmt.Errorf("drop %s: %w", t, err)
		}
	}

	return nil
}

// validateName rejects empty and well-known placeholder names. The lint
// is intentionally narrow: anything that isn't on the reject list passes,
// so a developer can supply any descriptive snake_case identifier.
func validateName(name string) error {
	s := strings.TrimSpace(name)
	if s == "" {
		return fmt.Errorf("%w: empty", errPlaceholderName)
	}

	if strings.ContainsAny(s, " \t") {
		return fmt.Errorf("%w: %q (contains whitespace)", errPlaceholderName, s)
	}

	switch strings.ToLower(s) {
	case "auto", "wip", "tmp", "todo", "temp", "test":
		return fmt.Errorf("%w: %q", errPlaceholderName, s)
	}

	return nil
}

func pickURL(flagVal, envKey, fallback string) string {
	if flagVal != "" {
		return flagVal
	}

	if v := os.Getenv(envKey); v != "" {
		return v
	}

	return fallback
}
