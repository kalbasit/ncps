package database_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

func setupDatabase(t *testing.T) (database.Querier, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "database-path-")
	require.NoError(t, err)

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	cleanup := func() {
		db.DB().Close()
		os.RemoveAll(dir)
	}

	return db, cleanup
}

func TestGetNarInfoByHash(t *testing.T) {
	t.Parallel()

	t.Run("narinfo not existing", func(t *testing.T) {
		t.Parallel()

		db, cleanup := setupDatabase(t)
		defer cleanup()

		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		_, err = db.GetNarInfoByHash(context.Background(), hash)
		assert.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("narinfo existing", func(t *testing.T) {
		t.Parallel()

		db, cleanup := setupDatabase(t)
		defer cleanup()

		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		ni1, err := db.CreateNarInfo(context.Background(), hash)
		require.NoError(t, err)

		ni2, err := db.GetNarInfoByHash(context.Background(), hash)
		require.NoError(t, err)

		assert.Equal(t, ni1.Hash, ni2.Hash)
	})
}

//nolint:paralleltest
func TestInsertNarInfo(t *testing.T) {
	db, cleanup := setupDatabase(t)
	defer cleanup()

	t.Run("inserting one record", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		nio, err := db.CreateNarInfo(context.Background(), hash)
		require.NoError(t, err)

		rows, err := db.DB().QueryContext(
			context.Background(),
			"SELECT id, hash, created_at, updated_at, last_accessed_at FROM narinfos",
		)
		require.NoError(t, err)

		defer rows.Close()

		nims := make([]database.NarInfo, 0)

		for rows.Next() {
			var nim database.NarInfo

			err := rows.Scan(&nim.ID, &nim.Hash, &nim.CreatedAt, &nim.UpdatedAt, &nim.LastAccessedAt)
			require.NoError(t, err)

			nims = append(nims, nim)
		}

		require.NoError(t, rows.Err())

		require.NoError(t, err)

		if assert.Len(t, nims, 1) {
			assert.Equal(t, nio.ID, nims[0].ID)
			assert.Equal(t, hash, nims[0].Hash)
			assert.Less(t, time.Since(nims[0].CreatedAt), 3*time.Second)
			assert.False(t, nims[0].UpdatedAt.Valid)
			assert.Equal(t, nims[0].CreatedAt, nims[0].LastAccessedAt.Time)
		}
	})

	t.Run("hash is unique", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		_, err = db.CreateNarInfo(context.Background(), hash)
		require.NoError(t, err)

		_, err = db.CreateNarInfo(context.Background(), hash)
		assert.True(t, database.IsDuplicateKeyError(err))
	})

	t.Run("can write many narinfos", func(t *testing.T) {
		var wg sync.WaitGroup

		errC := make(chan error)

		for i := 0; i < 10000; i++ {
			wg.Add(1)

			go func() {
				defer wg.Done()

				hash, err := helper.RandString(128, nil)
				if err != nil {
					errC <- fmt.Errorf("expected no error but got: %w", err)

					return
				}

				if _, err := db.CreateNarInfo(context.Background(), hash); err != nil {
					errC <- fmt.Errorf("error creating the narinfo record: %w", err)
				}
			}()
		}

		done := make(chan struct{})

		go func() {
			wg.Wait()

			close(done)
		}()

		for {
			select {
			case err := <-errC:
				assert.NoError(t, err)
			case <-done:
				return
			}
		}
	})
}

//nolint:paralleltest
func TestTouchNarInfo(t *testing.T) {
	db, cleanup := setupDatabase(t)
	defer cleanup()

	t.Run("narinfo not existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		ra, err := db.TouchNarInfo(context.Background(), hash)
		require.NoError(t, err)

		assert.Zero(t, ra)
	})

	t.Run("narinfo existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		_, err = db.CreateNarInfo(context.Background(), hash)
		require.NoError(t, err)

		t.Run("confirm created_at == last_accessed_at, and no updated_at", func(t *testing.T) {
			rows, err := db.DB().QueryContext(
				context.Background(),
				"SELECT id, hash, created_at, updated_at, last_accessed_at FROM narinfos",
			)
			require.NoError(t, err)

			defer rows.Close()

			nims := make([]database.NarInfo, 0)

			for rows.Next() {
				var nim database.NarInfo

				err := rows.Scan(&nim.ID, &nim.Hash, &nim.CreatedAt, &nim.UpdatedAt, &nim.LastAccessedAt)
				require.NoError(t, err)

				nims = append(nims, nim)
			}

			require.NoError(t, rows.Err())

			assert.Len(t, nims, 1)
			assert.Equal(t, nims[0].CreatedAt, nims[0].LastAccessedAt.Time)
			assert.False(t, nims[0].UpdatedAt.Valid)
		})

		t.Run("touch the narinfo", func(t *testing.T) {
			time.Sleep(time.Second)

			ra, err := db.TouchNarInfo(context.Background(), hash)
			require.NoError(t, err)
			assert.EqualValues(t, 1, ra)
		})

		t.Run("confirm created_at != last_accessed_at and updated_at == last_accessed_at", func(t *testing.T) {
			rows, err := db.DB().QueryContext(
				context.Background(),
				"SELECT id, hash, created_at, updated_at, last_accessed_at FROM narinfos",
			)
			require.NoError(t, err)

			defer rows.Close()

			nims := make([]database.NarInfo, 0)

			for rows.Next() {
				var nim database.NarInfo

				err := rows.Scan(&nim.ID, &nim.Hash, &nim.CreatedAt, &nim.UpdatedAt, &nim.LastAccessedAt)
				require.NoError(t, err)

				nims = append(nims, nim)
			}

			require.NoError(t, rows.Err())
			assert.Len(t, nims, 1)

			assert.NotEqual(t, nims[0].CreatedAt, nims[0].LastAccessedAt)

			if assert.True(t, nims[0].UpdatedAt.Valid) {
				assert.Equal(t, nims[0].UpdatedAt.Time, nims[0].LastAccessedAt.Time)
			}
		})
	})
}

//nolint:paralleltest
func TestDeleteNarInfo(t *testing.T) {
	db, cleanup := setupDatabase(t)
	defer cleanup()

	t.Run("narinfo not existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		ra, err := db.DeleteNarInfoByHash(context.Background(), hash)
		require.NoError(t, err)

		assert.Zero(t, ra)
	})

	t.Run("narinfo existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		t.Run("create the narinfo", func(t *testing.T) {
			_, err = db.CreateNarInfo(context.Background(), hash)
			require.NoError(t, err)
		})

		t.Run("delete the narinfo", func(t *testing.T) {
			time.Sleep(time.Second)

			ra, err := db.DeleteNarInfoByHash(context.Background(), hash)
			require.NoError(t, err)

			assert.EqualValues(t, 1, ra)
		})

		t.Run("confirm it has been removed", func(t *testing.T) {
			rows, err := db.DB().QueryContext(
				context.Background(),
				"SELECT id, hash, created_at, updated_at, last_accessed_at FROM narinfos",
			)
			require.NoError(t, err)

			defer rows.Close()

			nims := make([]database.NarInfo, 0)

			for rows.Next() {
				var nim database.NarInfo

				err := rows.Scan(&nim.ID, &nim.Hash, &nim.CreatedAt, &nim.UpdatedAt, &nim.LastAccessedAt)
				require.NoError(t, err)

				nims = append(nims, nim)
			}

			require.NoError(t, rows.Err())
			assert.Empty(t, nims)
		})
	})
}

func TestGetNarFileByHash(t *testing.T) {
	t.Parallel()

	t.Run("nar not existing", func(t *testing.T) {
		t.Parallel()

		db, cleanup := setupDatabase(t)
		defer cleanup()

		narInfoHash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		_, err = db.CreateNarInfo(context.Background(), narInfoHash)
		require.NoError(t, err)

		narHash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		_, err = db.GetNarFileByHash(context.Background(), narHash)
		assert.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("nar existing", func(t *testing.T) {
		t.Parallel()

		db, cleanup := setupDatabase(t)
		defer cleanup()

		narHash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		nf1, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
			Hash:        narHash,
			Compression: nar.CompressionTypeXz.String(),
			Query:       "hash=123&key=value",
			FileSize:    123,
		})
		require.NoError(t, err)

		nf2, err := db.GetNarFileByHash(context.Background(), narHash)
		require.NoError(t, err)

		assert.Equal(t, nf1.Hash, nf2.Hash)
		assert.Equal(t, nf1.Compression, nf2.Compression)
		assert.Equal(t, nf1.FileSize, nf2.FileSize)
	})
}

//nolint:paralleltest
func TestInsertNar(t *testing.T) {
	db, cleanup := setupDatabase(t)
	defer cleanup()

	allCompressions := []nar.CompressionType{
		nar.CompressionTypeNone,
		nar.CompressionTypeBzip2,
		nar.CompressionTypeZstd,
		nar.CompressionTypeLzip,
		nar.CompressionTypeLz4,
		nar.CompressionTypeBr,
		nar.CompressionTypeXz,
	}

	for _, compression := range allCompressions {
		t.Run(fmt.Sprintf("compression=%q", compression), func(t *testing.T) {
			_, err := db.DB().ExecContext(context.Background(), "DELETE FROM nar_files")
			require.NoError(t, err)

			t.Run("inserting one record", func(t *testing.T) {
				hash, err := helper.RandString(32, nil)
				require.NoError(t, err)

				narFile, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
					Hash:        hash,
					Compression: compression.String(),
					FileSize:    123,
				})
				require.NoError(t, err)

				const query = `
 				SELECT id, hash, compression, file_size, created_at, updated_at, last_accessed_at, query
 				FROM nar_files
 				`

				rows, err := db.DB().QueryContext(context.Background(), query)
				require.NoError(t, err)

				defer rows.Close()

				narFiles := make([]database.NarFile, 0)

				for rows.Next() {
					var nf database.NarFile

					err := rows.Scan(
						&nf.ID,
						&nf.Hash,
						&nf.Compression,
						&nf.FileSize,
						&nf.CreatedAt,
						&nf.UpdatedAt,
						&nf.LastAccessedAt,
						&nf.Query,
					)
					require.NoError(t, err)

					narFiles = append(narFiles, nf)
				}

				require.NoError(t, rows.Err())

				if assert.Len(t, narFiles, 1) {
					assert.Equal(t, narFile.ID, narFiles[0].ID)
					assert.Equal(t, hash, narFiles[0].Hash)
					assert.Equal(t, compression.String(), narFiles[0].Compression)
					assert.EqualValues(t, 123, narFiles[0].FileSize)
					assert.Less(t, time.Since(narFiles[0].CreatedAt), 3*time.Second)
					assert.False(t, narFiles[0].UpdatedAt.Valid)
					assert.Equal(t, narFiles[0].CreatedAt, narFiles[0].LastAccessedAt.Time)
				}
			})

			t.Run("hash is unique", func(t *testing.T) {
				hash, err := helper.RandString(32, nil)
				require.NoError(t, err)

				_, err = db.CreateNarFile(context.Background(), database.CreateNarFileParams{
					Hash:        hash,
					Compression: "",
					FileSize:    123,
				})
				require.NoError(t, err)

				_, err = db.CreateNarFile(context.Background(), database.CreateNarFileParams{
					Hash:        hash,
					Compression: "",
					FileSize:    123,
				})

				assert.True(t, database.IsDuplicateKeyError(err))
			})
		})
	}
}

//nolint:paralleltest
func TestTouchNarFile(t *testing.T) {
	db, cleanup := setupDatabase(t)
	defer cleanup()

	t.Run("nar not existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		ra, err := db.TouchNarFile(context.Background(), hash)
		require.NoError(t, err)

		assert.Zero(t, ra)
	})

	t.Run("nar existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		t.Run("create the nar", func(t *testing.T) {
			_, err = db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash,
				Compression: "",
				FileSize:    123,
			})
			require.NoError(t, err)
		})

		t.Run("confirm created_at == last_accessed_at, and no updated_at", func(t *testing.T) {
			const query = `
 				SELECT id, hash, compression, file_size, created_at, updated_at, last_accessed_at, query
 				FROM nar_files
 				`

			rows, err := db.DB().QueryContext(context.Background(), query)
			require.NoError(t, err)

			defer rows.Close()

			narFiles := make([]database.NarFile, 0)

			for rows.Next() {
				var nf database.NarFile

				err := rows.Scan(
					&nf.ID,
					&nf.Hash,
					&nf.Compression,
					&nf.FileSize,
					&nf.CreatedAt,
					&nf.UpdatedAt,
					&nf.LastAccessedAt,
					&nf.Query,
				)
				require.NoError(t, err)

				narFiles = append(narFiles, nf)
			}

			require.NoError(t, rows.Err())

			if assert.Len(t, narFiles, 1) {
				assert.False(t, narFiles[0].UpdatedAt.Valid)
				assert.Equal(t, narFiles[0].CreatedAt, narFiles[0].LastAccessedAt.Time)
			}
		})

		t.Run("touch the nar", func(t *testing.T) {
			time.Sleep(time.Second)

			ra, err := db.TouchNarFile(context.Background(), hash)
			require.NoError(t, err)

			assert.EqualValues(t, 1, ra)
		})

		t.Run("confirm created_at != last_accessed_at and updated_at == last_accessed_at", func(t *testing.T) {
			const query = `
 				SELECT id, hash, compression, file_size, created_at, updated_at, last_accessed_at, query
 				FROM nar_files
 				`

			rows, err := db.DB().QueryContext(context.Background(), query)
			require.NoError(t, err)

			defer rows.Close()

			narFiles := make([]database.NarFile, 0)

			for rows.Next() {
				var nf database.NarFile

				err := rows.Scan(
					&nf.ID,
					&nf.Hash,
					&nf.Compression,
					&nf.FileSize,
					&nf.CreatedAt,
					&nf.UpdatedAt,
					&nf.LastAccessedAt,
					&nf.Query,
				)
				require.NoError(t, err)

				narFiles = append(narFiles, nf)
			}

			require.NoError(t, rows.Err())

			if assert.Len(t, narFiles, 1) {
				assert.NotEqual(t, narFiles[0].CreatedAt, narFiles[0].LastAccessedAt)

				if assert.True(t, narFiles[0].UpdatedAt.Valid) {
					assert.Equal(t, narFiles[0].UpdatedAt.Time, narFiles[0].LastAccessedAt.Time)
				}
			}
		})
	})
}

//nolint:paralleltest
func TestDeleteNar(t *testing.T) {
	db, cleanup := setupDatabase(t)
	defer cleanup()

	t.Run("nar not existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		ra, err := db.DeleteNarFileByHash(context.Background(), hash)
		require.NoError(t, err)

		assert.Zero(t, ra)
	})

	t.Run("nar existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		t.Run("create the nar", func(t *testing.T) {
			_, err = db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash,
				Compression: "",
				FileSize:    123,
			})
			require.NoError(t, err)
		})

		t.Run("delete the narinfo", func(t *testing.T) {
			time.Sleep(time.Second)

			ra, err := db.DeleteNarFileByHash(context.Background(), hash)
			require.NoError(t, err)

			assert.EqualValues(t, 1, ra)
		})

		t.Run("confirm it has been removed", func(t *testing.T) {
			const query = `
				SELECT id, hash, compression, file_size, created_at, updated_at, last_accessed_at, query
				FROM nar_files
				`

			rows, err := db.DB().QueryContext(context.Background(), query)
			require.NoError(t, err)

			defer rows.Close()

			narFiles := make([]database.NarFile, 0)

			for rows.Next() {
				var nf database.NarFile

				err := rows.Scan(
					&nf.ID,
					&nf.Hash,
					&nf.Compression,
					&nf.FileSize,
					&nf.CreatedAt,
					&nf.UpdatedAt,
					&nf.LastAccessedAt,
					&nf.Query,
				)
				require.NoError(t, err)

				narFiles = append(narFiles, nf)
			}

			require.NoError(t, rows.Err())
			assert.Empty(t, narFiles)
		})
	})
}

func TestNarTotalSize(t *testing.T) {
	t.Parallel()

	db, cleanup := setupDatabase(t)
	defer cleanup()

	var expectedSize uint64
	for _, narEntry := range testdata.Entries {
		expectedSize += uint64(len(narEntry.NarText))

		_, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
			Hash:        narEntry.NarHash,
			Compression: nar.CompressionTypeXz.String(),
			FileSize:    uint64(len(narEntry.NarText)),
			Query:       "",
		})
		require.NoError(t, err)
	}

	size, err := db.GetNarTotalSize(context.Background())
	require.NoError(t, err)

	assert.EqualValues(t, expectedSize, size)
}

func TestGetLeastAccessedNars(t *testing.T) {
	t.Parallel()

	db, cleanup := setupDatabase(t)
	defer cleanup()

	// NOTE: For this test, any nar that's explicitly testing the zstd
	// transparent compression support will not be included because its size will
	// not be known and so the test will be more complex.
	var allEntries []testdata.Entry

	for _, narEntry := range testdata.Entries {
		expectedCompression := fmt.Sprintf("Compression: %s", narEntry.NarCompression)
		if strings.Contains(narEntry.NarInfoText, expectedCompression) {
			allEntries = append(allEntries, narEntry)
		}
	}

	for _, narEntry := range allEntries {
		_, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
			Hash:        narEntry.NarHash,
			Compression: nar.CompressionTypeXz.String(),
			FileSize:    uint64(len(narEntry.NarText)),
			Query:       "",
		})
		require.NoError(t, err)
	}

	time.Sleep(time.Second)

	for _, narEntry := range allEntries[:len(allEntries)-1] {
		_, err := db.TouchNarFile(context.Background(), narEntry.NarHash)
		require.NoError(t, err)
	}

	lastEntry := allEntries[len(allEntries)-1]

	// Ask for nars up to the size of the last entry (the least-accessed one)
	// This should return only the last entry since it's the least accessed
	sizeParam := uint64(len(lastEntry.NarText))

	nms, err := db.GetLeastUsedNarFiles(context.Background(), sizeParam)
	require.NoError(t, err)

	if assert.Len(t, nms, 1) {
		assert.Equal(t, lastEntry.NarHash, nms[0].Hash)
	}
}
