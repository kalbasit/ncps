package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kalbasit/ncps/pkg/database"
)

// ErrImpossibleState is returned when MySQL adoption finds a
// configuration that cannot arise from any happy-path operation (state
// S6 in design D6: both `schema_migrations` and
// `schema_migrations_dbmate_backup` exist, with `schema_migrations`
// still in dbmate shape). Indicates manual intervention or corruption.
var ErrImpossibleState = errors.New("migrate: impossible adoption state — manual intervention required")

// adopt converts the dbmate-shape `schema_migrations` tracking table to
// goose's canonical shape, preserving every version record. The
// per-dialect strategy is asymmetric:
//
//   - SQLite + Postgres: transactional table recreate. Atomic — on any
//     error the original dbmate table is intact.
//   - MySQL: non-transactional rename-then-rebuild dance with a backup
//     table that survives crashes. The state machine in Detect() picks
//     up partial state and resumes from the appropriate point.
//
// Idempotent: callers may invoke adopt unconditionally; if the table is
// already adopted (StateAdopted), this returns nil immediately.
func adopt(ctx context.Context, db *sql.DB, d database.Type, st State) error {
	switch st {
	case StateEmpty, StateAdopted:
		return nil
	case StateDbmate:
		switch d {
		case database.TypeSQLite, database.TypePostgreSQL:
			return adoptTransactional(ctx, db, d)
		case database.TypeMySQL:
			return adoptMySQLFromDbmate(ctx, db)
		case database.TypeUnknown:
			fallthrough
		default:
			return fmt.Errorf("adopt: %w %v", database.ErrUnknownDialect, d)
		}
	case StateMySQLS4:
		return adoptMySQLFromS4(ctx, db)
	case StateMySQLS5:
		return adoptMySQLFromS5(ctx, db)
	case StateImpossibleS6:
		return ErrImpossibleState
	case StateUnknown:
		fallthrough
	default:
		return fmt.Errorf("adopt: %w %v", ErrUnknownState, st)
	}
}

// adoptTransactional performs the SQLite/Postgres adoption inside a
// single transaction. The pattern is:
//
//  1. Create a temp table mirroring the data we need.
//  2. DROP the dbmate-shape schema_migrations.
//  3. CREATE the goose-shape schema_migrations.
//  4. INSERT the preserved versions, marked is_applied=true.
//  5. Verify row-count parity with the prior dbmate table.
//  6. COMMIT (or ROLLBACK if anything failed).
func adoptTransactional(ctx context.Context, db *sql.DB, d database.Type) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	// Deferred rollback is a no-op if Commit succeeds. Idiomatic + robust
	// against future early-return paths.
	defer func() { _ = tx.Rollback() }()

	// 1. Capture pre-count via a SELECT inside the transaction.
	var preCount int

	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations`).Scan(&preCount); err != nil {
		return fmt.Errorf("count dbmate rows: %w", err)
	}
	// 2. + 3. + 4. as a sequence of statements specific to each dialect.
	switch d {
	case database.TypeSQLite:
		if err := adoptSQLiteSteps(ctx, tx); err != nil {
			return err
		}
	case database.TypePostgreSQL:
		if err := adoptPostgresSteps(ctx, tx); err != nil {
			return err
		}
	case database.TypeMySQL, database.TypeUnknown:
		fallthrough
	default:
		return fmt.Errorf("adoptTransactional: %w %v", database.ErrUnknownDialect, d)
	}
	// 5. Verify row-count parity.
	var postCount int

	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations`).Scan(&postCount); err != nil {
		return fmt.Errorf("count adopted rows: %w", err)
	}

	// postCount = preCount + 1 because adoption inserts the goose
	// sentinel (version_id=0) alongside the preserved dbmate versions.
	if postCount != preCount+1 {
		//nolint:err113 // diagnostic; not meant for programmatic inspection
		return fmt.Errorf("migrate: adopt row-count mismatch: dbmate had %d, adopted %d (expected %d incl. sentinel)",
			preCount, postCount, preCount+1)
	}
	// 6. Commit.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// adoptSQLiteSteps does steps 2-4 for SQLite. CREATE TEMPORARY TABLE +
// INSERT/SELECT preserves the version values; DROP + CREATE rebuilds
// the table in goose shape; then we insert the goose sentinel
// version 0 so goose's existence-check fallback (which queries for
// version 0) recognises the table without trying to recreate it.
func adoptSQLiteSteps(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TEMPORARY TABLE schema_migrations_legacy AS
			SELECT version FROM "schema_migrations"`,
		`DROP TABLE "schema_migrations"`,
		`CREATE TABLE "schema_migrations" (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			version_id INTEGER NOT NULL,
			is_applied INTEGER NOT NULL,
			tstamp TIMESTAMP DEFAULT (datetime('now'))
		)`,
		`INSERT INTO "schema_migrations" (version_id, is_applied, tstamp)
			VALUES (0, 1, datetime('now'))`,
		`INSERT INTO "schema_migrations" (version_id, is_applied, tstamp)
			SELECT CAST(version AS INTEGER), 1, datetime('now')
			FROM schema_migrations_legacy`,
		`DROP TABLE schema_migrations_legacy`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("sqlite adopt step %q: %w", firstLine(s), err)
		}
	}

	return nil
}

// adoptPostgresSteps does steps 2-4 for Postgres. See adoptSQLiteSteps
// for the version-0 sentinel rationale.
func adoptPostgresSteps(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TEMPORARY TABLE schema_migrations_legacy AS
			SELECT version FROM schema_migrations`,
		`DROP TABLE schema_migrations`,
		`CREATE TABLE schema_migrations (
			id serial NOT NULL,
			version_id bigint NOT NULL,
			is_applied boolean NOT NULL,
			tstamp timestamp NOT NULL DEFAULT now(),
			PRIMARY KEY(id)
		)`,
		`INSERT INTO schema_migrations (version_id, is_applied, tstamp)
			VALUES (0, true, now())`,
		`INSERT INTO schema_migrations (version_id, is_applied, tstamp)
			SELECT CAST(version AS BIGINT), true, now()
			FROM schema_migrations_legacy`,
		`DROP TABLE schema_migrations_legacy`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("postgres adopt step %q: %w", firstLine(s), err)
		}
	}

	return nil
}

// adoptMySQLFromDbmate performs the S3 (full) adoption: rename, create,
// insert, verify, drop backup. MySQL does not support transactional
// DDL; a crash between any two statements leaves the database in a
// recoverable state per the S4/S5 logic.
func adoptMySQLFromDbmate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx,
		"ALTER TABLE `schema_migrations` RENAME TO `schema_migrations_dbmate_backup`"); err != nil {
		return fmt.Errorf("rename to backup: %w", err)
	}

	return adoptMySQLFromS4(ctx, db)
}

// adoptMySQLFromS4 resumes adoption from the state where the dbmate
// table has been renamed but the new schema_migrations doesn't exist
// yet. Creates the new table, inserts the goose sentinel (version 0),
// copies dbmate-era rows, verifies, drops backup.
func adoptMySQLFromS4(ctx context.Context, db *sql.DB) error {
	if err := createMySQLSchemaMigrations(ctx, db); err != nil {
		return fmt.Errorf("create new schema_migrations: %w", err)
	}

	if _, err := db.ExecContext(ctx,
		"INSERT INTO `schema_migrations` (`version_id`, `is_applied`, `tstamp`) "+
			"VALUES (0, 1, NOW())"); err != nil {
		return fmt.Errorf("insert sentinel: %w", err)
	}

	if _, err := db.ExecContext(ctx,
		"INSERT INTO `schema_migrations` (`version_id`, `is_applied`, `tstamp`) "+
			"SELECT CAST(`version` AS UNSIGNED), 1, NOW() "+
			"FROM `schema_migrations_dbmate_backup`"); err != nil {
		return fmt.Errorf("copy from backup: %w", err)
	}

	return adoptMySQLFromS5(ctx, db)
}

// adoptMySQLFromS5 finalises adoption from the state where both the
// new schema_migrations and the backup exist. Verifies row-count
// parity; if it matches, drops the backup. If counts mismatch, the
// most likely cause is a crash partway through INSERT — truncate the
// new table and copy fresh from backup.
func adoptMySQLFromS5(ctx context.Context, db *sql.DB) error {
	backupCount, newCount, err := mysqlAdoptionCounts(ctx, db)
	if err != nil {
		return fmt.Errorf("count rows: %w", err)
	}

	// Expected: newCount = backupCount + 1 (the goose version-0 sentinel).
	if newCount != backupCount+1 {
		if err := mysqlS5Recover(ctx, db); err != nil {
			return err
		}
	}

	if _, err := db.ExecContext(ctx,
		"DROP TABLE `schema_migrations_dbmate_backup`"); err != nil {
		return fmt.Errorf("drop backup: %w", err)
	}

	return nil
}

// mysqlS5Recover is the partial-copy recovery path for S5: truncate
// the new schema_migrations table and re-insert the sentinel + backup
// rows. Extracted from adoptMySQLFromS5 to keep the cyclomatic
// complexity of the top-level adoption flow low.
func mysqlS5Recover(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "TRUNCATE TABLE `schema_migrations`"); err != nil {
		return fmt.Errorf("truncate during S5 recovery: %w", err)
	}

	if _, err := db.ExecContext(ctx,
		"INSERT INTO `schema_migrations` (`version_id`, `is_applied`, `tstamp`) "+
			"VALUES (0, 1, NOW())"); err != nil {
		return fmt.Errorf("insert sentinel during S5 recovery: %w", err)
	}

	if _, err := db.ExecContext(ctx,
		"INSERT INTO `schema_migrations` (`version_id`, `is_applied`, `tstamp`) "+
			"SELECT CAST(`version` AS UNSIGNED), 1, NOW() "+
			"FROM `schema_migrations_dbmate_backup`"); err != nil {
		return fmt.Errorf("re-copy during S5 recovery: %w", err)
	}

	backupCount, newCount, err := mysqlAdoptionCounts(ctx, db)
	if err != nil {
		return fmt.Errorf("re-count rows: %w", err)
	}

	if newCount != backupCount+1 {
		//nolint:err113 // diagnostic; not meant for programmatic inspection
		return fmt.Errorf("migrate: S5 recovery still mismatched: backup %d, new %d (expected %d)",
			backupCount, newCount, backupCount+1)
	}

	return nil
}

func mysqlAdoptionCounts(ctx context.Context, db *sql.DB) (preCount, postCount int, err error) {
	if err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM `schema_migrations_dbmate_backup`").Scan(&preCount); err != nil {
		return 0, 0, fmt.Errorf("count backup: %w", err)
	}

	if err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM `schema_migrations`").Scan(&postCount); err != nil {
		return 0, 0, fmt.Errorf("count new: %w", err)
	}

	return preCount, postCount, nil
}

// createMySQLSchemaMigrations creates the schema_migrations tracking
// table in the shape goose v3 expects for MySQL. Used during the S4
// recovery step of the MySQL state-machine adoption.
//
// The shape matches goose's MySQL dialect (id BIGINT UNSIGNED
// AUTO_INCREMENT PRIMARY KEY, version_id BIGINT NOT NULL, is_applied
// BOOLEAN NOT NULL, tstamp TIMESTAMP NULL DEFAULT NOW()).
func createMySQLSchemaMigrations(ctx context.Context, db *sql.DB) error {
	stmt := "" +
		"CREATE TABLE `schema_migrations` (" +
		"  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT," +
		"  `version_id` BIGINT NOT NULL," +
		"  `is_applied` BOOLEAN NOT NULL," +
		"  `tstamp` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP," +
		"  PRIMARY KEY (`id`)" +
		") ENGINE=InnoDB"
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("create mysql schema_migrations: %w", err)
	}

	return nil
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}

	return s
}
