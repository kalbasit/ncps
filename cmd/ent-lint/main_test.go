package main_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEntLint runs the ent-lint binary against fixture directories under
// cmd/ent-lint/testdata/ and asserts the expected pass/fail outcome.
//
// Each fixture's name has the form "<invariant>_<good|bad>" — `_good`
// fixtures must produce only [PASS] lines and exit 0; `_bad` fixtures
// must produce at least one [FAIL] line for the named invariant and
// exit non-zero.
func TestEntLint(t *testing.T) {
	t.Parallel()

	binary := buildEntLint(t)

	cases := []struct {
		fixture    string
		invariant  string
		wantFail   bool
		wantInLine string
	}{
		{fixture: "a1_good", invariant: "A1", wantFail: false},
		{fixture: "a1_bad", invariant: "A1", wantFail: true, wantInLine: "entsql.Check"},
		{fixture: "a2_good", invariant: "A2", wantFail: false},
		{fixture: "a2_bad", invariant: "A2", wantFail: true, wantInLine: "OnDelete"},
		{fixture: "a4_good", invariant: "A4", wantFail: false},
		{fixture: "a4_bad", invariant: "A4", wantFail: true, wantInLine: "phantom FK"},
	}

	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join("testdata", tc.fixture)
			out, err := exec.CommandContext(t.Context(), binary, "--schema-dir="+dir).CombinedOutput()
			combined := string(out)

			if tc.wantFail {
				require.Errorf(t, err, "expected non-zero exit; output:\n%s", combined)
			} else {
				require.NoErrorf(t, err, "expected zero exit; output:\n%s", combined)
			}

			var (
				sawFail bool
				sawTok  bool
			)

			for _, line := range strings.Split(combined, "\n") {
				if !strings.HasPrefix(line, "[PASS]") && !strings.HasPrefix(line, "[FAIL]") {
					continue
				}

				if !strings.Contains(line, tc.invariant+" ") && !strings.HasSuffix(line, tc.invariant) {
					continue
				}

				if strings.HasPrefix(line, "[FAIL]") {
					sawFail = true
				}

				if tc.wantInLine != "" && strings.Contains(line, tc.wantInLine) {
					sawTok = true
				}
			}

			if tc.wantFail {
				assert.True(t, sawFail, "expected at least one [FAIL] %s line; output:\n%s", tc.invariant, combined)

				if tc.wantInLine != "" {
					assert.True(t, sawTok, "expected a [FAIL] line containing %q; output:\n%s", tc.wantInLine, combined)
				}
			} else {
				assert.False(t, sawFail, "expected no [FAIL] %s lines; output:\n%s", tc.invariant, combined)
			}
		})
	}
}

// TestEntLintRealSchemas runs the ent-lint binary against ncps's actual
// ent/schema/ tree. The project's schemas must be invariant-clean at all
// times; this test pins that contract.
func TestEntLintRealSchemas(t *testing.T) {
	t.Parallel()
	binary := buildEntLint(t)
	out, err := exec.CommandContext(t.Context(), binary, "--root=../..").CombinedOutput()
	require.NoErrorf(t, err, "ent-lint must pass on ncps's own schemas; output:\n%s", string(out))
}

func buildEntLint(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "ent-lint")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "go build ./cmd/ent-lint failed:\n%s", string(out))

	return binary
}
