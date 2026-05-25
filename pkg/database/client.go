package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"entgo.io/ent/dialect"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/kalbasit/ncps/ent"
)

// SchemaCreateMu serialises Ent's Schema.Create across the process.
// Ent mutates the package-level `migrate.Tables` slice during
// Schema.Create; without a global lock, concurrent goroutines race on
// that slice. Every code path that calls Schema.Create (fresh-install
// in pkg/database/migrate, schema-bootstrap helpers in tests) must
// acquire this mutex.
//
//nolint:gochecknoglobals // process-wide concurrency guard.
var SchemaCreateMu sync.Mutex

// ErrUnknownDialect is returned when a database.Type cannot be mapped to
// an ent dialect string. Mirrors the sentinel in pkg/database/migrate.
var ErrUnknownDialect = errors.New("database: unknown dialect")

// ErrNilDB is returned by NewClient when the caller passes a nil
// *sql.DB.
var ErrNilDB = errors.New("database.NewClient: sdb is nil")

// ErrTransactionPanic wraps panics observed inside a WithTransaction
// callback. Callers can detect "this transaction died of a panic"
// via errors.Is without losing the original panic value (it is
// formatted into the error message via %v).
var ErrTransactionPanic = errors.New("transaction panicked")

// Client is the post-migration database surface. It owns an Ent client
// (and the underlying *sql.DB) and exposes the lifecycle helpers callers
// need: Ent for fluent queries, DB for raw access (e.g. by goose),
// WithTransaction for atomic blocks, and Close for shutdown.
//
// Per D11 of the migrate-to-ent-and-atlas openspec change, this type
// replaces the sqlc-generated `Querier` interface as the canonical
// dependency that callers (cache, server, ncps tooling) hold.
type Client struct {
	ent     *ent.Client
	sdb     *sql.DB
	dialect Type
}

// NewClient wraps an already-opened *sql.DB in an Ent client. The
// dialect must match the driver registered with sdb; mismatch will be
// caught at first query.
//
// Callers should normally go through database.Open (introduced in §11)
// which constructs the *sql.DB and the *Client together with the
// correct otelsql instrumentation. NewClient is exposed for tests and
// for the migrate package, which already owns its *sql.DB lifecycle.
func NewClient(sdb *sql.DB, t Type) (*Client, error) {
	if sdb == nil {
		return nil, ErrNilDB
	}

	entDialect, err := EntDialectFor(t)
	if err != nil {
		return nil, err
	}

	drv := entsql.OpenDB(entDialect, sdb)

	return &Client{
		ent:     ent.NewClient(ent.Driver(drv)),
		sdb:     sdb,
		dialect: t,
	}, nil
}

// Ent returns the wrapped Ent client. Callers issue fluent queries
// against this client (e.g. `c.Ent().NarInfo.Create()...`).
func (c *Client) Ent() *ent.Client { return c.ent }

// DB returns the underlying *sql.DB. Used by goose (migrate package)
// and by code that still needs raw access during the §11 transition.
// Direct *sql.DB use is discouraged for new code — prefer the Ent API.
func (c *Client) DB() *sql.DB { return c.sdb }

// Type returns the dialect this client was opened against.
func (c *Client) Type() Type { return c.dialect }

// Close closes the Ent client, which in turn closes the underlying
// *sql.DB. Safe to call once; subsequent calls return the *sql.DB
// "already closed" error.
func (c *Client) Close() error {
	if err := c.ent.Close(); err != nil {
		return fmt.Errorf("database.Client.Close: %w", err)
	}

	return nil
}

// WithTransaction runs fn inside an Ent transaction. On success the
// transaction is committed; on error (including panic) it is rolled
// back. Lifecycle errors are wrapped with `name` so caller logs and
// telemetry can attribute failures to the originating operation —
// matching the behaviour of the legacy *Cache.executeTransaction
// helper this replaces.
//
// Retry-on-deadlock is intentionally NOT handled here; that is a
// caller policy (see *Cache.withTransaction which wraps this).
func (c *Client) WithTransaction(
	ctx context.Context,
	name string,
	fn func(tx *ent.Tx) error,
) (retErr error) {
	tx, err := c.ent.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction for %s: %w", name, err)
	}

	// Deferred rollback is a no-op once Commit succeeds. The pattern
	// also protects against panics in fn — the tx is rolled back even
	// if the goroutine unwinds before reaching the explicit Commit.
	defer func() {
		if p := recover(); p != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				retErr = fmt.Errorf("%w in %s (rollback also failed: %v): %v",
					ErrTransactionPanic, name, rbErr, p)
			} else {
				retErr = fmt.Errorf("%w in %s: %v", ErrTransactionPanic, name, p)
			}
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			return fmt.Errorf("transaction for %s failed (rollback also failed: %w): %w", name, rbErr, err)
		}

		// If the error carries SQLSTATE 25P02 (in_failed_sql_transaction),
		// the pgx driver may have returned the connection to the pool before
		// the PostgreSQL-side transaction was fully rolled back. Issue an
		// explicit ROLLBACK via the pool so that any connection in the aborted
		// state is cleaned up before the next caller acquires it. This is
		// best-effort: use a fresh context so a cancelled caller ctx does not
		// prevent the cleanup.
		if IsAbortedTransactionError(err) {
			_, _ = c.sdb.ExecContext(context.Background(), "ROLLBACK") //nolint:contextcheck
		}

		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction for %s: %w", name, err)
	}

	return nil
}

// EntDialectFor maps ncps's database.Type to the corresponding ent
// dialect string. Kept exported so pkg/database/migrate can share the
// same mapping (it owns its own *sql.DB and needs to build its own
// ent driver for Schema.Create).
func EntDialectFor(t Type) (string, error) {
	switch t {
	case TypeSQLite:
		return dialect.SQLite, nil
	case TypePostgreSQL:
		return dialect.Postgres, nil
	case TypeMySQL:
		return dialect.MySQL, nil
	case TypeUnknown:
		fallthrough
	default:
		return "", fmt.Errorf("%w: %v", ErrUnknownDialect, t)
	}
}
