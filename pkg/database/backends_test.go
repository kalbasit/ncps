package database_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/testhelper"
)

func TestBackends(t *testing.T) {
	t.Parallel()

	backends := []struct {
		name   string
		envVar string
		setup  querierFactory
	}{
		{
			name: "SQLite",
			setup: func(t *testing.T) database.Querier {
				t.Helper()

				dir, err := os.MkdirTemp("", "database-path-")
				require.NoError(t, err)

				dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
				testhelper.CreateMigrateDatabase(t, dbFile)

				db, err := database.Open("sqlite:"+dbFile, nil)
				require.NoError(t, err)

				t.Cleanup(func() {
					db.DB().Close()
					os.RemoveAll(dir)
				})

				return db
			},
		},

		{
			name:   "PostgreSQL",
			envVar: "NCPS_TEST_ADMIN_POSTGRES_URL",
			setup: func(t *testing.T) database.Querier {
				t.Helper()

				db, cleanup := testhelper.SetupPostgres(t)
				t.Cleanup(cleanup)

				return db
			},
		},

		{
			name:   "MySQL",
			envVar: "NCPS_TEST_ADMIN_MYSQL_URL",
			setup: func(t *testing.T) database.Querier {
				t.Helper()

				db, cleanup := testhelper.SetupMySQL(t)
				t.Cleanup(cleanup)

				return db
			},
		},
	}

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			// Skip logic
			if b.envVar != "" && os.Getenv(b.envVar) == "" {
				t.Skipf("Skipping %s: %s not set", b.name, b.envVar)
			}

			// Run the unified suite
			runComplianceSuite(t, b.setup)
		})
	}
}
