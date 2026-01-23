//go:generate gen-db-wrappers postgresdb/querier.go
package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/XSAM/otelsql"
	"github.com/go-sql-driver/mysql"

	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"

	"github.com/kalbasit/ncps/pkg/database/mysqldb"
	"github.com/kalbasit/ncps/pkg/database/postgresdb"
	"github.com/kalbasit/ncps/pkg/database/sqlitedb"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
	_ "github.com/mattn/go-sqlite3"    // SQLite driver
)

const (
	netTypeUnix      = "unix"
	schemePostgres   = "postgres"
	schemePostgresql = "postgresql"
)

// PoolConfig holds database connection pool settings.
type PoolConfig struct {
	// MaxOpenConns is the maximum number of open connections to the database.
	// If <= 0, defaults are used based on database type.
	MaxOpenConns int
	// MaxIdleConns is the maximum number of connections in the idle connection pool.
	// If <= 0, defaults are used based on database type.
	MaxIdleConns int
}

// Open opens a database connection and returns a Querier implementation.
// The database type is determined from the URL scheme:
//   - sqlite:// or sqlite3:// for SQLite
//   - postgres:// or postgresql:// for PostgreSQL
//   - mysql:// for MySQL/MariaDB
//
// The poolCfg parameter is optional. If nil, sensible defaults are used based on
// the database type. SQLite uses MaxOpenConns=1, PostgreSQL and MySQL use higher values.
func Open(dbURL string, poolCfg *PoolConfig) (Querier, error) {
	dbType, err := DetectFromDatabaseURL(dbURL)
	if err != nil {
		return nil, err
	}

	var sdb *sql.DB

	switch dbType {
	case TypeMySQL:
		sdb, err = openMySQL(dbURL, poolCfg)
	case TypePostgreSQL:
		sdb, err = openPostgreSQL(dbURL, poolCfg)
	case TypeSQLite:
		sdb, err = openSQLite(dbURL, poolCfg)
	case TypeUnknown:
		fallthrough
	default:
		// This should never happen due to detection above, but included for safety
		return nil, ErrUnsupportedDriver
	}

	if err != nil {
		return nil, fmt.Errorf("error opening the database at %q: %w", dbURL, err)
	}

	// Return the appropriate wrapper based on the scheme
	switch dbType {
	case TypeMySQL:
		return &mysqlWrapper{adapter: mysqldb.NewAdapter(sdb)}, nil
	case TypePostgreSQL:
		return &postgresWrapper{adapter: postgresdb.NewAdapter(sdb)}, nil
	case TypeSQLite:
		return &sqliteWrapper{adapter: sqlitedb.NewAdapter(sdb)}, nil
	case TypeUnknown:
		fallthrough
	default:
		// This should never happen due to detection above, but included for safety
		return nil, ErrUnsupportedDriver
	}
}

// applyPoolSettings applies connection pool settings to the database connection.
// It uses the provided defaults and overrides them with values from poolCfg if they are positive.
func applyPoolSettings(sdb *sql.DB, poolCfg *PoolConfig, defaultMaxOpen, defaultMaxIdle int) {
	maxOpen := defaultMaxOpen
	maxIdle := defaultMaxIdle

	if poolCfg != nil {
		if poolCfg.MaxOpenConns > 0 {
			maxOpen = poolCfg.MaxOpenConns
		}

		if poolCfg.MaxIdleConns > 0 {
			maxIdle = poolCfg.MaxIdleConns
		}
	}

	if maxOpen > 0 {
		sdb.SetMaxOpenConns(maxOpen)
	}

	if maxIdle > 0 {
		sdb.SetMaxIdleConns(maxIdle)
	}
}

func openSQLite(dbURL string, poolCfg *PoolConfig) (*sql.DB, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return nil, err
	}

	sdb, err := otelsql.Open("sqlite3", u.Path, otelsql.WithAttributes(
		semconv.DBSystemSqlite,
	))
	if err != nil {
		return nil, err
	}

	// Enable foreign key constraints (disabled by default in SQLite)
	// This is required for CASCADE DELETE to work
	if _, err := sdb.ExecContext(context.Background(), "PRAGMA foreign_keys = ON"); err != nil {
		return nil, fmt.Errorf("error enabling foreign keys: %w", err)
	}

	// Getting an error `database is locked` when data is being inserted in the
	// database at a fast rate. This will slow down read/write from the database
	// but at least none of them will fail due to connection issues.
	// SQLite requires MaxOpenConns=1 to avoid "database is locked" errors.
	// This value is enforced and cannot be overridden by the user.
	sdb.SetMaxOpenConns(1)

	// Allow user to configure MaxIdleConns if desired
	if poolCfg != nil && poolCfg.MaxIdleConns > 0 {
		sdb.SetMaxIdleConns(poolCfg.MaxIdleConns)
	}

	return sdb, nil
}

func openPostgreSQL(dbURL string, poolCfg *PoolConfig) (*sql.DB, error) {
	processedURL, err := parsePostgreSQLURL(dbURL)
	if err != nil {
		return nil, err
	}

	sdb, err := otelsql.Open("pgx", processedURL, otelsql.WithAttributes(
		semconv.DBSystemPostgreSQL,
	))
	if err != nil {
		return nil, err
	}

	// PostgreSQL can handle concurrent connections well
	// Set reasonable defaults for connection pooling
	applyPoolSettings(sdb, poolCfg, 25, 5)

	return sdb, nil
}

func parsePostgreSQLURL(dbURL string) (string, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return "", err
	}

	// pgx only supports postgres:// and postgresql:// schemes.
	// If the user provided postgres+unix:// or similar, we normalize it
	// and restructure the URL for pgx.
	scheme := strings.ToLower(u.Scheme)
	if strings.Contains(scheme, "+unix") {
		socketDir, dbName := path.Split(u.Path)
		if dbName == "" {
			return "", fmt.Errorf("%w: missing database name in path: %s", ErrInvalidPostgresUnixURL, dbURL)
		}
		// After split, socketDir will have a trailing slash. If path is just "/dbname", it will be "/".
		if socketDir == "" {
			return "", fmt.Errorf("%w: missing socket directory in path: %s", ErrInvalidPostgresUnixURL, dbURL)
		}

		socketDir = path.Clean(socketDir) // Clean up extra slashes and trailing slash.

		// Rebuild URL for pgx: postgresql:///dbname?host=/path/to/socket
		u.Path = "/" + dbName
		q := u.Query()
		q.Set("host", socketDir)
		u.RawQuery = q.Encode()
	}

	if strings.Contains(scheme, "+") {
		if strings.HasPrefix(scheme, schemePostgresql) {
			u.Scheme = schemePostgresql
		} else if strings.HasPrefix(scheme, schemePostgres) {
			u.Scheme = schemePostgres
		}
	}

	return u.String(), nil
}

func openMySQL(dbURL string, poolCfg *PoolConfig) (*sql.DB, error) {
	cfg, err := parseMySQLConfig(dbURL)
	if err != nil {
		return nil, err
	}

	dsn := cfg.FormatDSN()

	sdb, err := otelsql.Open("mysql", dsn, otelsql.WithAttributes(
		semconv.DBSystemMySQL,
	))
	if err != nil {
		return nil, err
	}

	applyPoolSettings(sdb, poolCfg, 25, 5)

	return sdb, nil
}

func parseMySQLConfig(dbURL string) (*mysql.Config, error) {
	// Convert mysql://user:pass@host:port/database to the format expected by go-sql-driver/mysql
	u, err := url.Parse(dbURL)
	if err != nil {
		return nil, err
	}

	cfg := mysql.NewConfig()

	// 1. Set credentials
	if u.User != nil {
		cfg.User = u.User.Username()
		if password, ok := u.User.Password(); ok {
			cfg.Passwd = password
		}
	}

	// 2. Set address (TCP or Unix)
	query := u.Query()

	scheme := strings.ToLower(u.Scheme)
	switch {
	case strings.Contains(scheme, "+unix"):
		if err := parseMySQLUnixPath(cfg, u, dbURL); err != nil {
			return nil, err
		}
	case query.Get("socket") != "":
		cfg.Net = netTypeUnix
		cfg.Addr = query.Get("socket")
	case query.Get("unix_socket") != "":
		cfg.Net = netTypeUnix
		cfg.Addr = query.Get("unix_socket")
	case query.Get("host") != "" && strings.HasPrefix(query.Get("host"), "/"):
		cfg.Net = netTypeUnix
		cfg.Addr = query.Get("host")
	case u.Host != "":
		cfg.Net = "tcp"
		cfg.Addr = u.Host
	}

	if cfg.DBName == "" && u.Path != "" {
		cfg.DBName = strings.TrimPrefix(u.Path, "/")
	}

	// 3. Initialize params with your SAFE defaults
	// These run regardless of whether the user provided other params.
	cfg.Params = map[string]string{
		"parseTime": "true",     // Required for scanning into time.Time
		"loc":       "UTC",      // logical timezone for the driver
		"time_zone": "'+00:00'", // Server-side session timezone (Critical for your test fix)
	}

	// 4. Overwrite defaults if the user explicitly specified them in the URL
	for k, v := range query {
		if len(v) > 0 {
			cfg.Params[k] = v[0]
		}
	}

	return cfg, nil
}

func parseMySQLUnixPath(cfg *mysql.Config, u *url.URL, dbURL string) error {
	// Handle mysql+unix://<socket_path>/<db_name>
	socketPath, dbName := path.Split(u.Path)
	if dbName == "" {
		return fmt.Errorf("%w: missing database name in path: %s", ErrInvalidMySQLUnixURL, dbURL)
	}

	if socketPath == "" {
		return fmt.Errorf("%w: missing socket path in path: %s", ErrInvalidMySQLUnixURL, dbURL)
	}

	socketPath = path.Clean(socketPath)

	cfg.Net = netTypeUnix
	cfg.Addr = socketPath
	cfg.DBName = dbName

	return nil
}
