package main_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Len(t, matches, 3, "expected exactly 3 .sql files (one per dialect); got %v", matches)

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

func buildGenerateMigrations(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "generate-migrations")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "go build ./cmd/generate-migrations failed:\n%s", string(out))

	return binary
}
