// Package migrate implements the ncps schema-migration runtime: state
// detection, dbmate→goose adoption, fresh-install via Ent's Schema.Create,
// and the goose hand-off for incremental migrations. Per design D6
// (Option E) of the migrate-to-ent-and-atlas change.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	entmigrate "github.com/kalbasit/ncps/ent/migrate"

	"github.com/kalbasit/ncps/pkg/database"
)

// Sentinel errors for state detection.
var (
	// ErrUnknownDialect is returned when state detection or any other
	// migrate operation encounters an unrecognised database.Type value.
	ErrUnknownDialect = errors.New("migrate: unknown dialect")

	// ErrCorruptState is returned when probe detects ncps application
	// tables but no schema_migrations tracking table. This should never
	// happen during normal operation and requires manual intervention.
	ErrCorruptState = errors.New(
		"migrate: ncps application tables exist but schema_migrations does not — refusing to adopt")
)

// State describes what `ncps migrate up` finds when it probes the
// database. Each state has a dedicated handler in this package.
type State int

const (
	// StateUnknown is the zero value and should never be observed in
	// practice. Treated as an error case.
	StateUnknown State = iota

	// StateEmpty: the database has no `schema_migrations` table and no
	// ncps application tables. Fresh install — `Schema.Create` produces
	// the full schema in one shot.
	StateEmpty

	// StateDbmate: `schema_migrations` exists with dbmate's column
	// layout (`version VARCHAR(...)` PRIMARY KEY, no `is_applied`
	// column). One of the prior ncps versions migrated this database
	// with dbmate; we now adopt the tracking table to goose's shape and
	// hand off to goose to apply the bridge.
	StateDbmate

	// StateAdopted: `schema_migrations` has goose's column layout
	// (`id, version_id, is_applied, tstamp`). The normal incremental
	// path — hand straight to goose.
	StateAdopted

	// StateMySQLS4 (MySQL-only mid-adoption recovery): the rename step
	// of MySQL adoption completed but the new schema_migrations was not
	// yet created. `schema_migrations_dbmate_backup` exists,
	// `schema_migrations` does not. Recovery: re-run from the CREATE
	// step.
	StateMySQLS4

	// StateMySQLS5 (MySQL-only mid-adoption recovery): the new
	// schema_migrations was created (and possibly populated) but the
	// backup was not yet dropped. Both `schema_migrations` (goose
	// shape) and `schema_migrations_dbmate_backup` exist. Recovery:
	// verify row-count parity, drop backup (or re-INSERT then drop on
	// mismatch).
	StateMySQLS5

	// StateImpossibleS6 (MySQL-only diagnostic): both tables exist but
	// `schema_migrations` still has dbmate shape. Per design D6 this
	// state is unreachable through any happy-path; we abort with an
	// operator-readable diagnostic.
	StateImpossibleS6
)

// String returns a human-readable name (for logging / dry-run output).
func (s State) String() string {
	switch s {
	case StateEmpty:
		return "empty"
	case StateDbmate:
		return "dbmate"
	case StateAdopted:
		return "adopted"
	case StateMySQLS4:
		return "mysql-S4-recover-from-create"
	case StateMySQLS5:
		return "mysql-S5-recover-finalise"
	case StateImpossibleS6:
		return "impossible-S6"
	case StateUnknown:
		fallthrough
	default:
		return "unknown"
	}
}

// Detect probes the database and returns the adoption state. It uses the
// dialect-appropriate catalog query for table-and-column existence; no
// DDL is issued.
func Detect(ctx context.Context, db *sql.DB, d database.Type) (State, error) {
	switch d {
	case database.TypeSQLite, database.TypePostgreSQL:
		return detectStandard(ctx, db, d)
	case database.TypeMySQL:
		return detectMySQL(ctx, db)
	case database.TypeUnknown:
		fallthrough
	default:
		return StateUnknown, fmt.Errorf("%w %v", ErrUnknownDialect, d)
	}
}

func detectStandard(ctx context.Context, db *sql.DB, d database.Type) (State, error) {
	migExists, err := tableExists(ctx, db, d, "schema_migrations")
	if err != nil {
		return StateUnknown, fmt.Errorf("probe schema_migrations: %w", err)
	}

	if !migExists {
		// No tracking table; check whether any ncps tables exist to
		// distinguish fresh vs. corrupt state.
		anyTable, err := anyAppTableExists(ctx, db, d)
		if err != nil {
			return StateUnknown, fmt.Errorf("probe app tables: %w", err)
		}

		if anyTable {
			return StateUnknown, ErrCorruptState
		}

		return StateEmpty, nil
	}

	isAppliedExists, err := columnExists(ctx, db, d, "schema_migrations", "is_applied")
	if err != nil {
		return StateUnknown, fmt.Errorf("probe is_applied column: %w", err)
	}

	if isAppliedExists {
		return StateAdopted, nil
	}

	return StateDbmate, nil
}

func detectMySQL(ctx context.Context, db *sql.DB) (State, error) {
	migExists, err := tableExists(ctx, db, database.TypeMySQL, "schema_migrations")
	if err != nil {
		return StateUnknown, err
	}

	backupExists, err := tableExists(ctx, db, database.TypeMySQL, "schema_migrations_dbmate_backup")
	if err != nil {
		return StateUnknown, err
	}

	if migExists {
		isAppliedExists, err := columnExists(ctx, db, database.TypeMySQL, "schema_migrations", "is_applied")
		if err != nil {
			return StateUnknown, err
		}

		switch {
		case backupExists && isAppliedExists:
			return StateMySQLS5, nil
		case backupExists && !isAppliedExists:
			return StateImpossibleS6, nil
		case !backupExists && isAppliedExists:
			return StateAdopted, nil
		default: // !backupExists && !isAppliedExists
			return StateDbmate, nil
		}
	}

	if backupExists {
		return StateMySQLS4, nil
	}
	// Neither table exists.
	anyTable, err := anyAppTableExists(ctx, db, database.TypeMySQL)
	if err != nil {
		return StateUnknown, err
	}

	if anyTable {
		return StateUnknown, ErrCorruptState
	}

	return StateEmpty, nil
}

// tableExists returns true iff `name` is present in the current database.
func tableExists(ctx context.Context, db *sql.DB, d database.Type, name string) (bool, error) {
	var (
		query string
		args  []any
	)

	switch d {
	case database.TypeSQLite:
		query = `SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?`
		args = []any{name}
	case database.TypePostgreSQL:
		query = `SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1`
		args = []any{name}
	case database.TypeMySQL:
		query = `SELECT 1 FROM information_schema.tables
			WHERE table_schema = DATABASE() AND table_name = ?`
		args = []any{name}
	case database.TypeUnknown:
		fallthrough
	default:
		return false, fmt.Errorf("tableExists: %w %v", ErrUnknownDialect, d)
	}

	var ok int

	err := db.QueryRowContext(ctx, query, args...).Scan(&ok)

	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return ok == 1, nil
}

// columnExists returns true iff `column` exists on `table`.
func columnExists(ctx context.Context, db *sql.DB, d database.Type, table, column string) (bool, error) {
	var (
		query string
		args  []any
	)

	switch d {
	case database.TypeSQLite:
		// PRAGMA table_info(<table>) returns one row per column.
		// We scan the result set and look for the requested name.
		rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%q)`, table))
		if err != nil {
			return false, err
		}
		defer rows.Close()

		for rows.Next() {
			var (
				cid     int
				name    string
				ctype   string
				notnull int
				dflt    sql.NullString
				pk      int
			)
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				return false, err
			}

			if name == column {
				return true, rows.Close()
			}
		}

		return false, rows.Err()
	case database.TypePostgreSQL:
		query = `SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`
		args = []any{table, column}
	case database.TypeMySQL:
		query = `SELECT 1 FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`
		args = []any{table, column}
	case database.TypeUnknown:
		fallthrough
	default:
		return false, fmt.Errorf("columnExists: %w %v", ErrUnknownDialect, d)
	}

	var ok int

	err := db.QueryRowContext(ctx, query, args...).Scan(&ok)

	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return ok == 1, nil
}

// anyAppTableExists returns true iff any of the canonical ncps tables
// is present. Used to distinguish "fresh empty DB" from "DB with app
// tables but no tracking" (the latter is an error — never adopt).
func anyAppTableExists(ctx context.Context, db *sql.DB, d database.Type) (bool, error) {
	for _, name := range appTables {
		ok, err := tableExists(ctx, db, d, name)
		if err != nil {
			return false, err
		}

		if ok {
			return true, nil
		}
	}

	return false, nil
}

// appTables is the canonical set of ncps tables Ent's migrate.Tables
// declares. Kept in sync via the §3 schema-parity tests and §8
// schema-equivalence test.
//
// appTables is the canonical set of ncps tables Ent declares in
// migrate.Tables. Derived dynamically at init() so adding a new entity
// to ent/schema/ automatically updates the corrupt-state probe — no
// hand-maintenance of a parallel list.
//
//nolint:gochecknoglobals // immutable; populated at init from entmigrate.Tables
var appTables []string

//nolint:gochecknoinits // populates appTables from the generated Ent migrate.Tables
func init() {
	appTables = make([]string, 0, len(entmigrate.Tables))
	for _, t := range entmigrate.Tables {
		appTables = append(appTables, t.Name)
	}
}
