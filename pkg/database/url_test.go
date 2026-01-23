package database_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
)

func TestDetectFromDatabaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url      string
		expected database.Type
		wantErr  bool
	}{
		{"mysql://user:pass@localhost/db", database.TypeMySQL, false},
		{"mysql+unix:///tmp/mysql.sock/db", database.TypeMySQL, false},
		{"postgres://user:pass@localhost/db", database.TypePostgreSQL, false},
		{"postgresql://user:pass@localhost/db", database.TypePostgreSQL, false},
		{"postgres+unix:///tmp/pg.sock/db", database.TypePostgreSQL, false},
		{"postgresql+unix:///tmp/pg.sock/db", database.TypePostgreSQL, false},
		{"sqlite:///tmp/db.sqlite", database.TypeSQLite, false},
		{"sqlite3:///tmp/db.sqlite", database.TypeSQLite, false},
		// Invalid URLs
		{"invalid-url", database.TypeUnknown, true},
		{"unknown://localhost/db", database.TypeUnknown, true},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			t.Parallel()

			got, err := database.DetectFromDatabaseURL(tt.url)
			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, tt.expected, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}
