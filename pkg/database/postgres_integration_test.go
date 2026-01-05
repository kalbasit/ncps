package database_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/testhelper"
)

var errDBMigrationPanic = errors.New("database migration panicked")

//nolint:gochecknoglobals
var (
	postgresMigrationOnce sync.Once
	errPostgresMigration  error
)

// getTestPostgresDB returns a PostgreSQL database connection for testing
// or skips the test if NCPS_TEST_POSTGRES_URL is not set.
func getTestPostgresDB(t *testing.T) database.Querier {
	t.Helper()

	postgresURL := os.Getenv("NCPS_TEST_POSTGRES_URL")
	if postgresURL == "" {
		t.Skip("Skipping PostgreSQL integration test: NCPS_TEST_POSTGRES_URL environment variable not set")

		return nil
	}

	// Run migrations once for all tests
	postgresMigrationOnce.Do(func() {
		// The testhelper uses `require` which panics on failure. We must recover
		// from this to capture the error and prevent other tests from running
		// on a non-migrated database.
		defer func() {
			if r := recover(); r != nil {
				errPostgresMigration = fmt.Errorf("%w: %v", errDBMigrationPanic, r)
			}
		}()

		testhelper.MigratePostgresDatabase(t, postgresURL)
	})

	if errPostgresMigration != nil {
		t.Fatalf("Failed to migrate PostgreSQL database: %v", errPostgresMigration)
	}

	db, err := database.Open(postgresURL, nil)
	require.NoError(t, err)

	return db
}

func TestPostgreSQL_GetNarInfoByHash(t *testing.T) {
	t.Parallel()

	t.Run("narinfo not existing", func(t *testing.T) {
		t.Parallel()

		db := getTestPostgresDB(t)
		if db == nil {
			return
		}

		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		_, err = db.GetNarInfoByHash(context.Background(), hash)
		assert.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("narinfo existing", func(t *testing.T) {
		t.Parallel()

		db := getTestPostgresDB(t)
		if db == nil {
			return
		}

		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		ni1, err := db.CreateNarInfo(context.Background(), hash)
		require.NoError(t, err)

		// Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			db.DeleteNarInfoByID(context.Background(), ni1.ID)
		})

		ni2, err := db.GetNarInfoByHash(context.Background(), hash)
		require.NoError(t, err)

		assert.Equal(t, ni1.Hash, ni2.Hash)
	})
}

func TestPostgreSQL_CreateNarInfo(t *testing.T) {
	t.Parallel()

	t.Run("create narinfo successfully", func(t *testing.T) {
		t.Parallel()

		db := getTestPostgresDB(t)
		if db == nil {
			return
		}

		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		ni, err := db.CreateNarInfo(context.Background(), hash)
		require.NoError(t, err)

		// Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			db.DeleteNarInfoByID(context.Background(), ni.ID)
		})

		assert.NotZero(t, ni.ID)
		assert.Equal(t, hash, ni.Hash)
		assert.False(t, ni.CreatedAt.IsZero())
	})

	t.Run("create duplicate narinfo returns error", func(t *testing.T) {
		t.Parallel()

		db := getTestPostgresDB(t)
		if db == nil {
			return
		}

		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		ni1, err := db.CreateNarInfo(context.Background(), hash)
		require.NoError(t, err)

		// Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			db.DeleteNarInfoByID(context.Background(), ni1.ID)
		})

		// Try to create again with same hash
		_, err = db.CreateNarInfo(context.Background(), hash)
		require.Error(t, err)
		assert.True(t, database.IsDuplicateKeyError(err))
	})
}

func TestPostgreSQL_CreateNar(t *testing.T) {
	t.Parallel()

	t.Run("create nar successfully", func(t *testing.T) {
		t.Parallel()

		db := getTestPostgresDB(t)
		if db == nil {
			return
		}

		// Create narinfo first
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		ni, err := db.CreateNarInfo(context.Background(), hash)
		require.NoError(t, err)

		// Create nar_file
		narHash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		params := database.CreateNarFileParams{
			Hash:        narHash,
			Compression: "xz",
			FileSize:    1024,
			Query:       "/nix/store/test",
		}

		narFile, err := db.CreateNarFile(context.Background(), params)
		require.NoError(t, err)

		// Link narinfo to nar_file
		err = db.LinkNarInfoToNarFile(context.Background(), database.LinkNarInfoToNarFileParams{
			NarInfoID: ni.ID,
			NarFileID: narFile.ID,
		})
		require.NoError(t, err)

		// Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			db.DeleteNarFileByID(context.Background(), narFile.ID)
			//nolint:errcheck
			db.DeleteNarInfoByID(context.Background(), ni.ID)
		})

		assert.NotZero(t, narFile.ID)
		assert.Equal(t, narHash, narFile.Hash)
		assert.Equal(t, "xz", narFile.Compression)
		assert.EqualValues(t, 1024, narFile.FileSize)
		assert.False(t, narFile.CreatedAt.IsZero())
	})
}

func TestPostgreSQL_GetNarFileByHash(t *testing.T) {
	t.Parallel()

	t.Run("nar not existing", func(t *testing.T) {
		t.Parallel()

		db := getTestPostgresDB(t)
		if db == nil {
			return
		}

		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		_, err = db.GetNarFileByHash(context.Background(), hash)
		assert.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("nar existing", func(t *testing.T) {
		t.Parallel()

		db := getTestPostgresDB(t)
		if db == nil {
			return
		}

		// Create nar_file
		narHash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		params := database.CreateNarFileParams{
			Hash:        narHash,
			Compression: "xz",
			FileSize:    2048,
			Query:       "/nix/store/test",
		}

		narFile1, err := db.CreateNarFile(context.Background(), params)
		require.NoError(t, err)

		// Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			db.DeleteNarFileByID(context.Background(), narFile1.ID)
		})

		// Get nar_file
		narFile2, err := db.GetNarFileByHash(context.Background(), narHash)
		require.NoError(t, err)

		assert.Equal(t, narFile1.ID, narFile2.ID)
		assert.Equal(t, narFile1.Hash, narFile2.Hash)
		assert.Equal(t, narFile1.Compression, narFile2.Compression)
	})
}

func TestPostgreSQL_GetNarTotalSize(t *testing.T) {
	t.Parallel()

	t.Run("total size query works", func(t *testing.T) {
		t.Parallel()

		db := getTestPostgresDB(t)
		if db == nil {
			return
		}

		size, err := db.GetNarTotalSize(context.Background())
		require.NoError(t, err)
		// Since all tests share the same database, we can't assume it's empty
		// Just verify the query works and returns a non-negative value
		assert.GreaterOrEqual(t, size, int64(0))
	})

	t.Run("with nars", func(t *testing.T) {
		t.Parallel()

		db := getTestPostgresDB(t)
		if db == nil {
			return
		}

		// Create multiple nar_files
		var narFileIDs []int64

		totalSize := uint64(0)

		for i := 0; i < 3; i++ {
			// Create nar_file
			narHash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			//nolint:gosec
			fileSize := uint64((i + 1) * 1024)
			totalSize += fileSize

			params := database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    fileSize,
				Query:       "/nix/store/test",
			}

			narFile, err := db.CreateNarFile(context.Background(), params)
			require.NoError(t, err)

			narFileIDs = append(narFileIDs, narFile.ID)
		}

		// Clean up
		t.Cleanup(func() {
			for _, id := range narFileIDs {
				//nolint:errcheck
				db.DeleteNarFileByID(context.Background(), id)
			}
		})

		size, err := db.GetNarTotalSize(context.Background())
		require.NoError(t, err)
		//nolint:gosec
		assert.LessOrEqual(t, totalSize, uint64(size)) // Should be at least our nar_files
	})
}

func TestPostgreSQL_TouchNarInfo(t *testing.T) {
	t.Parallel()

	t.Run("touch narinfo", func(t *testing.T) {
		t.Parallel()

		db := getTestPostgresDB(t)
		if db == nil {
			return
		}

		// Create narinfo
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		ni, err := db.CreateNarInfo(context.Background(), hash)
		require.NoError(t, err)

		// Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			db.DeleteNarInfoByID(context.Background(), ni.ID)
		})

		// Touch narinfo
		rowsAffected, err := db.TouchNarInfo(context.Background(), hash)
		require.NoError(t, err)
		assert.EqualValues(t, 1, rowsAffected)

		// Verify it was updated
		ni2, err := db.GetNarInfoByHash(context.Background(), hash)
		require.NoError(t, err)

		assert.True(t, ni2.LastAccessedAt.Valid)
	})
}
