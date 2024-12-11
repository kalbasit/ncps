package testhelper

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	_, b, _, _ = runtime.Caller(0)
	basePath   = filepath.Dir(b)
)

func CreateMigrateDatabase(t *testing.T, dbFile string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(dbFile), 0o700))

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	dbMigrationsDir := filepath.Join(
		filepath.Dir(filepath.Dir(thisFile)),
		"db",
		"migrations",
	)

	cmd := exec.Command(
		"dbmate",
		"--url=sqlite:"+dbFile,
		"--migrations-dir="+dbMigrationsDir,
		"migrate",
		"up",
	)

	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "Running %q has failed", cmd.String())

	t.Logf("%s: %s", cmd.String(), output)
}
