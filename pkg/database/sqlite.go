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

//
// // GetNarInfoRecordByID returns a narinfo record given its hash. If no nar was
// // found with the given hash then ErrNotFound is returned instead.
// func (db *DB) GetNarInfoRecordByID(tx *sql.Tx, id int64) (NarInfo, error) {
// 	return db.db.WithTx(tx).GetNarInfoByID(context.Background(), id)
// }
//
// // GetNarInfoRecord returns a narinfo record given its hash. If no nar was
// // found with the given hash then ErrNotFound is returned instead.
// func (db *DB) GetNarInfoRecord(tx *sql.Tx, hash string) (NarInfo, error) {
// 	return db.db.WithTx(tx).GetNarInfoByHash(context.Background(), hash)
// }
//
// // InsertNarInfoRecord creates a new narinfo record in the database.
// func (db *DB) InsertNarInfoRecord(tx *sql.Tx, hash string) (sql.Result, error) {
// 	return db.db.WithTx(tx).CreateNarInfo(context.Background(), hash)
// }
//
// // TouchNarInfoRecord updates the last_accessed_at of a narinfo record in the
// // database.
// func (db *DB) TouchNarInfoRecord(tx *sql.Tx, hash string) (sql.Result, error) {
// 	return db.db.WithTx(tx).TouchNarInfo(context.Background(), hash)
// }
//
// // DeleteNarInfoRecord deletes the narinfo record.
// func (db *DB) DeleteNarInfoRecord(tx *sql.Tx, hash string) error {
// 	return db.db.WithTx(tx).DeleteNarInfoByHash(context.Background(), hash)
// }
//
// // GetNarRecord returns a nar record given its hash. If no nar was found with
// // the given hash then ErrNotFound is returned instead.
// func (db *DB) GetNarRecord(tx *sql.Tx, hash string) (Nar, error) {
// 	return db.db.WithTx(tx).GetNarByHash(context.Background(), hash)
// }
//
// // InsertNarRecord creates a new nar record in the database.
// func (db *DB) InsertNarRecord(tx *sql.Tx, narInfoID int64,
// 	hash, compression string, fileSize uint64,
// ) (sql.Result, error) {
// 	return db.db.WithTx(tx).CreateNar(context.Background(), CreateNarParams{
// 		Hash:        hash,
// 		Compression: compression,
// 	})
// }
//
// // TouchNarRecord updates the last_accessed_at of a nar record in the database.
// func (db *DB) TouchNarRecord(tx *sql.Tx, hash string) (sql.Result, error) {
// 	return db.db.WithTx(tx).TouchNar(context.Background(), hash)
// }
//
// // DeleteNarInfoRecord deletes the narinfo record.
// func (db *DB) DeleteNarRecord(tx *sql.Tx, hash string) error {
// 	return db.db.WithTx(tx).DeleteNarByHash(context.Background(), hash)
// }
//
// // NarTotalSize returns the sum of FileSize of all nar records.
// func (db *DB) NarTotalSize(tx *sql.Tx) (uint64, error) {
// 	return db.db.WithTx(tx).GetNarTotalSize(context.Background())
// }
//
// // GetLeastAccessedNarRecords returns all records with the oldest
// // last_accessed_at up to totalFileSize left behind.
// func (db *DB) GetLeastAccessedNarRecords(tx *sql.Tx, totalFileSize uint64) ([]Nar, error) {
// 	return db.db.WithTx(tx).GetLeastUsedNars(context.Background(), int64(totalFileSize))
// }
