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
)

// MigrateMySQLDatabase will migrate the MySQL database using dbmate.
// The database URL should be in the format: mysql://user:password@host:port/database
func MigrateMySQLDatabase(t *testing.T, dbURL string) {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	dbMigrationsDir := filepath.Join(
		filepath.Dir(filepath.Dir(thisFile)),
		"db",
		"migrations",
		"mysql",
	)

	dbSchema := filepath.Join(
		filepath.Dir(filepath.Dir(thisFile)),
		"db",
		"schema",
		"mysql.sql",
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
	require.NoErrorf(t, err, "Running %q has failed", cmd.String())

	t.Logf("%s: %s", cmd.String(), output)
}

// SetupMySQL sets up a new temporary MySQL database for testing.
// It requires the NCPS_TEST_ADMIN_MYSQL_URL environment variable to be set.
// It returns a database connection and a cleanup function.
func SetupMySQL(t *testing.T) (database.Querier, string, func()) {
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

	cleanup := func() {
		_ = db.DB().Close()
		_, _ = adminDb.DB().ExecContext(context.Background(), fmt.Sprintf("DROP DATABASE `%s`", dbName))
		_ = adminDb.DB().Close()
	}

	return db, dbURL, cleanup
}
