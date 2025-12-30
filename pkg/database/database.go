package database

import (
	"database/sql"
	"fmt"
	"net/url"
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

// Open opens a database connection and returns a Querier implementation.
// The database type is determined from the URL scheme:
//   - sqlite:// or sqlite3:// for SQLite
//   - postgres:// or postgresql:// for PostgreSQL
//   - mysql:// for MySQL/MariaDB
func Open(dbURL string) (Querier, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing the database URL %q: %w", dbURL, err)
	}

	var sdb *sql.DB

	scheme := strings.ToLower(u.Scheme)

	switch scheme {
	case "sqlite", "sqlite3":
		sdb, err = openSQLite(u)
	case "postgres", "postgresql":
		sdb, err = openPostgreSQL(dbURL)
	case "mysql":
		sdb, err = openMySQL(dbURL)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedDriver, scheme)
	}

	if err != nil {
		return nil, fmt.Errorf("error opening the database at %q: %w", dbURL, err)
	}

	// Return the appropriate wrapper based on the scheme
	switch scheme {
	case "sqlite", "sqlite3":
		return &sqliteWrapper{adapter: sqlitedb.NewAdapter(sdb)}, nil
	case "postgres", "postgresql":
		return &postgresWrapper{adapter: postgresdb.NewAdapter(sdb)}, nil
	case "mysql":
		return &mysqlWrapper{adapter: mysqldb.NewAdapter(sdb)}, nil
	default:
		// This should never happen due to the switch above, but included for safety
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedDriver, scheme)
	}
}

func openSQLite(u *url.URL) (*sql.DB, error) {
	sdb, err := otelsql.Open("sqlite3", u.Path, otelsql.WithAttributes(
		semconv.DBSystemSqlite,
	))
	if err != nil {
		return nil, err
	}

	// Getting an error `database is locked` when data is being inserted in the
	// database at a fast rate. This will slow down read/write from the database
	// but at least none of them will fail due to connection issues.
	sdb.SetMaxOpenConns(1)

	return sdb, nil
}

func openPostgreSQL(dbURL string) (*sql.DB, error) {
	sdb, err := otelsql.Open("pgx", dbURL, otelsql.WithAttributes(
		semconv.DBSystemPostgreSQL,
	))
	if err != nil {
		return nil, err
	}

	// PostgreSQL can handle concurrent connections well
	// Set reasonable defaults for connection pooling
	sdb.SetMaxOpenConns(25)
	sdb.SetMaxIdleConns(5)

	return sdb, nil
}

func openMySQL(dbURL string) (*sql.DB, error) {
	// Convert mysql://user:pass@host:port/database to the format expected by go-sql-driver/mysql
	// mysql://user:pass@tcp(host:port)/database?params
	u, err := url.Parse(dbURL)
	if err != nil {
		return nil, err
	}

	// Build DSN using mysql.Config for safer handling of special characters
	cfg := mysql.NewConfig()

	if u.User != nil {
		cfg.User = u.User.Username()
		if password, ok := u.User.Password(); ok {
			cfg.Passwd = password
		}
	}

	if u.Host != "" {
		cfg.Net = "tcp"
		cfg.Addr = u.Host
	}

	// Remove leading slash from path to get database name
	if u.Path != "" {
		cfg.DBName = strings.TrimPrefix(u.Path, "/")
	}

	// Parse query parameters into cfg.Params
	if u.RawQuery != "" {
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return nil, fmt.Errorf("error parsing MySQL query parameters: %w", err)
		}

		cfg.Params = make(map[string]string)

		for k, v := range query {
			if len(v) > 0 {
				cfg.Params[k] = v[0]
			}
		}
	} else {
		// Set sensible defaults for MySQL
		cfg.Params = map[string]string{
			"parseTime": "true",
			"loc":       "UTC",
		}
	}

	dsn := cfg.FormatDSN()

	sdb, err := otelsql.Open("mysql", dsn, otelsql.WithAttributes(
		semconv.DBSystemMySQL,
	))
	if err != nil {
		return nil, err
	}

	// MySQL can handle concurrent connections well
	// Set reasonable defaults for connection pooling
	sdb.SetMaxOpenConns(25)
	sdb.SetMaxIdleConns(5)

	return sdb, nil
}
