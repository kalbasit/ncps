package database

import (
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mattn/go-sqlite3"
)

// IsDuplicateKeyError checks if the error is a unique constraint violation
// Works across SQLite, PostgreSQL, and MySQL.
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
	// ErrNotFound is returned when a query returns no rows.
	ErrNotFound = errors.New("not found")

	// ErrUnsupportedDriver is returned when the database driver is not recognized.
	ErrUnsupportedDriver = errors.New("unsupported database driver")

	// ErrInvalidPostgresUnixURL is returned when a postgres+unix URL is invalid.
	ErrInvalidPostgresUnixURL = errors.New("invalid postgres+unix URL")

	// ErrInvalidMySQLUnixURL is returned when a mysql+unix URL is invalid.
	ErrInvalidMySQLUnixURL = errors.New("invalid mysql+unix URL")
)
