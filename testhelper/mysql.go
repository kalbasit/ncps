package testhelper

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/migrations"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/database/migrate"
)

// MigrateMySQLDatabase runs the same migrate.Up flow `ncps migrate up`
// uses in production: empty DB → ent.Schema.Create → final schema
// including the §10b surrogate-id columns on weak entities. Replaces
// the legacy dbmate invocation.
// The database URL should be in the format: mysql://user:password@host:port/database
func MigrateMySQLDatabase(t *testing.T, dbURL string) {
	t.Helper()

	dbClient, err := database.Open(dbURL, nil)
	require.NoError(t, err)

	defer dbClient.Close()

	sub, err := fs.Sub(migrations.FS, "mysql")
	require.NoError(t, err)

	require.NoError(t, migrate.Up(context.Background(), migrate.Options{
		DB:           dbClient.DB(),
		Dialect:      database.TypeMySQL,
		MigrationsFS: sub,
	}))
}

// SetupMySQL sets up a new temporary MySQL database for testing.
// It requires the NCPS_TEST_ADMIN_MYSQL_URL environment variable to
// be set. Returns the Ent-backed *database.Client, the database URL,
// and a cleanup function.
func SetupMySQL(t *testing.T) (*database.Client, string, func()) {
	t.Helper()

	adminDbURL := os.Getenv("NCPS_TEST_ADMIN_MYSQL_URL")
	if adminDbURL == "" {
		t.Skip("Skipping MySQL test: NCPS_TEST_ADMIN_MYSQL_URL not set")
	}

	adminDb, err := database.Open(adminDbURL, nil)
	require.NoError(t, err, "failed to connect to the mysql database")

	dbName := "test-" + MustRandString(58)

	_, err = adminDb.DB().ExecContext(context.Background(), fmt.Sprintf("CREATE DATABASE `%s`", dbName))
	require.NoError(t, err, "failed to create database %s", dbName)

	u, err := url.Parse(adminDbURL)
	require.NoError(t, err)

	u.Path = "/" + dbName
	dbURL := u.String()

	var errMigration error

	func() {
		defer func() {
			if r := recover(); r != nil {
				errMigration = fmt.Errorf("database migration panicked: %v", r) //nolint:err113
			}
		}()

		MigrateMySQLDatabase(t, dbURL)
	}()

	if errMigration != nil {
		t.Fatalf("Failed to migrate MySQL database: %v", errMigration)
	}

	dbClient, err := database.Open(dbURL, nil)
	require.NoError(t, err)

	cleanup := func() {
		_ = dbClient.Close()
		_, _ = adminDb.DB().ExecContext(context.Background(), fmt.Sprintf("DROP DATABASE `%s`", dbName))
		_ = adminDb.Close()
	}

	return dbClient, dbURL, cleanup
}
