package database

import (
	"database/sql"
	"fmt"

	"github.com/inconshreveable/log15/v3"
	"github.com/mattn/go-sqlite3"
)

// Open opens a sqlite3 database, and creates it if necessary.
func Open(logger log15.Logger, dbpath string) (*Queries, error) {
	sdb, err := sql.Open("sqlite3", dbpath)
	if err != nil {
		return nil, fmt.Errorf("error opening the SQLite3 database at %q: %w", dbpath, err)
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
