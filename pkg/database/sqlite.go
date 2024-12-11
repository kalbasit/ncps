package database

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/inconshreveable/log15/v3"
	"github.com/mattn/go-sqlite3"
)

const (
	getNarInfoQuery = `
	SELECT id, hash, created_at, updated_at, last_accessed_at
	FROM narinfos
	WHERE hash = ?
	`

	getNarInfoIDQuery = `
	SELECT id, hash, created_at, updated_at, last_accessed_at
	FROM narinfos
	WHERE id = ?
	`

	getNarQuery = `
	SELECT
		id,
		narinfo_id,
		hash,
		compression,
		file_size,
		created_at,
		updated_at,
		last_accessed_at
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

	deletNarInfoQuery = `
	DELETE FROM narinfos
	WHERE hash = ?
	`

	deletNarQuery = `
	DELETE FROM nars
	WHERE hash = ?
	`

	narTotalSizeQuery = `
	SELECT SUM(file_size) as total_size FROM nars;
	`

	leastUsedNarsQuery = `
	SELECT
		id,
		narinfo_id,
		hash,
		compression,
		file_size,
		created_at,
		updated_at,
		last_accessed_at
	FROM (
		SELECT 
			*,
			(
				SELECT SUM(file_size)
				FROM nars n2
				WHERE n2.last_accessed_at <= n1.last_accessed_at
				ORDER BY last_accessed_at ASC
			) AS running_total
			FROM nars n1
			ORDER BY last_accessed_at ASC
	)
	WHERE running_total <= ?;
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

	return db, nil
}

// GetNarInfoRecordByID returns a narinfo record given its hash. If no nar was
// found with the given hash then ErrNotFound is returned instead.
func (db *DB) GetNarInfoRecordByID(tx *sql.Tx, id int64) (NarInfo, error) {
	return db.getNarInfoRecord(tx, getNarInfoIDQuery, id)
}

// GetNarInfoRecord returns a narinfo record given its hash. If no nar was
// found with the given hash then ErrNotFound is returned instead.
func (db *DB) GetNarInfoRecord(tx *sql.Tx, hash string) (NarInfo, error) {
	return db.getNarInfoRecord(tx, getNarInfoQuery, hash)
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
		sqliteErr, ok := err.(sqlite3.Error)
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
	return db.stmtExec(tx, touchNarInfoQuery, hash)
}

// DeleteNarInfoRecord deletes the narinfo record.
func (db *DB) DeleteNarInfoRecord(tx *sql.Tx, hash string) error {
	_, err := db.stmtExec(tx, deletNarInfoQuery, hash)

	return err
}

// GetNarRecord returns a nar record given its hash. If no nar was found with
// the given hash then ErrNotFound is returned instead.
func (db *DB) GetNarRecord(tx *sql.Tx, hash string) (Nar, error) {
	var nm Nar

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

	nms := make([]Nar, 0)

	for rows.Next() {
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
			return nm, fmt.Errorf("error scanning the row into a Nar: %w", err)
		}

		nms = append(nms, nm)
	}

	if err := rows.Err(); err != nil {
		return nm, fmt.Errorf("error returned from rows: %w", err)
	}

	if len(nms) == 0 {
		return nm, ErrNotFound
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
		sqliteErr, ok := err.(sqlite3.Error)
		if ok && sqliteErr.Code == sqlite3.ErrConstraint {
			return nil, ErrAlreadyExists
		}

		return nil, fmt.Errorf("error executing the statement: %w", err)
	}

	return res, nil
}

// TouchNarRecord updates the last_accessed_at of a nar record in the database.
func (db *DB) TouchNarRecord(tx *sql.Tx, hash string) (sql.Result, error) {
	return db.stmtExec(tx, touchNarQuery, hash)
}

// DeleteNarInfoRecord deletes the narinfo record.
func (db *DB) DeleteNarRecord(tx *sql.Tx, hash string) error {
	_, err := db.stmtExec(tx, deletNarQuery, hash)

	return err
}

// NarTotalSize returns the sum of FileSize of all nar records.
func (db *DB) NarTotalSize(tx *sql.Tx) (uint64, error) {
	stmt, err := tx.Prepare(narTotalSizeQuery)
	if err != nil {
		return 0, fmt.Errorf("error preparing a statement: %w", err)
	}
	defer stmt.Close()

	rows, err := stmt.Query()
	if err != nil {
		return 0, fmt.Errorf("error querying the statement: %w", err)
	}

	defer rows.Close()

	var size uint64

	for rows.Next() {
		if err := rows.Scan(&size); err != nil {
			return 0, err
		}
	}

	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("error returned from rows: %w", err)
	}

	return size, nil
}

// GetLeastAccessedNarRecords returns all records with the oldest
// last_accessed_at up to totalFileSize left behind.
func (db *DB) GetLeastAccessedNarRecords(tx *sql.Tx, totalFileSize uint64) ([]Nar, error) {
	stmt, err := tx.Prepare(leastUsedNarsQuery)
	if err != nil {
		return nil, fmt.Errorf("error preparing a statement: %w", err)
	}
	defer stmt.Close()

	rows, err := stmt.Query(totalFileSize)
	if err != nil {
		return nil, fmt.Errorf("error querying the statement: %w", err)
	}
	defer rows.Close()

	nms := make([]Nar, 0)

	for rows.Next() {
		var nm Nar

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
			return nms, fmt.Errorf("error scanning the row into a Nar: %w", err)
		}

		nms = append(nms, nm)
	}

	if err := rows.Err(); err != nil {
		return nms, fmt.Errorf("error returned from rows: %w", err)
	}

	return nms, nil
}

func (db *DB) stmtExec(tx *sql.Tx, query string, args ...any) (sql.Result, error) {
	stmt, err := tx.Prepare(query)
	if err != nil {
		return nil, fmt.Errorf("error preparing a statement: %w", err)
	}
	defer stmt.Close()

	res, err := stmt.Exec(args...)
	if err != nil {
		return nil, fmt.Errorf("error executing the statement: %w", err)
	}

	return res, nil
}

func (db *DB) getNarInfoRecord(tx *sql.Tx, query string, args ...any) (NarInfo, error) {
	var nim NarInfo

	stmt, err := tx.Prepare(query)
	if err != nil {
		return nim, fmt.Errorf("error preparing a statement: %w", err)
	}
	defer stmt.Close()

	rows, err := stmt.Query(args...)
	if err != nil {
		return nim, fmt.Errorf("error executing the statement: %w", err)
	}
	defer rows.Close()

	nims := make([]NarInfo, 0)

	for rows.Next() {
		if err := rows.Scan(&nim.ID, &nim.Hash, &nim.CreatedAt, &nim.UpdatedAt, &nim.LastAccessedAt); err != nil {
			return nim, fmt.Errorf("error scanning the row into a NarInfo: %w", err)
		}

		nims = append(nims, nim)
	}

	if err := rows.Err(); err != nil {
		return nim, fmt.Errorf("error returned from rows: %w", err)
	}

	if len(nims) == 0 {
		return nim, ErrNotFound
	}

	return nims[0], nil
}
