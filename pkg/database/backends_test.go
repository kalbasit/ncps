package database_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/testhelper"
)

func TestBackends(t *testing.T) {
	t.Parallel()

	backends := []struct {
		name   string
		envVar string
		setup  querierFactory
	}{
		{
			name: "SQLite",
			setup: func(t *testing.T) database.Querier {
				t.Helper()

				dir, err := os.MkdirTemp("", "database-path-")
				require.NoError(t, err)

				dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
				testhelper.CreateMigrateDatabase(t, dbFile)

				db, err := database.Open("sqlite:"+dbFile, nil)
				require.NoError(t, err)

				t.Cleanup(func() {
					db.DB().Close()
					os.RemoveAll(dir)
				})

				return db
			},
		},

		{
			name:   "PostgreSQL",
			envVar: "NCPS_TEST_ADMIN_POSTGRES_URL",
			setup: func(t *testing.T) database.Querier {
				t.Helper()

				// Connect to the test database and create the new ephemeral database.
				dbURL := os.Getenv("NCPS_TEST_ADMIN_POSTGRES_URL")

				db, err := database.Open(dbURL, nil)
				require.NoError(t, err, "failed to connect to the postgres database")

				dbName := "test-" + helper.MustRandString(58, nil)

				_, err = db.DB().ExecContext(context.Background(), "SELECT create_test_db($1);", dbName)
				require.NoError(t, err, "failed to create database %s", dbName)

				if err := db.DB().Close(); err != nil {
					t.Logf("error closing the connection to the testing database: %s", err)
				}

				// Replace the test-db with the ephemeral database in the dbURL
				dbURL = replaceDataseName(t, dbURL, dbName)

				// Migrate the ephemeral database.

				var errMigration error

				// The testhelper uses `require` which panics on failure. We must recover
				// from this to capture the error and prevent other tests from running
				// on a non-migrated database.
				defer func() {
					if r := recover(); r != nil {
						errMigration = fmt.Errorf("database migration panicked: %v", r) //nolint:err113
					}
				}()

				testhelper.MigratePostgresDatabase(t, dbURL)

				if errMigration != nil {
					t.Fatalf("Failed to migrate PostgreSQL database: %v", errMigration)
				}

				// Connect to the ephemeral database.
				db, err = database.Open(dbURL, nil)
				require.NoError(t, err)

				t.Cleanup(func() {
					_, err := db.DB().ExecContext(context.Background(), "SELECT drop_test_db($1);", dbName)
					if err != nil {
						t.Logf("error deleting the testing database: %s", err)
					}

					if err := db.DB().Close(); err != nil {
						t.Logf("error closing the connection to the testing database: %s", err)
					}
				})

				return db
			},
		},

		{
			name:   "MySQL",
			envVar: "NCPS_TEST_ADMIN_MYSQL_URL",
			setup: func(t *testing.T) database.Querier {
				t.Helper()

				// Connect to the test database and create the new ephemeral database.
				dbURL := os.Getenv("NCPS_TEST_ADMIN_MYSQL_URL")

				db, err := database.Open(dbURL, nil)
				require.NoError(t, err, "failed to connect to the mysql database")

				dbName := "test-" + helper.MustRandString(58, nil)

				_, err = db.DB().ExecContext(context.Background(), fmt.Sprintf("CREATE DATABASE `%s`;", dbName))
				require.NoError(t, err, "failed to create database %s", dbName)

				if err := db.DB().Close(); err != nil {
					t.Logf("error closing the connection to the testing database: %s", err)
				}

				// Replace the test-db with the ephemeral database in the dbURL
				dbURL = replaceDataseName(t, dbURL, dbName)

				// Migrate the ephemeral database.

				var errMigration error

				// The testhelper uses `require` which panics on failure. We must recover
				// from this to capture the error and prevent other tests from running
				// on a non-migrated database.
				defer func() {
					if r := recover(); r != nil {
						errMigration = fmt.Errorf("database migration panicked: %v", r) //nolint:err113
					}
				}()

				testhelper.MigrateMySQLDatabase(t, dbURL)

				if errMigration != nil {
					t.Fatalf("Failed to migrate MySQL database: %v", errMigration)
				}

				// Connect to the ephemeral database.
				db, err = database.Open(dbURL, nil)
				require.NoError(t, err)

				t.Cleanup(func() {
					_, err := db.DB().ExecContext(context.Background(), fmt.Sprintf("DROP DATABASE `%s`;", dbName))
					if err != nil {
						t.Logf("error deleting the testing database: %s", err)
					}

					if err := db.DB().Close(); err != nil {
						t.Logf("error closing the connection to the testing database: %s", err)
					}
				})

				return db
			},
		},
	}

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			// Skip logic
			if b.envVar != "" && os.Getenv(b.envVar) == "" {
				t.Skipf("Skipping %s: %s not set", b.name, b.envVar)
			}

			// Run the unified suite
			runComplianceSuite(t, b.setup)
		})
	}
}

func replaceDataseName(t *testing.T, dbURL, dbName string) string {
	u, err := url.Parse(dbURL)
	require.NoError(t, err)

	u.Path = "/" + dbName

	return u.String()
}
