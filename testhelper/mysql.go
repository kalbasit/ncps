package testhelper

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
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
