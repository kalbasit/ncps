package database_test

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/testhelper"
)

//nolint:gochecknoglobals
var mysqlMigrationOnce sync.Once

// getTestMySQLDB returns a MySQL database connection for testing
// or skips the test if NCPS_TEST_MYSQL_URL is not set.
func getTestMySQLDB(t *testing.T) database.Querier {
	t.Helper()

	mysqlURL := os.Getenv("NCPS_TEST_MYSQL_URL")
	if mysqlURL == "" {
		t.Skip("Skipping MySQL integration test: NCPS_TEST_MYSQL_URL environment variable not set")

		return nil
	}

	// Run migrations once for all tests
	mysqlMigrationOnce.Do(func() {
		testhelper.MigrateMySQLDatabase(t, mysqlURL)
	})

	db, err := database.Open(mysqlURL, nil)
	require.NoError(t, err)

	return db
}

func TestMySQL_GetNarInfoByHash(t *testing.T) {
	t.Parallel()

	t.Run("narinfo not existing", func(t *testing.T) {
		t.Parallel()

		db := getTestMySQLDB(t)
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

		db := getTestMySQLDB(t)
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

func TestMySQL_CreateNarInfo(t *testing.T) {
	t.Parallel()

	t.Run("create narinfo successfully", func(t *testing.T) {
		t.Parallel()

		db := getTestMySQLDB(t)
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

		db := getTestMySQLDB(t)
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

func TestMySQL_CreateNar(t *testing.T) {
	t.Parallel()

	t.Run("create nar successfully", func(t *testing.T) {
		t.Parallel()

		db := getTestMySQLDB(t)
		if db == nil {
			return
		}

		// Create narinfo first
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		ni, err := db.CreateNarInfo(context.Background(), hash)
		require.NoError(t, err)

		// Create nar
		narHash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		params := database.CreateNarParams{
			NarInfoID:   ni.ID,
			Hash:        narHash,
			Compression: "xz",
			FileSize:    1024,
			Query:       "/nix/store/test",
		}

		nar, err := db.CreateNar(context.Background(), params)
		require.NoError(t, err)

		// Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			db.DeleteNarByID(context.Background(), nar.ID)
			//nolint:errcheck
			db.DeleteNarInfoByID(context.Background(), ni.ID)
		})

		assert.NotZero(t, nar.ID)
		assert.Equal(t, ni.ID, nar.NarInfoID)
		assert.Equal(t, narHash, nar.Hash)
		assert.Equal(t, "xz", nar.Compression)
		assert.EqualValues(t, 1024, nar.FileSize)
		assert.False(t, nar.CreatedAt.IsZero())
	})
}

func TestMySQL_GetNarByHash(t *testing.T) {
	t.Parallel()

	t.Run("nar not existing", func(t *testing.T) {
		t.Parallel()

		db := getTestMySQLDB(t)
		if db == nil {
			return
		}

		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		_, err = db.GetNarByHash(context.Background(), hash)
		assert.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("nar existing", func(t *testing.T) {
		t.Parallel()

		db := getTestMySQLDB(t)
		if db == nil {
			return
		}

		// Create narinfo first
		niHash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		ni, err := db.CreateNarInfo(context.Background(), niHash)
		require.NoError(t, err)

		// Create nar
		narHash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		params := database.CreateNarParams{
			NarInfoID:   ni.ID,
			Hash:        narHash,
			Compression: "xz",
			FileSize:    2048,
			Query:       "/nix/store/test",
		}

		nar1, err := db.CreateNar(context.Background(), params)
		require.NoError(t, err)

		// Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			db.DeleteNarByID(context.Background(), nar1.ID)
			//nolint:errcheck
			db.DeleteNarInfoByID(context.Background(), ni.ID)
		})

		// Get nar
		nar2, err := db.GetNarByHash(context.Background(), narHash)
		require.NoError(t, err)

		assert.Equal(t, nar1.ID, nar2.ID)
		assert.Equal(t, nar1.Hash, nar2.Hash)
		assert.Equal(t, nar1.Compression, nar2.Compression)
	})
}

func TestMySQL_GetNarTotalSize(t *testing.T) {
	t.Parallel()

	t.Run("total size query works", func(t *testing.T) {
		t.Parallel()

		db := getTestMySQLDB(t)
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

		db := getTestMySQLDB(t)
		if db == nil {
			return
		}

		initialSize, err := db.GetNarTotalSize(context.Background())
		require.NoError(t, err)

		// Create multiple nars
		var narIDs []int64

		var niIDs []int64

		totalSize := uint64(0)

		for i := 0; i < 3; i++ {
			// Create narinfo
			niHash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			ni, err := db.CreateNarInfo(context.Background(), niHash)
			require.NoError(t, err)

			niIDs = append(niIDs, ni.ID)

			// Create nar
			narHash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			//nolint:gosec
			fileSize := uint64((i + 1) * 1024)
			totalSize += fileSize

			params := database.CreateNarParams{
				NarInfoID:   ni.ID,
				Hash:        narHash,
				Compression: "xz",
				FileSize:    fileSize,
				Query:       "/nix/store/test",
			}

			nar, err := db.CreateNar(context.Background(), params)
			require.NoError(t, err)

			narIDs = append(narIDs, nar.ID)
		}

		// Clean up
		t.Cleanup(func() {
			for _, id := range narIDs {
				//nolint:errcheck
				db.DeleteNarByID(context.Background(), id)
			}

			for _, id := range niIDs {
				//nolint:errcheck
				db.DeleteNarInfoByID(context.Background(), id)
			}
		})

		finalSize, err := db.GetNarTotalSize(context.Background())
		require.NoError(t, err)
		//nolint:gosec
		assert.Equal(t, int64(totalSize), finalSize-initialSize)
	})
}

func TestMySQL_TouchNarInfo(t *testing.T) {
	t.Parallel()

	t.Run("touch narinfo", func(t *testing.T) {
		t.Parallel()

		db := getTestMySQLDB(t)
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
