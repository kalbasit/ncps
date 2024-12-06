package database

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/inconshreveable/log15/v3"

	// Import the SQLite driver.
	"github.com/mattn/go-sqlite3"
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

	getNarInfoQuery = `
	SELECT id, hash, created_at, updated_at, last_accessed_at
	FROM narinfos
	WHERE hash = ?
	`

	getNarQuery = `
	SELECT id, narinfo_id, hash, compression, file_size,
		created_at, updated_at, last_accessed_at
	FROM nars
	WHERE hash = ?
	`

	insertNarInfoQuery = `INSERT into narinfos(hash) VALUES (?)`

	insertNarQuery = `
	INSERT into nars(narinfo_id, hash, compression, file_size) VALUES (?, ?, ?, ?)
	`

	touchNarInfoQuery = `
	UPDATE narinfos
	SET last_accessed_at = CURRENT_TIMESTAMP,
		  updated_at = CURRENT_TIMESTAMP
	WHERE hash = ?
	`

	touchNarQuery = `
	UPDATE nars
	SET last_accessed_at = CURRENT_TIMESTAMP,
		  updated_at = CURRENT_TIMESTAMP
	WHERE hash = ?
	`
)

var (
	// ErrNotFound is returned if record is not found in the database.
	ErrNotFound = errors.New("not found")

	// ErrAlreadyExists is returned if record insertion failed due to uniqueness constraint.
	ErrAlreadyExists = errors.New("error already exists")
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
		FileSize    uint64

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

	// Getting an error `database is locked` when data is being inserted in the
	// database at a fast rate. This will slow down read/write from the database
	// but at least none of them will fail due to connection issues.
	sdb.SetMaxOpenConns(1)

	db := &DB{DB: sdb, logger: logger.New("dbpath", dbpath)}

	return db, db.createTables()
}

func (db *DB) GetNarInfoRecord(tx *sql.Tx, hash string) (NarInfoModel, error) {
	var nim NarInfoModel

	stmt, err := tx.Prepare(getNarInfoQuery)
	if err != nil {
		return nim, fmt.Errorf("error preparing a statement: %w", err)
	}
	defer stmt.Close()

	rows, err := stmt.Query(hash)
	if err != nil {
		return nim, fmt.Errorf("error executing the statement: %w", err)
	}
	defer rows.Close()

	nims := make([]NarInfoModel, 0)

	for rows.Next() {
		var nim NarInfoModel

		if err := rows.Scan(&nim.ID, &nim.Hash, &nim.CreatedAt, &nim.UpdatedAt, &nim.LastAccessedAt); err != nil {
			return nim, fmt.Errorf("error scanning the row into a NarInfoModel: %w", err)
		}

		nims = append(nims, nim)
	}

	if len(nims) == 0 {
		return nim, ErrNotFound
	}

	if len(nims) > 1 {
		return nim, fmt.Errorf("that's impossible but multiple narinfos were found with the same hash %q", hash)
	}

	return nims[0], nil
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
		sqliteErr, ok := errors.Unwrap(err).(sqlite3.Error)
		if ok && sqliteErr.Code == sqlite3.ErrConstraint {
			return nil, ErrAlreadyExists
		}

		return nil, fmt.Errorf("error executing the statement: %w", err)
	}

	return res, nil
}

// TouchNarInfoRecord updates the last_accessed_at of a narinfo record in the
// database.
func (db *DB) TouchNarInfoRecord(tx *sql.Tx, hash string) (sql.Result, error) {
	return db.touchRecord(tx, touchNarInfoQuery, hash)
}

func (db *DB) GetNarRecord(tx *sql.Tx, hash string) (NarModel, error) {
	var nm NarModel

	stmt, err := tx.Prepare(getNarQuery)
	if err != nil {
		return nm, fmt.Errorf("error preparing a statement: %w", err)
	}
	defer stmt.Close()

	rows, err := stmt.Query(hash)
	if err != nil {
		return nm, fmt.Errorf("error executing the statement: %w", err)
	}
	defer rows.Close()

	nms := make([]NarModel, 0)

	for rows.Next() {
		var nm NarModel

		err := rows.Scan(
			&nm.ID,
			&nm.NarInfoID,
			&nm.Hash,
			&nm.Compression,
			&nm.FileSize,
			&nm.CreatedAt,
			&nm.UpdatedAt,
			&nm.LastAccessedAt,
		)
		if err != nil {
			return nm, fmt.Errorf("error scanning the row into a NarInfoModel: %w", err)
		}

		nms = append(nms, nm)
	}

	if len(nms) == 0 {
		return nm, ErrNotFound
	}

	if len(nms) > 1 {
		return nm, fmt.Errorf("that's impossible but multiple nars were found with the same hash %q", hash)
	}

	return nms[0], nil
}

// InsertNarRecord creates a new nar record in the database.
func (db *DB) InsertNarRecord(tx *sql.Tx, narInfoID int64,
	hash, compression string, fileSize uint64,
) (sql.Result, error) {
	stmt, err := tx.Prepare(insertNarQuery)
	if err != nil {
		return nil, fmt.Errorf("error preparing a statement: %w", err)
	}
	defer stmt.Close()

	res, err := stmt.Exec(narInfoID, hash, compression, fileSize)
	if err != nil {
		sqliteErr, ok := errors.Unwrap(err).(sqlite3.Error)
		if ok && sqliteErr.Code == sqlite3.ErrConstraint {
			return nil, ErrAlreadyExists
		}

		return nil, fmt.Errorf("error executing the statement: %w", err)
	}

	return res, nil
}

func (db *DB) TouchNarRecord(tx *sql.Tx, hash string) (sql.Result, error) {
	return db.touchRecord(tx, touchNarQuery, hash)
}

func (db *DB) touchRecord(tx *sql.Tx, query, hash string) (sql.Result, error) {
	stmt, err := tx.Prepare(query)
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
