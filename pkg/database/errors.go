package database

import (
	"errors"

	"github.com/mattn/go-sqlite3"
)

// IsDuplicateKeyError checks if the error is a unique constraint violation
// Works across SQLite.
func IsDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}

	// SQLite
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code == sqlite3.ErrConstraint
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
)
