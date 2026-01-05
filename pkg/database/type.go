package database

import (
	"fmt"
	"net/url"
	"strings"
)

type Type uint8

const (
	TypeUnknown Type = iota
	TypeMySQL
	TypePostgreSQL
	TypeSQLite
)

// DetectFromDataseURL detects the database type given a database url.
func DetectFromDataseURL(dbURL string) (Type, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return TypeUnknown, fmt.Errorf("error parsing the database URL %q: %w", dbURL, err)
	}

	scheme := strings.ToLower(u.Scheme)

	switch scheme {
	case "mysql":
		return TypeMySQL, nil
	case "postgres", "postgresql":
		return TypePostgreSQL, nil
	case "sqlite", "sqlite3":
		return TypeSQLite, nil
	default:
		return TypeUnknown, fmt.Errorf("%w: %q", ErrUnsupportedDriver, scheme)
	}
}

// String returns the string representation of a Type.
func (t Type) String() string {
	switch t {
	case TypeMySQL:
		return "MySQL"
	case TypePostgreSQL:
		return "PostgreSQL"
	case TypeSQLite:
		return "SQLite"
	case TypeUnknown:
		fallthrough
	default:
		return "unknown"
	}
}
