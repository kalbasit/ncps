package database_test

import (
	"context"
	"fmt"
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
)

// querierFactory is a function that returns a clean, ready-to-use Querier and
// it takes care of cleaning up once the test is done.
type querierFactory func(t *testing.T) database.Querier

func runComplianceSuite(t *testing.T, factory querierFactory) {
	t.Helper()

	t.Run("GetConfigByKey", func(t *testing.T) {
		t.Parallel()

		t.Run("key not existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			key, err := helper.RandString(32, nil)
			require.NoError(t, err)

			_, err = db.GetConfigByKey(context.Background(), key)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("key existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			key, err := helper.RandString(32, nil)
			require.NoError(t, err)

			value, err := helper.RandString(32, nil)
			require.NoError(t, err)

			conf1, err := db.CreateConfig(context.Background(), database.CreateConfigParams{
				Key:   key,
				Value: value,
			})
			require.NoError(t, err)

			conf2, err := db.GetConfigByKey(context.Background(), key)
			require.NoError(t, err)

			assert.Equal(t, conf1, conf2)
		})
	})

	t.Run("GetNarInfoByHash", func(t *testing.T) {
		t.Parallel()

		t.Run("narinfo not existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			_, err = db.GetNarInfoByHash(context.Background(), hash)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("narinfo existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			ni1, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			ni2, err := db.GetNarInfoByHash(context.Background(), hash)
			require.NoError(t, err)

			assert.Equal(t, ni1.Hash, ni2.Hash)
		})
	})

	t.Run("InsertNarInfo", func(t *testing.T) {
		db := factory(t)

		t.Run("inserting one record", func(t *testing.T) {
			hash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			nio, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
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

			ni1, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			ni2, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			assert.Equal(t, ni1.ID, ni2.ID)
		})

		t.Run("can write many narinfos", func(t *testing.T) {
			var wg sync.WaitGroup

			const numWrites = 10000

			errC := make(chan error, numWrites)

			for i := 0; i < numWrites; i++ {
				wg.Add(1)

				go func() {
					defer wg.Done()

					hash, err := helper.RandString(128, nil)
					if err != nil {
						errC <- fmt.Errorf("expected no error but got: %w", err)

						return
					}

					if _, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash}); err != nil {
						errC <- fmt.Errorf("error creating the narinfo record: %w", err)
					}
				}()
			}

			wg.Wait()
			close(errC)

			for err := range errC {
				assert.NoError(t, err)
			}
		})
	})

	t.Run("TouchNarInfo", func(t *testing.T) {
		db := factory(t)

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

			_, err = db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
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
	})

	t.Run("DeleteNarInfo", func(t *testing.T) {
		db := factory(t)

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
				_, err = db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
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
	})

	t.Run("CreateConfig", func(t *testing.T) {
		t.Parallel()

		t.Run("successful creation", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			key, err := helper.RandString(32, nil)
			require.NoError(t, err)

			value, err := helper.RandString(32, nil)
			require.NoError(t, err)

			createdConf, err := db.CreateConfig(context.Background(), database.CreateConfigParams{
				Key:   key,
				Value: value,
			})
			require.NoError(t, err)

			fetchedConf, err := db.GetConfigByKey(context.Background(), key)
			require.NoError(t, err)

			assert.Equal(t, createdConf, fetchedConf)
		})

		t.Run("duplicate key", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			key, err := helper.RandString(32, nil)
			require.NoError(t, err)

			value, err := helper.RandString(32, nil)
			require.NoError(t, err)

			_, err = db.CreateConfig(context.Background(), database.CreateConfigParams{
				Key:   key,
				Value: value,
			})
			require.NoError(t, err)

			// Try to create again with the same key
			_, err = db.CreateConfig(context.Background(), database.CreateConfigParams{
				Key:   key,
				Value: "another value",
			})
			assert.True(t, database.IsDuplicateKeyError(err))
		})
	})

	t.Run("GetNarFileByHashAndCompressionAndQuery", func(t *testing.T) {
		t.Parallel()

		t.Run("can store multiple representations of same hash", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			nf1, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: nar.CompressionTypeXz.String(),
				Query:       "hash=123&key=value",
				FileSize:    123,
			})
			require.NoError(t, err)

			nf2, err := db.GetNarFileByHashAndCompressionAndQuery(
				context.Background(),
				database.GetNarFileByHashAndCompressionAndQueryParams{
					Hash:        narHash,
					Compression: nar.CompressionTypeXz.String(),
					Query:       "hash=123&key=value",
				},
			)
			require.NoError(t, err)

			assert.Equal(t, nf1.Hash, nf2.Hash)
			assert.Equal(t, nf1.Compression, nf2.Compression)
			assert.Equal(t, nf1.Query, nf2.Query)

			// Store another one with different compression
			nf3, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: nar.CompressionTypeNone.String(),
				Query:       "hash=123&key=value",
				FileSize:    456,
			})
			require.NoError(t, err)
			assert.NotEqual(t, nf1.ID, nf3.ID)

			// Store another one with different query
			nf4, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: nar.CompressionTypeXz.String(),
				Query:       "different=query",
				FileSize:    789,
			})
			require.NoError(t, err)
			assert.NotEqual(t, nf1.ID, nf4.ID)
		})

		t.Run("nar not existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			_, err = db.GetNarFileByHashAndCompressionAndQuery(context.Background(), database.GetNarFileByHashAndCompressionAndQueryParams{
				Hash:        narHash,
				Compression: "xz",
				Query:       "",
			})
			require.Error(t, err)
			assert.True(t, database.IsNotFoundError(err))
		})

		t.Run("nar existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			nf1, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "zstd",
				FileSize:    123,
			})
			require.NoError(t, err)

			nf2, err := db.GetNarFileByHashAndCompressionAndQuery(context.Background(), database.GetNarFileByHashAndCompressionAndQueryParams{
				Hash:        narHash,
				Compression: "zstd",
				Query:       "",
			})
			require.NoError(t, err)

			assert.Equal(t, nf1.ID, nf2.ID)
			assert.Equal(t, nf1.Hash, nf2.Hash)
		})
	})

	t.Run("InsertNar", func(t *testing.T) {
		db := factory(t)

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

				t.Run("upsert on duplicate hash", func(t *testing.T) {
					hash, err := helper.RandString(32, nil)
					require.NoError(t, err)

					nf1, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
						Hash:        hash,
						Compression: "",
						FileSize:    123,
					})
					require.NoError(t, err)

					nf2, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
						Hash:        hash,
						Compression: "",
						FileSize:    123,
					})
					require.NoError(t, err)

					nf3, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
						Hash:        hash,
						Compression: "",
						FileSize:    123,
					})
					require.NoError(t, err)

					assert.Equal(t, nf1.ID, nf2.ID)
					assert.Equal(t, nf1.ID, nf3.ID)
				})
			})
		}
	})

	t.Run("TouchNarFile", func(t *testing.T) {
		db := factory(t)

		t.Run("nar not existing", func(t *testing.T) {
			hash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			ra, err := db.TouchNarFile(context.Background(), database.TouchNarFileParams{
				Hash: hash,
			})
			require.NoError(t, err)

			assert.Zero(t, ra)
		})

		t.Run("nar existing", func(t *testing.T) {
			hash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			t.Run("create the nar", func(t *testing.T) {
				_, err = db.CreateNarFile(context.Background(), database.CreateNarFileParams{
					Hash:        hash,
					Compression: "zstd",
					Query:       "foo=bar",
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

				ra, err := db.TouchNarFile(context.Background(), database.TouchNarFileParams{
					Hash:        hash,
					Compression: "zstd",
					Query:       "foo=bar",
				})
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
	})

	t.Run("DeleteNar", func(t *testing.T) {
		db := factory(t)

		t.Run("nar not existing", func(t *testing.T) {
			hash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			ra, err := db.DeleteNarFileByHash(context.Background(), database.DeleteNarFileByHashParams{
				Hash: hash,
			})
			require.NoError(t, err)

			assert.Zero(t, ra)
		})

		t.Run("nar existing", func(t *testing.T) {
			hash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			t.Run("create the nar", func(t *testing.T) {
				_, err = db.CreateNarFile(context.Background(), database.CreateNarFileParams{
					Hash:        hash,
					Compression: "zstd",
					Query:       "foo=bar",
					FileSize:    123,
				})
				require.NoError(t, err)
			})

			t.Run("delete the narinfo", func(t *testing.T) {
				time.Sleep(time.Second)

				ra, err := db.DeleteNarFileByHash(context.Background(), database.DeleteNarFileByHashParams{
					Hash:        hash,
					Compression: "zstd",
					Query:       "foo=bar",
				})
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

		t.Run("independent variants", func(t *testing.T) {
			hash, err := helper.RandString(32, nil)
			require.NoError(t, err)

			// Create two variants
			_, err = db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash,
				Compression: "xz",
				Query:       "q1",
				FileSize:    100,
			})
			require.NoError(t, err)

			v2, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash,
				Compression: "zstd",
				Query:       "q2",
				FileSize:    200,
			})
			require.NoError(t, err)

			// Delete only v1
			ra, err := db.DeleteNarFileByHash(context.Background(), database.DeleteNarFileByHashParams{
				Hash:        hash,
				Compression: "xz",
				Query:       "q1",
			})
			require.NoError(t, err)
			assert.EqualValues(t, 1, ra)

			// Confirm v1 is gone
			_, err = db.GetNarFileByHashAndCompressionAndQuery(
				context.Background(),
				database.GetNarFileByHashAndCompressionAndQueryParams{
					Hash:        hash,
					Compression: "xz",
					Query:       "q1",
				},
			)
			assert.True(t, database.IsNotFoundError(err))

			// Confirm v2 still exists
			retV2, err := db.GetNarFileByHashAndCompressionAndQuery(
				context.Background(),
				database.GetNarFileByHashAndCompressionAndQueryParams{
					Hash:        hash,
					Compression: "zstd",
					Query:       "q2",
				},
			)
			require.NoError(t, err)
			assert.Equal(t, v2.ID, retV2.ID)
		})
	})

	t.Run("NarTotalSize", func(t *testing.T) {
		t.Parallel()

		db := factory(t)

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
	})

	t.Run("GetLeastAccessedNars", func(t *testing.T) {
		t.Parallel()

		db := factory(t)

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
			_, err := db.TouchNarFile(context.Background(), database.TouchNarFileParams{
				Hash:        narEntry.NarHash,
				Compression: nar.CompressionTypeXz.String(),
				Query:       "",
			})
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
	})

	t.Run("SetConfig", func(t *testing.T) {
		t.Parallel()

		t.Run("key not existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			key, err := helper.RandString(32, nil)
			require.NoError(t, err)

			value, err := helper.RandString(32, nil)
			require.NoError(t, err)

			err = db.SetConfig(context.Background(), database.SetConfigParams{
				Key:   key,
				Value: value,
			})
			require.NoError(t, err)

			conf, err := db.GetConfigByKey(context.Background(), key)
			require.NoError(t, err)

			assert.Equal(t, key, conf.Key)
			assert.Equal(t, value, conf.Value)
		})

		t.Run("key existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			key, err := helper.RandString(32, nil)
			require.NoError(t, err)

			value, err := helper.RandString(32, nil)
			require.NoError(t, err)

			err = db.SetConfig(context.Background(), database.SetConfigParams{
				Key:   key,
				Value: value,
			})
			require.NoError(t, err)

			value2, err := helper.RandString(32, nil)
			require.NoError(t, err)

			err = db.SetConfig(context.Background(), database.SetConfigParams{
				Key:   key,
				Value: value2,
			})
			require.NoError(t, err)

			conf, err := db.GetConfigByKey(context.Background(), key)
			require.NoError(t, err)

			assert.Equal(t, key, conf.Key)
			assert.Equal(t, value2, conf.Value)
		})
	})
}
