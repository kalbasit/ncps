package testhelper

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
)

// MigratePostgresDatabase will migrate the PostgreSQL database using dbmate.
// The database URL should be in the format: postgresql://user:password@host:port/database
func MigratePostgresDatabase(t *testing.T, dbURL string) {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	dbMigrationsDir := filepath.Join(
		filepath.Dir(filepath.Dir(thisFile)),
		"db",
		"migrations",
		"postgres",
	)

	dbSchema := filepath.Join(
		filepath.Dir(filepath.Dir(thisFile)),
		"db",
		"schema",
		"postgres.sql",
	)

	//nolint:gosec
	cmd := exec.CommandContext(context.Background(),
		"dbmate",
		"--no-dump-schema",
		"--url="+dbURL,
		"--migrations-dir="+dbMigrationsDir,
		"--schema-file="+dbSchema,
		"up",
	)

	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "Running %q has failed. Output:\n%s", cmd.String(), string(output))

	t.Logf("%s: %s", cmd.String(), output)
}

// SetupPostgres sets up a new temporary PostgreSQL database for testing.
// It requires the NCPS_TEST_ADMIN_POSTGRES_URL environment variable to be set.
// It returns a database connection and a cleanup function.
func SetupPostgres(t *testing.T) (database.Querier, func()) {
	t.Helper()

	adminDbURL := os.Getenv("NCPS_TEST_ADMIN_POSTGRES_URL")
	if adminDbURL == "" {
		t.Skip("Skipping Postgres test: NCPS_TEST_ADMIN_POSTGRES_URL not set")
	}

	adminDb, err := database.Open(adminDbURL, nil)
	require.NoError(t, err, "failed to connect to the postgres database")

	dbName := "test-" + helper.MustRandString(58, nil)
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

	db, err := database.Open(dbURL, nil)
	require.NoError(t, err)

	cleanup := func() {
		_ = db.DB().Close()
		_, _ = adminDb.DB().ExecContext(context.Background(), "SELECT drop_test_db($1);", dbName)
		_ = adminDb.DB().Close()
	}

	return db, cleanup
}
