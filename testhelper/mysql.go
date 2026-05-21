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

	db, err := database.Open(dbURL, nil)
	require.NoError(t, err)

	defer db.DB().Close()

	sub, err := fs.Sub(migrations.FS, "mysql")
	require.NoError(t, err)

	require.NoError(t, migrate.Up(context.Background(), migrate.Options{
		DB:           db.DB(),
		Dialect:      database.TypeMySQL,
		MigrationsFS: sub,
	}))
}

// SetupMySQL sets up a new temporary MySQL database for testing.
// It requires the NCPS_TEST_ADMIN_MYSQL_URL environment variable to be set.
// It returns the legacy Querier (still in use during §11.2-§11.7),
// the §11-introduced Ent-backed *database.Client, the database URL,
// and a cleanup function.
func SetupMySQL(t *testing.T) (database.Querier, *database.Client, string, func()) {
	t.Helper()

	adminDbURL := os.Getenv("NCPS_TEST_ADMIN_MYSQL_URL")
	if adminDbURL == "" {
		t.Skip("Skipping MySQL test: NCPS_TEST_ADMIN_MYSQL_URL not set")
	}

	adminDb, err := database.Open(adminDbURL, nil)
	require.NoError(t, err, "failed to connect to the mysql database")

	dbName := "test-" + MustRandString(58)

	// MySQL CREATE DATABASE
	_, err = adminDb.DB().ExecContext(context.Background(), fmt.Sprintf("CREATE DATABASE `%s`", dbName))
	require.NoError(t, err, "failed to create database %s", dbName)

	// Replace the database name in the URL
	u, err := url.Parse(adminDbURL)
	require.NoError(t, err)

	u.Path = "/" + dbName
	dbURL := u.String()

	// Helper to recover from migration panic
	var errMigration error

	// We can't defer the check here easily because t.Fatalf stops the test.
	// But MigrateMySQLDatabase might panic? The original code had a defer recover block.
	// Let's keep it safe.
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

	db, err := database.Open(dbURL, nil)
	require.NoError(t, err)

	dbClient, err := database.NewClient(db.DB(), database.TypeMySQL)
	require.NoError(t, err)

	cleanup := func() {
		_ = db.DB().Close()
		_, _ = adminDb.DB().ExecContext(context.Background(), fmt.Sprintf("DROP DATABASE `%s`", dbName))
		_ = adminDb.DB().Close()
	}

	return db, dbClient, dbURL, cleanup
}
