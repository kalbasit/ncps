package database

import (
	"database/sql"
	"fmt"
	"time"

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
		file_size INTEGER NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
		updated_at TIMESTAMP,
		last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	`

	insertNarInfoQuery = `INSERT into narinfos(hash) VALUES (?)`

	insertNarQuery = `
	INSERT into nars(narinfo_id, hash, compression, file_size) VALUES (?, ?, ?, ?)
	`
)

type (
	// DB is the main database wrapping *sql.DB and have functions that can
	// operate on nar and narinfos.
	DB struct {
		*sql.DB

		logger log15.Logger
	}

	// NarInfoModel represents a narinfo record in the database; This is not the
	// same as narinfo.NarInfo!
	NarInfoModel struct {
		ID   int64
		Hash string

		CreatedAt      time.Time
		UpdatedAt      *time.Time
		LastAccessedAt time.Time
	}

	// NarModel represents a nar record in the database.
	NarModel struct {
		ID          int64
		NarInfoID   int64
		Hash        string
		Compression string
		FileSize    int64

		CreatedAt      time.Time
		UpdatedAt      *time.Time
		LastAccessedAt time.Time
	}
)

// Open opens a sqlite3 database, and creates it if necessary.
func Open(logger log15.Logger, dbpath string) (*DB, error) {
	sdb, err := sql.Open("sqlite3", dbpath)
	if err != nil {
		return nil, fmt.Errorf("error opening the SQLite3 database at %q: %w", dbpath, err)
	}

	db := &DB{DB: sdb, logger: logger.New("dbpath", dbpath)}

	return db, db.createTables()
}

// InsertNarInfoRecord creates a new narinfo record in the database.
func (db *DB) InsertNarInfoRecord(tx *sql.Tx, hash string) (sql.Result, error) {
	stmt, err := tx.Prepare(insertNarInfoQuery)
	if err != nil {
		return nil, fmt.Errorf("error preparing a statement: %w", err)
	}
	defer stmt.Close()

	res, err := stmt.Exec(hash)
	if err != nil {
		return nil, fmt.Errorf("error executing the statement: %w", err)
	}

	return res, nil
}

// InsertNarRecord creates a new nar record in the database.
func (db *DB) InsertNarRecord(tx *sql.Tx, narInfoID int64,
	hash, compression string, fileSize int64,
) (sql.Result, error) {
	stmt, err := tx.Prepare(insertNarQuery)
	if err != nil {
		return nil, fmt.Errorf("error preparing a statement: %w", err)
	}
	defer stmt.Close()

	res, err := stmt.Exec(narInfoID, hash, compression, fileSize)
	if err != nil {
		return nil, fmt.Errorf("error executing the statement: %w", err)
	}

	return res, nil
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
