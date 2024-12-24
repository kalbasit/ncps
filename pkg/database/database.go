package database

import (
	"database/sql"
	"fmt"
	"net/url"

	"github.com/XSAM/otelsql"
	"github.com/mattn/go-sqlite3"

	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

// Open opens a sqlite3 database, and creates it if necessary.
func Open(dbURL string) (*Queries, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing the database URL %q: %w", dbURL, err)
	}

	var sdb *sql.DB

	switch u.Scheme {
	case "sqlite":
		sdb, err = otelsql.Open("sqlite3", u.Path, otelsql.WithAttributes(
			semconv.DBSystemSqlite,
		))
	default:
		//nolint:err113
		return nil, fmt.Errorf("driver %q unrecognized", u.Scheme)
	}

	if err != nil {
		return nil, fmt.Errorf("error opening the database at %q: %w", dbURL, err)
	}

	// Getting an error `database is locked` when data is being inserted in the
	// database at a fast rate. This will slow down read/write from the database
	// but at least none of them will fail due to connection issues.
	sdb.SetMaxOpenConns(1)

	return New(sdb), nil
}

func (q *Queries) DB() *sql.DB { return q.db.(*sql.DB) }

// ErrorIsNo returns true if the error is an sqlite3 error and its code match
// the errNo code.
func ErrorIsNo(err error, errNo sqlite3.ErrNo) bool {
	sqliteErr, ok := err.(sqlite3.Error)
	if !ok {
		return false
	}

	return sqliteErr.Code == errNo
}
