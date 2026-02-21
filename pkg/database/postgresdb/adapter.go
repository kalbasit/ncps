package postgresdb

import (
	"database/sql"
)

// Adapter wraps postgresdb.Queries and provides the DB() method.
type Adapter struct {
	*Queries
	db *sql.DB
}

// NewAdapter creates a new PostgreSQL adapter.
func NewAdapter(db *sql.DB) *Adapter {
	return &Adapter{
		Queries: New(db),
		db:      db,
	}
}

// DB returns the underlying database connection.
func (a *Adapter) DB() *sql.DB {
	return a.db
}

// WithTx returns a new Adapter with the queries scoped to the given transaction.
func (a *Adapter) WithTx(tx *sql.Tx) *Adapter {
	return &Adapter{
		Queries: a.Queries.WithTx(tx),
		db:      a.db,
	}
}

// DBTX returns the current database executor (can be a transaction or the pool).
func (a *Adapter) DBTX() DBTX {
	return a.Queries.db
}
