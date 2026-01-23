package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePostgreSQLURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		dbURL    string
		expected string
		wantErr  bool
	}{
		{
			name:     "Standard TCP URL",
			dbURL:    "postgres://user:pass@localhost:5432/dbname?sslmode=disable",
			expected: "postgres://user:pass@localhost:5432/dbname?sslmode=disable",
			wantErr:  false,
		},
		{
			name:     "Postgres+unix specialized scheme",
			dbURL:    "postgres+unix:///var/run/postgresql/dbname",
			expected: "postgres:///dbname?host=%2Fvar%2Frun%2Fpostgresql",
			wantErr:  false,
		},
		{
			name:     "Postgresql+unix specialized scheme",
			dbURL:    "postgresql+unix:///var/run/postgresql/dbname",
			expected: "postgresql:///dbname?host=%2Fvar%2Frun%2Fpostgresql",
			wantErr:  false,
		},
		{
			name:    "Invalid postgres+unix - missing socket path",
			dbURL:   "postgres+unix:///dbname",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parsePostgreSQLURL(tt.dbURL)
			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestParseMySQLConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		dbURL          string
		expectedNet    string
		expectedAddr   string
		expectedDBName string
		wantErr        bool
	}{
		{
			name:           "Standard TCP URL",
			dbURL:          "mysql://user:pass@localhost:3306/dbname",
			expectedNet:    "tcp",
			expectedAddr:   "localhost:3306",
			expectedDBName: "dbname",
			wantErr:        false,
		},
		{
			name:           "MySQL+unix specialized scheme",
			dbURL:          "mysql+unix:///var/run/mysqld/mysqld.sock/dbname",
			expectedNet:    "unix",
			expectedAddr:   "/var/run/mysqld/mysqld.sock",
			expectedDBName: "dbname",
			wantErr:        false,
		},
		{
			name:           "MySQL with socket parameter",
			dbURL:          "mysql://user:pass@/dbname?socket=/var/run/mysqld/mysqld.sock",
			expectedNet:    "unix",
			expectedAddr:   "/var/run/mysqld/mysqld.sock",
			expectedDBName: "dbname",
			wantErr:        false,
		},
		{
			name:    "Invalid mysql+unix - missing socket path",
			dbURL:   "mysql+unix:///dbname",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := parseMySQLConfig(tt.dbURL)
			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedNet, cfg.Net)
			assert.Equal(t, tt.expectedAddr, cfg.Addr)
			assert.Equal(t, tt.expectedDBName, cfg.DBName)
		})
	}
}
