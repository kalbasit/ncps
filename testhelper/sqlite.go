package testhelper

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// CreateMigrateDatabase will create all necessary directories, and will create
// the sqlite3 database (if necessary) and migrate it.
func CreateMigrateDatabase(t *testing.T, dbFile string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(dbFile), 0o700))

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	dbMigrationsDir := filepath.Join(
		filepath.Dir(filepath.Dir(thisFile)),
		"db",
		"migrations",
		"sqlite",
	)

	dbSchema := filepath.Join(
		filepath.Dir(filepath.Dir(thisFile)),
		"db",
		"schema",
		"sqlite.sql",
	)

	//nolint:gosec
	cmd := exec.CommandContext(context.Background(),
		"dbmate",
		"--no-dump-schema",
		"--url=sqlite:"+dbFile,
		"--migrations-dir="+dbMigrationsDir,
		"--schema-file="+dbSchema,
		"up",
	)

	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "Running %q has failed", cmd.String())

	t.Logf("%s: %s", cmd.String(), output)
}
