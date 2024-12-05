package database

import (
	"database/sql"
	"fmt"

	"github.com/inconshreveable/log15/v3"

	// Import the SQLite driver.
	_ "github.com/mattn/go-sqlite3"
)

const (
	// narInfosTable represents all the narinfo files that are available in the store.
	// NOTE: Updating the structure here **will not** migrate the existing table!
	narInfosTable = `
	CREATE TABLE IF NOT EXISTS narinfos (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		hash TEXT NOT NULL UNIQUE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
		updated_at TIMESTAMP,
		last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	`

	// narsTable represents all the nar files that are available in the store.
	// NOTE: Updating the structure here **will not** migrate the existing table!
	narsTable = `
	CREATE TABLE IF NOT EXISTS nars (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		narinfo_id INTEGER NOT NULL REFERENCES narinfos(id),
		hash TEXT NOT NULL UNIQUE,
		compression TEXT NOT NULL DEFAULT '',
		filesize INTEGER NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
		updated_at TIMESTAMP,
		last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	`
)

type DB struct {
	*sql.DB

	logger log15.Logger
}

func Open(logger log15.Logger, dbpath string) (*DB, error) {
	sdb, err := sql.Open("sqlite3", dbpath)
	if err != nil {
		return nil, fmt.Errorf("error opening the SQLite3 database at %q: %w", dbpath, err)
	}

	db := &DB{DB: sdb, logger: logger.New("dbpath", dbpath)}

	return db, db.createTables()
}

func (db *DB) createTables() error {
	db.logger.Info("creating the narinfos table")

	if _, err := db.Exec(narInfosTable); err != nil {
		return fmt.Errorf("error creating the narinfos table: %w", err)
	}

	db.logger.Info("creating the nars table")

	if _, err := db.Exec(narsTable); err != nil {
		return fmt.Errorf("error creating the nars table: %w", err)
	}

	return nil
}
