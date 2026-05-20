package migrations_test

import (
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/migrations"
)

// TestEmbedFS asserts the embed.FS exposes the expected per-dialect
// migration files. It is a smoke test that prevents a future change from
// silently dropping the //go:embed pattern.
func TestEmbedFS(t *testing.T) {
	t.Parallel()

	cases := []struct {
		dialect string
		minimum int // minimum number of .sql files expected
	}{
		{dialect: "sqlite", minimum: 14},
		{dialect: "postgres", minimum: 8},
		{dialect: "mysql", minimum: 8},
	}

	for _, tc := range cases {
		t.Run(tc.dialect, func(t *testing.T) {
			t.Parallel()

			sub, err := fs.Sub(migrations.FS, tc.dialect)
			require.NoError(t, err)

			var files []string

			err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}

				if !d.IsDir() && strings.HasSuffix(p, ".sql") {
					files = append(files, p)
				}

				return nil
			})
			require.NoError(t, err)
			sort.Strings(files)

			assert.GreaterOrEqualf(t, len(files), tc.minimum,
				"%s migrations: expected at least %d files, got %d (%v)",
				tc.dialect, tc.minimum, len(files), files)

			// Every file MUST contain the goose Up/Down markers — never the
			// dbmate-era markers — confirming the §7 translation is complete.
			for _, f := range files {
				body, err := fs.ReadFile(sub, f)
				require.NoError(t, err)

				s := string(body)
				assert.Containsf(t, s, "-- +goose Up", "%s/%s missing goose Up marker", tc.dialect, f)
				assert.NotContainsf(t, s, "-- migrate:up", "%s/%s still has dbmate up marker", tc.dialect, f)
				assert.NotContainsf(t, s, "-- migrate:down", "%s/%s still has dbmate down marker", tc.dialect, f)
			}
		})
	}
}
