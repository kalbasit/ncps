package main_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sharedBinary is built once for the whole package via TestMain so each test
// reuses the same executable. Building it per-test cost ~3-4s × N.
//
//nolint:gochecknoglobals // intentional package-level state for one-shot binary build
var (
	sharedBinary    string
	errSharedBuild  error
	sharedBuildOnce sync.Once
	sharedBuildOut  string
	sharedBinaryDir string
)

// TestSQLOnlyEmitsThreeDialects verifies the --sql-only path produces
// one empty goose stub per dialect under a single shared timestamp prefix.
func TestSQLOnlyEmitsThreeDialects(t *testing.T) {
	t.Parallel()

	binary := buildGenerateMigrations(t)
	root := t.TempDir()

	out, err := exec.CommandContext(t.Context(), binary,
		"--sql-only", "--name=test_backfill", "--root="+root).
		CombinedOutput()
	require.NoErrorf(t, err, "stdout:\n%s", string(out))

	matches, err := filepath.Glob(filepath.Join(root, "migrations", "*", "*.sql"))
	require.NoError(t, err)
	require.Len(t, matches, 3, "expected exactly 3 .sql files (one per dialect); got %v", matches)

	// All three files should share the same timestamp prefix.
	stamps := make([]string, 0, len(matches))

	for _, m := range matches {
		base := filepath.Base(m)
		// timestamp is the leading 14-char run before the first underscore.
		stamps = append(stamps, base[:14])
	}

	assert.Equal(t, stamps[0], stamps[1], "timestamp prefixes must match across dialects")
	assert.Equal(t, stamps[1], stamps[2], "timestamp prefixes must match across dialects")
}

// TestNameValidation pins the placeholder-name rejection contract.
func TestNameValidation(t *testing.T) {
	t.Parallel()

	binary := buildGenerateMigrations(t)
	root := t.TempDir()

	cases := []struct {
		name     string
		wantFail bool
	}{
		{name: "auto", wantFail: true},
		{name: "wip", wantFail: true},
		{name: "tmp", wantFail: true},
		{name: "todo", wantFail: true},
		{name: "temp", wantFail: true},
		{name: "test", wantFail: true},
		{name: "", wantFail: true},
		{name: "quick fix", wantFail: true},
		{name: "add_widget_count", wantFail: false},
		{name: "backfill_orphan_chunks", wantFail: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := exec.CommandContext(t.Context(), binary,
				"--sql-only", "--name="+tc.name, "--root="+filepath.Join(root, tc.name+"-out")).
				CombinedOutput()
			if tc.wantFail {
				require.Errorf(t, err, "expected non-zero exit for name=%q; output:\n%s", tc.name, string(out))
			} else {
				require.NoErrorf(t, err, "expected zero exit for name=%q; output:\n%s", tc.name, string(out))
			}
		})
	}
}

// buildGenerateMigrations returns the path to a built copy of the binary.
//
// If `NCPS_TEST_GENERATE_MIGRATIONS_BIN` is set (Nix passes it in cohort
// checkPhase), use that pre-built binary directly — skip the in-test
// `go build`, which on slow CI runners (aarch64 ubuntu-24.04 in particular)
// can take >180 s on this package thanks to Atlas + Ent's heavy imports.
//
// Without the env var (local `go test`), fall back to `go build`. Built
// once per package run via sync.Once.
func buildGenerateMigrations(t *testing.T) string {
	t.Helper()

	if bin := os.Getenv("NCPS_TEST_GENERATE_MIGRATIONS_BIN"); bin != "" {
		return bin
	}

	sharedBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "generate-migrations-bin-*")
		if err != nil {
			errSharedBuild = err

			return
		}

		sharedBinaryDir = dir
		sharedBinary = filepath.Join(dir, "generate-migrations")

		// 180s for local dev; CI cohorts use NCPS_TEST_GENERATE_MIGRATIONS_BIN
		// to skip this build entirely.
		buildCtx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		defer cancel()

		cmd := exec.CommandContext(buildCtx, "go", "build", "-o", sharedBinary, ".")
		cmd.Dir = "."

		out, err := cmd.CombinedOutput()
		if err != nil {
			errSharedBuild = err
			sharedBuildOut = string(out)
		}
	})
	require.NoErrorf(t, errSharedBuild, "go build ./cmd/generate-migrations failed:\n%s", sharedBuildOut)

	return sharedBinary
}

func TestMain(m *testing.M) {
	code := m.Run()

	if sharedBinaryDir != "" {
		_ = os.RemoveAll(sharedBinaryDir)
	}

	os.Exit(code)
}
