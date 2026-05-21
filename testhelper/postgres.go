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

// MigratePostgresDatabase runs the same migrate.Up flow `ncps migrate
// up` uses in production: empty DB → ent.Schema.Create → final
// schema including the §10b surrogate-id columns on weak entities.
// Replaces the legacy dbmate invocation.
// The database URL should be in the format: postgresql://user:password@host:port/database
func MigratePostgresDatabase(t *testing.T, dbURL string) {
	t.Helper()

	dbClient, err := database.Open(dbURL, nil)
	require.NoError(t, err)

	defer dbClient.Close()

	sub, err := fs.Sub(migrations.FS, "postgres")
	require.NoError(t, err)

	require.NoError(t, migrate.Up(context.Background(), migrate.Options{
		DB:           dbClient.DB(),
		Dialect:      database.TypePostgreSQL,
		MigrationsFS: sub,
	}))
}

// SetupPostgres sets up a new temporary PostgreSQL database for testing.
// It requires the NCPS_TEST_ADMIN_POSTGRES_URL environment variable to
// be set. Returns the Ent-backed *database.Client, the database URL,
// and a cleanup function.
func SetupPostgres(t *testing.T) (*database.Client, string, func()) {
	t.Helper()

	adminDbURL := os.Getenv("NCPS_TEST_ADMIN_POSTGRES_URL")
	if adminDbURL == "" {
		t.Skip("Skipping Postgres test: NCPS_TEST_ADMIN_POSTGRES_URL not set")
	}

	adminDb, err := database.Open(adminDbURL, nil)
	require.NoError(t, err, "failed to connect to the postgres database")

	dbName := "test-" + MustRandString(58)
	_, err = adminDb.DB().ExecContext(context.Background(), "SELECT create_test_db($1);", dbName)
	require.NoError(t, err, "failed to create database %s", dbName)

	// Replace the test-db with the ephemeral database in the dbURL
	u, err := url.Parse(adminDbURL)
	require.NoError(t, err)

	u.Path = "/" + dbName
	dbURL := u.String()

	// Helper to recover from migration panic
	var errMigration error

	func() {
		defer func() {
			if r := recover(); r != nil {
				errMigration = fmt.Errorf("database migration panicked: %v", r) //nolint:err113
			}
		}()

		MigratePostgresDatabase(t, dbURL)
	}()

	if errMigration != nil {
		t.Fatalf("Failed to migrate PostgreSQL database: %v", errMigration)
	}

	dbClient, err := database.Open(dbURL, nil)
	require.NoError(t, err)

	cleanup := func() {
		_ = dbClient.Close()
		_, _ = adminDb.DB().ExecContext(context.Background(), "SELECT drop_test_db($1);", dbName)
		_ = adminDb.Close()
	}

	return dbClient, dbURL, cleanup
}
