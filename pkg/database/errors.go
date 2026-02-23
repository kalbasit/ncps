package database

import (
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mattn/go-sqlite3"
)

// IsDeadlockError checks if the error is a deadlock or a "database busy" error.
// Works across SQLite, PostgreSQL, and MySQL.
func IsDeadlockError(err error) bool {
	if err == nil {
		return false
	}

	// SQLite
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		// ErrBusy (5) or ErrLocked (6) or ErrProtocol (15)
		return sqliteErr.Code == sqlite3.ErrBusy ||
			sqliteErr.Code == sqlite3.ErrLocked ||
			sqliteErr.Code == sqlite3.ErrProtocol
	}

	// PostgreSQL
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// 40001 is serialization_failure
		// 40P01 is deadlock_detected
		return pgErr.Code == "40001" || pgErr.Code == "40P01"
	}

	// MySQL/MariaDB
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		// 1213 is ER_LOCK_DEADLOCK
		// 1205 is ER_LOCK_WAIT_TIMEOUT
		return mysqlErr.Number == 1213 || mysqlErr.Number == 1205
	}

	// Fallback to string matching
	errStr := strings.ToLower(err.Error())

	return strings.Contains(errStr, "deadlock") ||
		strings.Contains(errStr, "database is locked") ||
		strings.Contains(errStr, "database is busy")
}

func IsDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}

	// SQLite
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code == sqlite3.ErrConstraint
	}

	// PostgreSQL
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// 23505 is unique_violation in PostgreSQL
		return pgErr.Code == "23505"
	}

	// MySQL/MariaDB
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		// 1062 is ER_DUP_ENTRY - Duplicate entry for key
		return mysqlErr.Number == 1062
	}

	// Fallback to string matching for MySQL errors that don't unwrap properly
	if strings.Contains(err.Error(), "Error 1062") || strings.Contains(err.Error(), "Duplicate entry") {
		return true
	}

	return false
}

// IsNotFoundError checks if the error indicates a row was not found.
func IsNotFoundError(err error) bool {
	return errors.Is(err, ErrNotFound)
}

var (
	// ErrUnsupportedDriver is returned when the database driver is not recognized.
	ErrUnsupportedDriver = errors.New("unsupported database driver")

	// ErrInvalidPostgresUnixURL is returned when a postgres+unix URL is invalid.
	ErrInvalidPostgresUnixURL = errors.New("invalid postgres+unix URL")

	// ErrInvalidMySQLUnixURL is returned when a mysql+unix URL is invalid.
	ErrInvalidMySQLUnixURL = errors.New("invalid mysql+unix URL")
)
