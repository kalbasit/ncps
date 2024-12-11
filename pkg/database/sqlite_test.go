package database_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inconshreveable/log15/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

//nolint:paralleltest
func TestInsertNarInfoRecord(t *testing.T) {
	dir, err := os.MkdirTemp("", "database-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open(logger, dbFile)
	require.NoError(t, err)

	t.Run("inserting one record", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		tx, err := db.Begin()
		require.NoError(t, err)

		//nolint:errcheck
		defer tx.Rollback()

		res, err := db.InsertNarInfoRecord(tx, hash)
		require.NoError(t, err)

		require.NoError(t, tx.Commit())

		rows, err := db.Query("SELECT id, hash, created_at, updated_at, last_accessed_at FROM narinfos")
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

		lid, err := res.LastInsertId()
		require.NoError(t, err)

		if assert.Len(t, nims, 1) {
			assert.Equal(t, lid, nims[0].ID)
			assert.Equal(t, hash, nims[0].Hash)
			assert.Less(t, time.Since(nims[0].CreatedAt), 3*time.Second)
			assert.False(t, nims[0].UpdatedAt.Valid)
			assert.Equal(t, nims[0].CreatedAt, nims[0].LastAccessedAt.Time)
		}
	})

	t.Run("hash is unique", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		tx, err := db.Begin()
		require.NoError(t, err)

		//nolint:errcheck
		defer tx.Rollback()

		_, err = db.InsertNarInfoRecord(tx, hash)
		require.NoError(t, err)

		require.NoError(t, tx.Commit())

		tx, err = db.Begin()
		require.NoError(t, err)

		//nolint:errcheck
		defer tx.Rollback()

		_, err = db.InsertNarInfoRecord(tx, hash)
		assert.ErrorIs(t, err, database.ErrAlreadyExists)
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

				tx, err := db.Begin()
				if err != nil {
					errC <- fmt.Errorf("expected no error but got: %w", err)

					return
				}

				//nolint:errcheck
				defer tx.Rollback()

				if _, err := db.InsertNarInfoRecord(tx, hash); err != nil {
					errC <- fmt.Errorf("expected no error got: %w", err)

					return
				}

				if err := tx.Commit(); err != nil {
					errC <- fmt.Errorf("expected no error got: %w", err)

					return
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
func TestTouchNarInfoRecord(t *testing.T) {
	dir, err := os.MkdirTemp("", "database-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open(logger, dbFile)
	require.NoError(t, err)

	t.Run("narinfo not existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		tx, err := db.Begin()
		require.NoError(t, err)

		//nolint:errcheck
		defer tx.Rollback()

		res, err := db.TouchNarInfoRecord(tx, hash)
		require.NoError(t, err)

		ra, err := res.RowsAffected()
		require.NoError(t, err)

		assert.Zero(t, ra)
	})

	t.Run("narinfo existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		t.Run("create the narinfo", func(t *testing.T) {
			tx, err := db.Begin()
			require.NoError(t, err)

			//nolint:errcheck
			defer tx.Rollback()

			_, err = db.InsertNarInfoRecord(tx, hash)
			require.NoError(t, err)

			require.NoError(t, tx.Commit())
		})

		t.Run("confirm created_at == last_accessed_at, and no updated_at", func(t *testing.T) {
			rows, err := db.Query("SELECT id, hash, created_at, updated_at, last_accessed_at FROM narinfos")
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
			tx, err := db.Begin()
			require.NoError(t, err)

			//nolint:errcheck
			defer tx.Rollback()

			time.Sleep(time.Second)

			res, err := db.TouchNarInfoRecord(tx, hash)
			require.NoError(t, err)

			require.NoError(t, tx.Commit())

			ra, err := res.RowsAffected()
			require.NoError(t, err)

			assert.EqualValues(t, 1, ra)
		})

		t.Run("confirm created_at != last_accessed_at and updated_at == last_accessed_at", func(t *testing.T) {
			rows, err := db.Query("SELECT id, hash, created_at, updated_at, last_accessed_at FROM narinfos")
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
func TestDeleteNarInfoRecord(t *testing.T) {
	dir, err := os.MkdirTemp("", "database-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open(logger, dbFile)
	require.NoError(t, err)

	t.Run("narinfo not existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		tx, err := db.Begin()
		require.NoError(t, err)

		//nolint:errcheck
		defer tx.Rollback()

		err = db.DeleteNarInfoRecord(tx, hash)
		require.NoError(t, err)
	})

	t.Run("narinfo existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		t.Run("create the narinfo", func(t *testing.T) {
			tx, err := db.Begin()
			require.NoError(t, err)

			//nolint:errcheck
			defer tx.Rollback()

			_, err = db.InsertNarInfoRecord(tx, hash)
			require.NoError(t, err)

			assert.NoError(t, tx.Commit())
		})

		t.Run("delete the narinfo", func(t *testing.T) {
			tx, err := db.Begin()
			require.NoError(t, err)

			//nolint:errcheck
			defer tx.Rollback()

			time.Sleep(time.Second)

			err = db.DeleteNarInfoRecord(tx, hash)
			require.NoError(t, err)

			assert.NoError(t, tx.Commit())
		})

		t.Run("confirm it has been removed", func(t *testing.T) {
			rows, err := db.Query("SELECT id, hash, created_at, updated_at, last_accessed_at FROM narinfos")
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

//nolint:paralleltest
func TestInsertNarRecord(t *testing.T) {
	dir, err := os.MkdirTemp("", "database-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open(logger, dbFile)
	require.NoError(t, err)

	// create a narinfo
	hash, err := helper.RandString(32, nil)
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	//nolint:errcheck
	defer tx.Rollback()

	res, err := db.InsertNarInfoRecord(tx, hash)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	nid, err := res.LastInsertId()
	require.NoError(t, err)

	for _, compression := range []string{"", "xz", "tar.gz"} {
		t.Run(fmt.Sprintf("compression=%q", compression), func(t *testing.T) {
			_, err := db.Exec("DELETE FROM nars")
			require.NoError(t, err)

			t.Run("inserting one record", func(t *testing.T) {
				hash, err := helper.RandString(32, nil)
				require.NoError(t, err)

				tx, err := db.Begin()
				require.NoError(t, err)

				//nolint:errcheck
				defer tx.Rollback()

				res, err := db.InsertNarRecord(tx, nid, hash, compression, 123)
				require.NoError(t, err)

				require.NoError(t, tx.Commit())

				const query = `
				SELECT id, narinfo_id, hash, compression, file_size, created_at, updated_at, last_accessed_at
				FROM nars
				`

				rows, err := db.Query(query)
				require.NoError(t, err)

				defer rows.Close()

				nims := make([]database.Nar, 0)

				for rows.Next() {
					var nim database.Nar

					err := rows.Scan(
						&nim.ID,
						&nim.NarInfoID,
						&nim.Hash,
						&nim.Compression,
						&nim.FileSize,
						&nim.CreatedAt,
						&nim.UpdatedAt,
						&nim.LastAccessedAt,
					)
					require.NoError(t, err)

					nims = append(nims, nim)
				}

				require.NoError(t, rows.Err())

				lid, err := res.LastInsertId()
				require.NoError(t, err)

				if assert.Len(t, nims, 1) {
					assert.Equal(t, lid, nims[0].ID)
					assert.Equal(t, nid, nims[0].NarInfoID)
					assert.Equal(t, hash, nims[0].Hash)
					assert.Equal(t, compression, nims[0].Compression)
					assert.EqualValues(t, 123, nims[0].FileSize)
					assert.Less(t, time.Since(nims[0].CreatedAt), 3*time.Second)
					assert.False(t, nims[0].UpdatedAt.Valid)
					assert.Equal(t, nims[0].CreatedAt, nims[0].LastAccessedAt.Time)
				}
			})

			t.Run("hash is unique", func(t *testing.T) {
				hash, err := helper.RandString(32, nil)
				require.NoError(t, err)

				tx, err := db.Begin()
				require.NoError(t, err)

				//nolint:errcheck
				defer tx.Rollback()

				_, err = db.InsertNarRecord(tx, nid, hash, "", 123)
				require.NoError(t, err)

				require.NoError(t, tx.Commit())

				tx, err = db.Begin()
				require.NoError(t, err)

				//nolint:errcheck
				defer tx.Rollback()

				_, err = db.InsertNarRecord(tx, nid, hash, "", 123)

				assert.ErrorIs(t, database.ErrAlreadyExists, err)
			})
		})
	}
}

//nolint:paralleltest
func TestTouchNarRecord(t *testing.T) {
	dir, err := os.MkdirTemp("", "database-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open(logger, dbFile)
	require.NoError(t, err)

	t.Run("nar not existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		tx, err := db.Begin()
		require.NoError(t, err)

		//nolint:errcheck
		defer tx.Rollback()

		res, err := db.TouchNarRecord(tx, hash)
		require.NoError(t, err)

		ra, err := res.RowsAffected()
		require.NoError(t, err)

		assert.Zero(t, ra)
	})

	t.Run("nar existing", func(t *testing.T) {
		var nid int64

		t.Run("create the narinfo", func(t *testing.T) {
			// create a narinfo
			hash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			tx, err := db.Begin()
			require.NoError(t, err)

			//nolint:errcheck
			defer tx.Rollback()

			res, err := db.InsertNarInfoRecord(tx, hash)
			require.NoError(t, err)

			require.NoError(t, tx.Commit())

			nid, err = res.LastInsertId()
			require.NoError(t, err)
		})

		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		t.Run("create the nar", func(t *testing.T) {
			tx, err := db.Begin()
			require.NoError(t, err)

			//nolint:errcheck
			defer tx.Rollback()

			_, err = db.InsertNarRecord(tx, nid, hash, "", 123)
			require.NoError(t, err)

			require.NoError(t, tx.Commit())
		})

		t.Run("confirm created_at == last_accessed_at, and no updated_at", func(t *testing.T) {
			const query = `
				SELECT id, narinfo_id, hash, compression, file_size, created_at, updated_at, last_accessed_at
				FROM nars
				`

			rows, err := db.Query(query)
			require.NoError(t, err)

			defer rows.Close()

			nims := make([]database.Nar, 0)

			for rows.Next() {
				var nim database.Nar

				err := rows.Scan(
					&nim.ID,
					&nim.NarInfoID,
					&nim.Hash,
					&nim.Compression,
					&nim.FileSize,
					&nim.CreatedAt,
					&nim.UpdatedAt,
					&nim.LastAccessedAt,
				)
				require.NoError(t, err)

				nims = append(nims, nim)
			}

			require.NoError(t, rows.Err())

			if assert.Len(t, nims, 1) {
				assert.False(t, nims[0].UpdatedAt.Valid)
				assert.Equal(t, nims[0].CreatedAt, nims[0].LastAccessedAt.Time)
			}
		})

		t.Run("touch the nar", func(t *testing.T) {
			tx, err := db.Begin()
			require.NoError(t, err)

			//nolint:errcheck
			defer tx.Rollback()

			time.Sleep(time.Second)

			res, err := db.TouchNarRecord(tx, hash)
			require.NoError(t, err)

			require.NoError(t, tx.Commit())

			ra, err := res.RowsAffected()
			require.NoError(t, err)

			assert.EqualValues(t, 1, ra)
		})

		t.Run("confirm created_at != last_accessed_at and updated_at == last_accessed_at", func(t *testing.T) {
			const query = `
				SELECT id, narinfo_id, hash, compression, file_size, created_at, updated_at, last_accessed_at
				FROM nars
				`

			rows, err := db.Query(query)
			require.NoError(t, err)

			defer rows.Close()

			nims := make([]database.Nar, 0)

			for rows.Next() {
				var nim database.Nar

				err := rows.Scan(
					&nim.ID,
					&nim.NarInfoID,
					&nim.Hash,
					&nim.Compression,
					&nim.FileSize,
					&nim.CreatedAt,
					&nim.UpdatedAt,
					&nim.LastAccessedAt,
				)
				require.NoError(t, err)

				nims = append(nims, nim)
			}

			require.NoError(t, rows.Err())

			if assert.Len(t, nims, 1) {
				assert.NotEqual(t, nims[0].CreatedAt, nims[0].LastAccessedAt)

				if assert.True(t, nims[0].UpdatedAt.Valid) {
					assert.Equal(t, nims[0].UpdatedAt.Time, nims[0].LastAccessedAt.Time)
				}
			}
		})
	})
}

//nolint:paralleltest
func TestDeleteNarRecord(t *testing.T) {
	dir, err := os.MkdirTemp("", "database-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open(logger, dbFile)
	require.NoError(t, err)

	t.Run("nar not existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		tx, err := db.Begin()
		require.NoError(t, err)

		//nolint:errcheck
		defer tx.Rollback()

		err = db.DeleteNarRecord(tx, hash)
		require.NoError(t, err)
	})

	t.Run("nar existing", func(t *testing.T) {
		var nid int64

		t.Run("create the narinfo", func(t *testing.T) {
			// create a narinfo
			hash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			tx, err := db.Begin()
			require.NoError(t, err)

			//nolint:errcheck
			defer tx.Rollback()

			res, err := db.InsertNarInfoRecord(tx, hash)
			require.NoError(t, err)

			require.NoError(t, tx.Commit())

			nid, err = res.LastInsertId()
			require.NoError(t, err)
		})

		hash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		t.Run("create the nar", func(t *testing.T) {
			tx, err := db.Begin()
			require.NoError(t, err)

			//nolint:errcheck
			defer tx.Rollback()

			_, err = db.InsertNarRecord(tx, nid, hash, "", 123)
			require.NoError(t, err)

			assert.NoError(t, tx.Commit())
		})

		t.Run("delete the narinfo", func(t *testing.T) {
			tx, err := db.Begin()
			require.NoError(t, err)

			//nolint:errcheck
			defer tx.Rollback()

			time.Sleep(time.Second)

			err = db.DeleteNarRecord(tx, hash)
			require.NoError(t, err)

			assert.NoError(t, tx.Commit())
		})

		t.Run("confirm it has been removed", func(t *testing.T) {
			const query = `
				SELECT id, narinfo_id, hash, compression, file_size, created_at, updated_at, last_accessed_at
				FROM nars
				`

			rows, err := db.Query(query)
			require.NoError(t, err)

			defer rows.Close()

			nims := make([]database.Nar, 0)

			for rows.Next() {
				var nim database.Nar

				err := rows.Scan(
					&nim.ID,
					&nim.NarInfoID,
					&nim.Hash,
					&nim.Compression,
					&nim.FileSize,
					&nim.CreatedAt,
					&nim.UpdatedAt,
					&nim.LastAccessedAt,
				)
				require.NoError(t, err)

				nims = append(nims, nim)
			}

			require.NoError(t, rows.Err())
			assert.Empty(t, nims)
		})
	})
}

func TestNarTotalSize(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "database-path-")
	require.NoError(t, err)

	defer os.RemoveAll(dir) // clean up

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open(logger, dbFile)
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	var expectedSize uint64
	for _, nar := range testdata.Entries {
		expectedSize += uint64(len(nar.NarText))

		res, err := db.InsertNarInfoRecord(tx, nar.NarInfoHash)
		require.NoError(t, err)

		nid, err := res.LastInsertId()
		require.NoError(t, err)

		_, err = db.InsertNarRecord(tx, nid, nar.NarHash, "", uint64(len(nar.NarText)))
		require.NoError(t, err)
	}

	require.NoError(t, tx.Commit())

	tx, err = db.Begin()
	require.NoError(t, err)

	size, err := db.NarTotalSize(tx)
	require.NoError(t, err)

	assert.Equal(t, expectedSize, size)
}

func TestGetLeastAccessedNarRecords(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "database-path-")
	require.NoError(t, err)

	defer os.RemoveAll(dir) // clean up

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open(logger, dbFile)
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	var totalSize uint64
	for _, nar := range testdata.Entries {
		totalSize += uint64(len(nar.NarText))

		res, err := db.InsertNarInfoRecord(tx, nar.NarInfoHash)
		require.NoError(t, err)

		nid, err := res.LastInsertId()
		require.NoError(t, err)

		_, err = db.InsertNarRecord(tx, nid, nar.NarHash, "", uint64(len(nar.NarText)))
		require.NoError(t, err)
	}

	time.Sleep(time.Second)

	for _, nar := range testdata.Entries[:len(testdata.Entries)-1] {
		_, err := db.TouchNarRecord(tx, nar.NarHash)
		require.NoError(t, err)
	}

	require.NoError(t, tx.Commit())

	tx, err = db.Begin()
	require.NoError(t, err)

	lastEntry := testdata.Entries[len(testdata.Entries)-1]

	nms, err := db.GetLeastAccessedNarRecords(tx, totalSize-uint64(len(lastEntry.NarText)))
	require.NoError(t, err)

	if assert.Len(t, nms, 1) {
		assert.Equal(t, lastEntry.NarHash, nms[0].Hash)
	}
}
