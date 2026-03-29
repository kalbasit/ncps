package database

import (
	"embed"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
	"github.com/uptrace/bun/migrate"
)

//go:embed migrations/sqlite/*.sql
var sqliteMigrations embed.FS

//go:embed migrations/postgres/*.sql
var postgresMigrations embed.FS

//go:embed migrations/mysql/*.sql
var mysqlMigrations embed.FS

// SQLiteMigrations returns a migrator for SQLite migrations.
func SQLiteMigrations(db *bun.DB) *migrate.Migrator {
	return newMigrator(db, sqliteMigrations)
}

// PostgreSQLMigrations returns a migrator for PostgreSQL migrations.
func PostgreSQLMigrations(db *bun.DB) *migrate.Migrator {
	return newMigrator(db, postgresMigrations)
}

// MySQLMigrations returns a migrator for MySQL migrations.
func MySQLMigrations(db *bun.DB) *migrate.Migrator {
	return newMigrator(db, mysqlMigrations)
}

// Migrations returns the appropriate migrator based on the database dialect.
func Migrations(db *bun.DB) *migrate.Migrator {
	switch db.Dialect().Name() {
	case dialect.SQLite:
		return SQLiteMigrations(db)
	case dialect.PG:
		return PostgreSQLMigrations(db)
	case dialect.MySQL:
		return MySQLMigrations(db)
	case dialect.Invalid, dialect.MSSQL, dialect.Oracle:
		fallthrough
	default:
		return nil
	}
}

// newMigrator creates a new migrator from an embedded FS.
func newMigrator(db *bun.DB, fsys embed.FS) *migrate.Migrator {
	migrations := migrate.NewMigrations()

	if err := migrations.Discover(fsys); err != nil {
		return migrate.NewMigrator(db, migrations)
	}

	return migrate.NewMigrator(db, migrations)
}
