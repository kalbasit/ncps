package database_test

import (
	"context"
	"database/sql"
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

			key := helper.MustRandString(32, nil)

			_, err := db.GetConfigByKey(context.Background(), key)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("key existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			key := helper.MustRandString(32, nil)

			value := helper.MustRandString(32, nil)

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

			hash := helper.MustRandString(32, nil)

			_, err := db.GetNarInfoByHash(context.Background(), hash)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("narinfo existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

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
			hash := helper.MustRandString(32, nil)

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
			hash := helper.MustRandString(32, nil)

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

					hash := helper.MustRandString(128, nil)

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

		t.Run("CreateNarInfoUpdateFromPlaceholder", func(t *testing.T) {
			hash := helper.MustRandString(32, nil)

			// 1. Create a placeholder (url IS NULL)
			_, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{
				Hash: hash,
			})
			require.NoError(t, err)

			// 2. Perform the "migration" upsert
			fileHash := "sha256:1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri"
			narURL := "nar/1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri.nar.xz"
			_, err = db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{
				Hash:     hash,
				URL:      sql.NullString{String: narURL, Valid: true},
				FileHash: sql.NullString{String: fileHash, Valid: true},
			})
			require.NoError(t, err)

			// 3. Verify it was correctly updated
			ni, err := db.GetNarInfoByHash(context.Background(), hash)
			require.NoError(t, err)

			assert.True(t, ni.URL.Valid)
			assert.Equal(t, narURL, ni.URL.String)
			assert.True(t, ni.FileHash.Valid)
			assert.Equal(t, fileHash, ni.FileHash.String)
		})
	})

	t.Run("UpdateNarInfo", func(t *testing.T) {
		db := factory(t)
		ctx := context.Background()

		t.Run("updating an existing narinfo", func(t *testing.T) {
			hash := helper.MustRandString(32, nil)

			// 1. Create a narinfo
			_, err := db.CreateNarInfo(ctx, database.CreateNarInfoParams{
				Hash: hash,
			})
			require.NoError(t, err)

			// 2. Update it
			params := database.UpdateNarInfoParams{
				Hash:        hash,
				StorePath:   sql.NullString{String: "/nix/store/hash-name", Valid: true},
				URL:         sql.NullString{String: "nar/hash.nar", Valid: true},
				Compression: sql.NullString{String: "zstd", Valid: true},
				FileHash:    sql.NullString{String: "sha256:filehash", Valid: true},
				FileSize:    sql.NullInt64{Int64: 1234, Valid: true},
				NarHash:     sql.NullString{String: "sha256:narhash", Valid: true},
				NarSize:     sql.NullInt64{Int64: 5678, Valid: true},
				Deriver:     sql.NullString{String: "deriver", Valid: true},
				System:      sql.NullString{String: "x86_64-linux", Valid: true},
				Ca:          sql.NullString{String: "ca", Valid: true},
			}

			updated, err := db.UpdateNarInfo(ctx, params)
			require.NoError(t, err)

			// helper function to verify narinfo fields
			verifyFields := func(t *testing.T, ni database.NarInfo) {
				t.Helper()
				assert.Equal(t, params.StorePath, ni.StorePath)
				assert.Equal(t, params.URL, ni.URL)
				assert.Equal(t, params.Compression, ni.Compression)
				assert.Equal(t, params.FileHash, ni.FileHash)
				assert.Equal(t, params.FileSize, ni.FileSize)
				assert.Equal(t, params.NarHash, ni.NarHash)
				assert.Equal(t, params.NarSize, ni.NarSize)
				assert.Equal(t, params.Deriver, ni.Deriver)
				assert.Equal(t, params.System, ni.System)
				assert.Equal(t, params.Ca, ni.Ca)
			}

			// Verify returned object
			assert.Equal(t, hash, updated.Hash)
			verifyFields(t, updated)

			// 3. Verify the updates by fetching
			ni, err := db.GetNarInfoByHash(ctx, hash)
			require.NoError(t, err)

			verifyFields(t, ni)
		})

		t.Run("updating a non-existing narinfo", func(t *testing.T) {
			hash := helper.MustRandString(32, nil)
			params := database.UpdateNarInfoParams{
				Hash: hash,
			}

			_, err := db.UpdateNarInfo(ctx, params)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})
	})

	t.Run("TouchNarInfo", func(t *testing.T) {
		db := factory(t)

		t.Run("narinfo not existing", func(t *testing.T) {
			hash := helper.MustRandString(32, nil)

			ra, err := db.TouchNarInfo(context.Background(), hash)
			require.NoError(t, err)

			assert.Zero(t, ra)
		})

		t.Run("narinfo existing", func(t *testing.T) {
			hash := helper.MustRandString(32, nil)

			_, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
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
			hash := helper.MustRandString(32, nil)

			ra, err := db.DeleteNarInfoByHash(context.Background(), hash)
			require.NoError(t, err)

			assert.Zero(t, ra)
		})

		t.Run("narinfo existing", func(t *testing.T) {
			hash := helper.MustRandString(32, nil)

			t.Run("create the narinfo", func(t *testing.T) {
				_, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
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

			key := helper.MustRandString(32, nil)

			value := helper.MustRandString(32, nil)

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

			key := helper.MustRandString(32, nil)

			value := helper.MustRandString(32, nil)

			_, err := db.CreateConfig(context.Background(), database.CreateConfigParams{
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

			narHash := helper.MustRandString(32, nil)

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

			narHash := helper.MustRandString(32, nil)

			_, err := db.GetNarFileByHashAndCompressionAndQuery(
				context.Background(),
				database.GetNarFileByHashAndCompressionAndQueryParams{
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

			narHash := helper.MustRandString(32, nil)

			nf1, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "zstd",
				FileSize:    123,
			})
			require.NoError(t, err)

			nf2, err := db.GetNarFileByHashAndCompressionAndQuery(
				context.Background(),
				database.GetNarFileByHashAndCompressionAndQueryParams{
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
			hash := helper.MustRandString(32, nil)

			ra, err := db.TouchNarFile(context.Background(), database.TouchNarFileParams{
				Hash: hash,
			})
			require.NoError(t, err)

			assert.Zero(t, ra)
		})

		t.Run("nar existing", func(t *testing.T) {
			hash := helper.MustRandString(32, nil)

			t.Run("create the nar", func(t *testing.T) {
				_, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
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
			hash := helper.MustRandString(32, nil)

			ra, err := db.DeleteNarFileByHash(context.Background(), database.DeleteNarFileByHashParams{
				Hash: hash,
			})
			require.NoError(t, err)

			assert.Zero(t, ra)
		})

		t.Run("nar existing", func(t *testing.T) {
			hash := helper.MustRandString(32, nil)

			t.Run("create the nar", func(t *testing.T) {
				_, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
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
			hash := helper.MustRandString(32, nil)

			// Create two variants
			_, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
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

			key := helper.MustRandString(32, nil)

			value := helper.MustRandString(32, nil)

			err := db.SetConfig(context.Background(), database.SetConfigParams{
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

			key := helper.MustRandString(32, nil)

			value := helper.MustRandString(32, nil)

			err := db.SetConfig(context.Background(), database.SetConfigParams{
				Key:   key,
				Value: value,
			})
			require.NoError(t, err)

			value2 := helper.MustRandString(32, nil)

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

	t.Run("AddNarInfoReference", func(t *testing.T) {
		t.Parallel()

		t.Run("successful insertion", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			reference := helper.MustRandString(32, nil)

			err = db.AddNarInfoReference(context.Background(), database.AddNarInfoReferenceParams{
				NarInfoID: ni.ID,
				Reference: reference,
			})
			require.NoError(t, err)

			refs, err := db.GetNarInfoReferences(context.Background(), ni.ID)
			require.NoError(t, err)

			if assert.Len(t, refs, 1) {
				assert.Equal(t, reference, refs[0])
			}
		})

		t.Run("duplicate reference is idempotent", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			reference := helper.MustRandString(32, nil)

			// Insert first time
			err = db.AddNarInfoReference(context.Background(), database.AddNarInfoReferenceParams{
				NarInfoID: ni.ID,
				Reference: reference,
			})
			require.NoError(t, err)

			// Insert duplicate - should not error
			err = db.AddNarInfoReference(context.Background(), database.AddNarInfoReferenceParams{
				NarInfoID: ni.ID,
				Reference: reference,
			})
			require.NoError(t, err, "duplicate reference insertion should be idempotent")

			// Verify only one reference exists
			refs, err := db.GetNarInfoReferences(context.Background(), ni.ID)
			require.NoError(t, err)

			if assert.Len(t, refs, 1) {
				assert.Equal(t, reference, refs[0])
			}
		})
	})

	t.Run("AddNarInfoReferences", func(t *testing.T) {
		t.Parallel()

		t.Run("successful bulk insertion", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			references := make([]string, 3)
			for i := range references {
				ref, err := helper.RandString(32, nil)
				require.NoError(t, err)

				references[i] = ref
			}

			err = db.AddNarInfoReferences(context.Background(), database.AddNarInfoReferencesParams{
				NarInfoID: ni.ID,
				Reference: references,
			})
			require.NoError(t, err)

			refs, err := db.GetNarInfoReferences(context.Background(), ni.ID)
			require.NoError(t, err)

			assert.Len(t, refs, 3)
		})

		t.Run("duplicate references in same batch are idempotent", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			reference := helper.MustRandString(32, nil)

			// Insert same reference multiple times in one batch
			references := []string{reference, reference, reference}

			err = db.AddNarInfoReferences(context.Background(), database.AddNarInfoReferencesParams{
				NarInfoID: ni.ID,
				Reference: references,
			})
			require.NoError(t, err, "duplicate references in batch should be idempotent")

			// Verify only one reference exists
			refs, err := db.GetNarInfoReferences(context.Background(), ni.ID)
			require.NoError(t, err)

			if assert.Len(t, refs, 1) {
				assert.Equal(t, reference, refs[0])
			}
		})

		t.Run("duplicate references across batches are idempotent", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			ref1 := helper.MustRandString(32, nil)

			ref2 := helper.MustRandString(32, nil)

			// First batch
			err = db.AddNarInfoReferences(context.Background(), database.AddNarInfoReferencesParams{
				NarInfoID: ni.ID,
				Reference: []string{ref1, ref2},
			})
			require.NoError(t, err)

			// Second batch with duplicates
			err = db.AddNarInfoReferences(context.Background(), database.AddNarInfoReferencesParams{
				NarInfoID: ni.ID,
				Reference: []string{ref1, ref2},
			})
			require.NoError(t, err, "duplicate references across batches should be idempotent")

			// Verify only two unique references exist
			refs, err := db.GetNarInfoReferences(context.Background(), ni.ID)
			require.NoError(t, err)

			assert.Len(t, refs, 2)
		})
	})

	t.Run("AddNarInfoSignature", func(t *testing.T) {
		t.Parallel()

		t.Run("successful insertion", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			signature := helper.MustRandString(32, nil)

			err = db.AddNarInfoSignature(context.Background(), database.AddNarInfoSignatureParams{
				NarInfoID: ni.ID,
				Signature: signature,
			})
			require.NoError(t, err)

			sigs, err := db.GetNarInfoSignatures(context.Background(), ni.ID)
			require.NoError(t, err)

			if assert.Len(t, sigs, 1) {
				assert.Equal(t, signature, sigs[0])
			}
		})

		t.Run("duplicate signature is idempotent", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			signature := helper.MustRandString(32, nil)

			// Insert first time
			err = db.AddNarInfoSignature(context.Background(), database.AddNarInfoSignatureParams{
				NarInfoID: ni.ID,
				Signature: signature,
			})
			require.NoError(t, err)

			// Insert duplicate - should not error
			err = db.AddNarInfoSignature(context.Background(), database.AddNarInfoSignatureParams{
				NarInfoID: ni.ID,
				Signature: signature,
			})
			require.NoError(t, err, "duplicate signature insertion should be idempotent")

			// Verify only one signature exists
			sigs, err := db.GetNarInfoSignatures(context.Background(), ni.ID)
			require.NoError(t, err)

			if assert.Len(t, sigs, 1) {
				assert.Equal(t, signature, sigs[0])
			}
		})
	})

	t.Run("AddNarInfoSignatures", func(t *testing.T) {
		t.Parallel()

		t.Run("successful bulk insertion", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			signatures := make([]string, 3)
			for i := range signatures {
				sig, err := helper.RandString(32, nil)
				require.NoError(t, err)

				signatures[i] = sig
			}

			err = db.AddNarInfoSignatures(context.Background(), database.AddNarInfoSignaturesParams{
				NarInfoID: ni.ID,
				Signature: signatures,
			})
			require.NoError(t, err)

			sigs, err := db.GetNarInfoSignatures(context.Background(), ni.ID)
			require.NoError(t, err)

			assert.Len(t, sigs, 3)
		})

		t.Run("duplicate signatures in same batch are idempotent", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			signature := helper.MustRandString(32, nil)

			// Insert same signature multiple times in one batch
			signatures := []string{signature, signature, signature}

			err = db.AddNarInfoSignatures(context.Background(), database.AddNarInfoSignaturesParams{
				NarInfoID: ni.ID,
				Signature: signatures,
			})
			require.NoError(t, err, "duplicate signatures in batch should be idempotent")

			// Verify only one signature exists
			sigs, err := db.GetNarInfoSignatures(context.Background(), ni.ID)
			require.NoError(t, err)

			if assert.Len(t, sigs, 1) {
				assert.Equal(t, signature, sigs[0])
			}
		})

		t.Run("duplicate signatures across batches are idempotent", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			sig1 := helper.MustRandString(32, nil)

			sig2 := helper.MustRandString(32, nil)

			// First batch
			err = db.AddNarInfoSignatures(context.Background(), database.AddNarInfoSignaturesParams{
				NarInfoID: ni.ID,
				Signature: []string{sig1, sig2},
			})
			require.NoError(t, err)

			// Second batch with duplicates
			err = db.AddNarInfoSignatures(context.Background(), database.AddNarInfoSignaturesParams{
				NarInfoID: ni.ID,
				Signature: []string{sig1, sig2},
			})
			require.NoError(t, err, "duplicate signatures across batches should be idempotent")

			// Verify only two unique signatures exist
			sigs, err := db.GetNarInfoSignatures(context.Background(), ni.ID)
			require.NoError(t, err)

			assert.Len(t, sigs, 2)
		})
	})

	t.Run("GetConfigByID", func(t *testing.T) {
		t.Parallel()

		t.Run("config not existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			_, err := db.GetConfigByID(context.Background(), 999999)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("config existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			key := helper.MustRandString(32, nil)

			value := helper.MustRandString(32, nil)

			conf1, err := db.CreateConfig(context.Background(), database.CreateConfigParams{
				Key:   key,
				Value: value,
			})
			require.NoError(t, err)

			conf2, err := db.GetConfigByID(context.Background(), conf1.ID)
			require.NoError(t, err)

			assert.Equal(t, conf1, conf2)
		})
	})

	t.Run("GetNarInfoByID", func(t *testing.T) {
		t.Parallel()

		t.Run("narinfo not existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			_, err := db.GetNarInfoByID(context.Background(), 999999)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("narinfo existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			ni1, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			ni2, err := db.GetNarInfoByID(context.Background(), ni1.ID)
			require.NoError(t, err)

			assert.Equal(t, ni1.ID, ni2.ID)
			assert.Equal(t, ni1.Hash, ni2.Hash)
		})
	})

	t.Run("GetNarFileByID", func(t *testing.T) {
		t.Parallel()

		t.Run("nar file not existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			_, err := db.GetNarFileByID(context.Background(), 999999)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("nar file existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			nf1, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			nf2, err := db.GetNarFileByID(context.Background(), nf1.ID)
			require.NoError(t, err)

			assert.Equal(t, nf1.ID, nf2.ID)
			assert.Equal(t, nf1.Hash, nf2.Hash)
		})
	})

	t.Run("UpdateNarInfoFileSize", func(t *testing.T) {
		t.Parallel()

		t.Run("update file size", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			assert.False(t, ni.FileSize.Valid)

			err = db.UpdateNarInfoFileSize(context.Background(), database.UpdateNarInfoFileSizeParams{
				Hash:     hash,
				FileSize: sql.NullInt64{Int64: 456, Valid: true},
			})
			require.NoError(t, err)

			ni2, err := db.GetNarInfoByHash(context.Background(), hash)
			require.NoError(t, err)

			if assert.True(t, ni2.FileSize.Valid) {
				assert.EqualValues(t, 456, ni2.FileSize.Int64)
			}
		})
	})

	t.Run("GetNarInfoHashesByURL", func(t *testing.T) {
		t.Parallel()

		t.Run("no narinfos with url", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hashes, err := db.GetNarInfoHashesByURL(context.Background(),
				sql.NullString{String: "nonexistent.nar", Valid: true})
			require.NoError(t, err)
			assert.Empty(t, hashes)
		})

		t.Run("multiple narinfos with same url", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			url := "nar/test.nar.xz"
			hash1 := helper.MustRandString(32, nil)

			hash2 := helper.MustRandString(32, nil)

			_, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{
				Hash: hash1,
				URL:  sql.NullString{String: url, Valid: true},
			})
			require.NoError(t, err)

			_, err = db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{
				Hash: hash2,
				URL:  sql.NullString{String: url, Valid: true},
			})
			require.NoError(t, err)

			hashes, err := db.GetNarInfoHashesByURL(context.Background(),
				sql.NullString{String: url, Valid: true})
			require.NoError(t, err)

			assert.Len(t, hashes, 2)
			assert.Contains(t, hashes, hash1)
			assert.Contains(t, hashes, hash2)
		})
	})

	t.Run("LinkNarInfoToNarFile", func(t *testing.T) {
		t.Parallel()

		t.Run("successful linking", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash1 := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash1})
			require.NoError(t, err)

			hash2 := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash2,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			err = db.LinkNarInfoToNarFile(context.Background(), database.LinkNarInfoToNarFileParams{
				NarInfoID: ni.ID,
				NarFileID: nf.ID,
			})
			require.NoError(t, err)

			// Verify the link by fetching nar file by narinfo id
			nf2, err := db.GetNarFileByNarInfoID(context.Background(), ni.ID)
			require.NoError(t, err)

			assert.Equal(t, nf.ID, nf2.ID)
		})

		t.Run("duplicate link is idempotent", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash1 := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash1})
			require.NoError(t, err)

			hash2 := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash2,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			err = db.LinkNarInfoToNarFile(context.Background(), database.LinkNarInfoToNarFileParams{
				NarInfoID: ni.ID,
				NarFileID: nf.ID,
			})
			require.NoError(t, err)

			// Link again - should not error
			err = db.LinkNarInfoToNarFile(context.Background(), database.LinkNarInfoToNarFileParams{
				NarInfoID: ni.ID,
				NarFileID: nf.ID,
			})
			require.NoError(t, err, "duplicate link should be idempotent")
		})
	})

	t.Run("GetNarFileByNarInfoID", func(t *testing.T) {
		t.Parallel()

		t.Run("no link exists", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			_, err = db.GetNarFileByNarInfoID(context.Background(), ni.ID)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("link exists", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash1 := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash1})
			require.NoError(t, err)

			hash2 := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash2,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			err = db.LinkNarInfoToNarFile(context.Background(), database.LinkNarInfoToNarFileParams{
				NarInfoID: ni.ID,
				NarFileID: nf.ID,
			})
			require.NoError(t, err)

			nf2, err := db.GetNarFileByNarInfoID(context.Background(), ni.ID)
			require.NoError(t, err)

			assert.Equal(t, nf.ID, nf2.ID)
		})
	})

	t.Run("GetNarInfoHashesByNarFileID", func(t *testing.T) {
		t.Parallel()

		t.Run("no narinfos linked", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			hashes, err := db.GetNarInfoHashesByNarFileID(context.Background(), nf.ID)
			require.NoError(t, err)
			assert.Empty(t, hashes)
		})

		t.Run("multiple narinfos linked to one nar file", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash1 := helper.MustRandString(32, nil)

			ni1, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash1})
			require.NoError(t, err)

			hash2 := helper.MustRandString(32, nil)

			ni2, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash2})
			require.NoError(t, err)

			hash3 := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash3,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			err = db.LinkNarInfoToNarFile(context.Background(), database.LinkNarInfoToNarFileParams{
				NarInfoID: ni1.ID,
				NarFileID: nf.ID,
			})
			require.NoError(t, err)

			err = db.LinkNarInfoToNarFile(context.Background(), database.LinkNarInfoToNarFileParams{
				NarInfoID: ni2.ID,
				NarFileID: nf.ID,
			})
			require.NoError(t, err)

			hashes, err := db.GetNarInfoHashesByNarFileID(context.Background(), nf.ID)
			require.NoError(t, err)

			assert.Len(t, hashes, 2)
			assert.Contains(t, hashes, hash1)
			assert.Contains(t, hashes, hash2)
		})
	})

	t.Run("DeleteNarInfoByID", func(t *testing.T) {
		t.Parallel()

		t.Run("narinfo not existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			ra, err := db.DeleteNarInfoByID(context.Background(), 999999)
			require.NoError(t, err)
			assert.Zero(t, ra)
		})

		t.Run("narinfo existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			ra, err := db.DeleteNarInfoByID(context.Background(), ni.ID)
			require.NoError(t, err)
			assert.EqualValues(t, 1, ra)

			_, err = db.GetNarInfoByID(context.Background(), ni.ID)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})
	})

	t.Run("DeleteNarFileByID", func(t *testing.T) {
		t.Parallel()

		t.Run("nar file not existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			ra, err := db.DeleteNarFileByID(context.Background(), 999999)
			require.NoError(t, err)
			assert.Zero(t, ra)
		})

		t.Run("nar file existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			ra, err := db.DeleteNarFileByID(context.Background(), nf.ID)
			require.NoError(t, err)
			assert.EqualValues(t, 1, ra)

			_, err = db.GetNarFileByID(context.Background(), nf.ID)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})
	})

	t.Run("DeleteOrphanedNarFiles", func(t *testing.T) {
		t.Parallel()

		t.Run("no orphaned nar files", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash1 := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash1})
			require.NoError(t, err)

			hash2 := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash2,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			err = db.LinkNarInfoToNarFile(context.Background(), database.LinkNarInfoToNarFileParams{
				NarInfoID: ni.ID,
				NarFileID: nf.ID,
			})
			require.NoError(t, err)

			ra, err := db.DeleteOrphanedNarFiles(context.Background())
			require.NoError(t, err)
			assert.Zero(t, ra)
		})

		t.Run("orphaned nar files are deleted", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash1 := helper.MustRandString(32, nil)

			nf1, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash1,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			hash2 := helper.MustRandString(32, nil)

			nf2, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash2,
				Compression: "xz",
				FileSize:    456,
			})
			require.NoError(t, err)

			ra, err := db.DeleteOrphanedNarFiles(context.Background())
			require.NoError(t, err)
			assert.EqualValues(t, 2, ra)

			_, err = db.GetNarFileByID(context.Background(), nf1.ID)
			require.ErrorIs(t, err, database.ErrNotFound)

			_, err = db.GetNarFileByID(context.Background(), nf2.ID)
			require.ErrorIs(t, err, database.ErrNotFound)
		})
	})

	t.Run("DeleteOrphanedNarInfos", func(t *testing.T) {
		t.Parallel()

		t.Run("no orphaned narinfos", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash1 := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash1})
			require.NoError(t, err)

			hash2 := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash2,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			err = db.LinkNarInfoToNarFile(context.Background(), database.LinkNarInfoToNarFileParams{
				NarInfoID: ni.ID,
				NarFileID: nf.ID,
			})
			require.NoError(t, err)

			ra, err := db.DeleteOrphanedNarInfos(context.Background())
			require.NoError(t, err)
			assert.Zero(t, ra)
		})

		t.Run("orphaned narinfos are deleted", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash1 := helper.MustRandString(32, nil)

			ni1, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash1})
			require.NoError(t, err)

			hash2 := helper.MustRandString(32, nil)

			ni2, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash2})
			require.NoError(t, err)

			ra, err := db.DeleteOrphanedNarInfos(context.Background())
			require.NoError(t, err)
			assert.EqualValues(t, 2, ra)

			_, err = db.GetNarInfoByID(context.Background(), ni1.ID)
			require.ErrorIs(t, err, database.ErrNotFound)

			_, err = db.GetNarInfoByID(context.Background(), ni2.ID)
			require.ErrorIs(t, err, database.ErrNotFound)
		})
	})

	t.Run("GetNarInfoCount", func(t *testing.T) {
		t.Parallel()

		db := factory(t)

		initialCount, err := db.GetNarInfoCount(context.Background())
		require.NoError(t, err)

		for i := 0; i < 5; i++ {
			hash := helper.MustRandString(32, nil)

			_, err = db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)
		}

		count, err := db.GetNarInfoCount(context.Background())
		require.NoError(t, err)

		assert.Equal(t, initialCount+5, count)
	})

	t.Run("GetNarFileCount", func(t *testing.T) {
		t.Parallel()

		db := factory(t)

		initialCount, err := db.GetNarFileCount(context.Background())
		require.NoError(t, err)

		for i := 0; i < 3; i++ {
			hash := helper.MustRandString(32, nil)

			//nolint:gosec // G115: Safe conversion, i is small and controlled
			_, err = db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash,
				Compression: "xz",
				FileSize:    uint64(100 * (i + 1)),
			})
			require.NoError(t, err)
		}

		count, err := db.GetNarFileCount(context.Background())
		require.NoError(t, err)

		assert.Equal(t, initialCount+3, count)
	})

	t.Run("GetLeastUsedNarInfos", func(t *testing.T) {
		t.Parallel()

		db := factory(t)

		// Create 3 narinfos with different nar files of different sizes
		hash1 := helper.MustRandString(32, nil)

		ni1, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash1})
		require.NoError(t, err)

		nfHash1, err := helper.RandString(32, nil)
		require.NoError(t, err)

		nf1, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
			Hash:        nfHash1,
			Compression: "xz",
			FileSize:    100,
		})
		require.NoError(t, err)

		err = db.LinkNarInfoToNarFile(context.Background(), database.LinkNarInfoToNarFileParams{
			NarInfoID: ni1.ID,
			NarFileID: nf1.ID,
		})
		require.NoError(t, err)

		hash2, err := helper.RandString(32, nil)
		require.NoError(t, err)

		ni2, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash2})
		require.NoError(t, err)

		nfHash2, err := helper.RandString(32, nil)
		require.NoError(t, err)

		nf2, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
			Hash:        nfHash2,
			Compression: "xz",
			FileSize:    200,
		})
		require.NoError(t, err)

		err = db.LinkNarInfoToNarFile(context.Background(), database.LinkNarInfoToNarFileParams{
			NarInfoID: ni2.ID,
			NarFileID: nf2.ID,
		})
		require.NoError(t, err)

		hash3, err := helper.RandString(32, nil)
		require.NoError(t, err)

		ni3, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash3})
		require.NoError(t, err)

		nfHash3, err := helper.RandString(32, nil)
		require.NoError(t, err)

		nf3, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
			Hash:        nfHash3,
			Compression: "xz",
			FileSize:    300,
		})
		require.NoError(t, err)

		err = db.LinkNarInfoToNarFile(context.Background(), database.LinkNarInfoToNarFileParams{
			NarInfoID: ni3.ID,
			NarFileID: nf3.ID,
		})
		require.NoError(t, err)

		// Wait for one second to ensure that last_accessed_at is different from
		// created_at. This is needed because some databases (like SQLite) might
		// not have sub-second precision for CURRENT_TIMESTAMP.
		time.Sleep(time.Second)

		// Touch ni2 and ni3, making ni1 the least used
		_, err = db.TouchNarInfo(context.Background(), hash2)
		require.NoError(t, err)

		_, err = db.TouchNarInfo(context.Background(), hash3)
		require.NoError(t, err)

		// Ask for narinfos up to 100 bytes - should return only ni1
		narInfos, err := db.GetLeastUsedNarInfos(context.Background(), 100)
		require.NoError(t, err)

		if assert.Len(t, narInfos, 1) {
			assert.Equal(t, hash1, narInfos[0].Hash)
		}
	})

	t.Run("GetOrphanedNarFiles", func(t *testing.T) {
		t.Parallel()

		t.Run("no orphaned nar files", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash1 := helper.MustRandString(32, nil)

			ni, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash1})
			require.NoError(t, err)

			hash2 := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash2,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			err = db.LinkNarInfoToNarFile(context.Background(), database.LinkNarInfoToNarFileParams{
				NarInfoID: ni.ID,
				NarFileID: nf.ID,
			})
			require.NoError(t, err)

			orphaned, err := db.GetOrphanedNarFiles(context.Background())
			require.NoError(t, err)
			assert.Empty(t, orphaned)
		})

		t.Run("orphaned nar files are returned", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash1 := helper.MustRandString(32, nil)

			nf1, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash1,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			hash2 := helper.MustRandString(32, nil)

			nf2, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        hash2,
				Compression: "xz",
				FileSize:    456,
			})
			require.NoError(t, err)

			orphaned, err := db.GetOrphanedNarFiles(context.Background())
			require.NoError(t, err)

			assert.Len(t, orphaned, 2)
			foundIDs := []int64{orphaned[0].ID, orphaned[1].ID}
			assert.Contains(t, foundIDs, nf1.ID)
			assert.Contains(t, foundIDs, nf2.ID)
		})
	})

	t.Run("GetUnmigratedNarInfoHashes", func(t *testing.T) {
		t.Parallel()

		db := factory(t)

		hash1, err := helper.RandString(32, nil)
		require.NoError(t, err)

		_, err = db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash1})
		require.NoError(t, err)

		hash2, err := helper.RandString(32, nil)
		require.NoError(t, err)

		_, err = db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{
			Hash: hash2,
			URL:  sql.NullString{String: "nar/test.nar.xz", Valid: true},
		})
		require.NoError(t, err)

		unmigrated, err := db.GetUnmigratedNarInfoHashes(context.Background())
		require.NoError(t, err)

		assert.Contains(t, unmigrated, hash1)
		assert.NotContains(t, unmigrated, hash2)
	})

	t.Run("GetMigratedNarInfoHashes", func(t *testing.T) {
		t.Parallel()

		db := factory(t)

		hash1, err := helper.RandString(32, nil)
		require.NoError(t, err)

		_, err = db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash1})
		require.NoError(t, err)

		hash2, err := helper.RandString(32, nil)
		require.NoError(t, err)

		_, err = db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{
			Hash: hash2,
			URL:  sql.NullString{String: "nar/test.nar.xz", Valid: true},
		})
		require.NoError(t, err)

		migrated, err := db.GetMigratedNarInfoHashes(context.Background())
		require.NoError(t, err)

		assert.NotContains(t, migrated, hash1)
		assert.Contains(t, migrated, hash2)
	})

	t.Run("IsNarInfoMigrated", func(t *testing.T) {
		t.Parallel()

		t.Run("unmigrated narinfo", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			_, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			migrated, err := db.IsNarInfoMigrated(context.Background(), hash)
			require.NoError(t, err)

			assert.False(t, migrated)
		})

		t.Run("migrated narinfo", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			_, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{
				Hash: hash,
				URL:  sql.NullString{String: "nar/test.nar.xz", Valid: true},
			})
			require.NoError(t, err)

			migrated, err := db.IsNarInfoMigrated(context.Background(), hash)
			require.NoError(t, err)

			assert.True(t, migrated)
		})
	})

	t.Run("GetMigratedNarInfoHashesPaginated", func(t *testing.T) {
		t.Parallel()

		db := factory(t)

		// Create 5 migrated narinfos
		for i := 0; i < 5; i++ {
			hash := helper.MustRandString(32, nil)

			_, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{
				Hash: hash,
				URL:  sql.NullString{String: fmt.Sprintf("nar/test%d.nar.xz", i), Valid: true},
			})
			require.NoError(t, err)
		}

		// Create 2 unmigrated narinfos (should not appear in results)
		for i := 0; i < 2; i++ {
			hash := helper.MustRandString(32, nil)

			_, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)
		}

		// Get first page (limit 2, offset 0)
		page1, err := db.GetMigratedNarInfoHashesPaginated(context.Background(),
			database.GetMigratedNarInfoHashesPaginatedParams{
				Limit:  2,
				Offset: 0,
			})
		require.NoError(t, err)
		assert.Len(t, page1, 2)

		// Get second page (limit 2, offset 2)
		page2, err := db.GetMigratedNarInfoHashesPaginated(context.Background(),
			database.GetMigratedNarInfoHashesPaginatedParams{
				Limit:  2,
				Offset: 2,
			})
		require.NoError(t, err)
		assert.Len(t, page2, 2)

		// Get third page (limit 2, offset 4) - should have 1 item
		page3, err := db.GetMigratedNarInfoHashesPaginated(context.Background(),
			database.GetMigratedNarInfoHashesPaginatedParams{
				Limit:  2,
				Offset: 4,
			})
		require.NoError(t, err)
		assert.Len(t, page3, 1)
	})

	t.Run("CreateChunk", func(t *testing.T) {
		t.Parallel()

		t.Run("create new chunk", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			chunkHash := helper.MustRandString(32, nil)

			chunk, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash,
				Size: 1024,
			})
			require.NoError(t, err)

			assert.Equal(t, chunkHash, chunk.Hash)
			assert.EqualValues(t, 1024, chunk.Size)
		})

		t.Run("duplicate chunk is idempotent", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			chunkHash := helper.MustRandString(32, nil)

			chunk1, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash,
				Size: 1024,
			})
			require.NoError(t, err)

			chunk2, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash,
				Size: 1024,
			})
			require.NoError(t, err)

			assert.Equal(t, chunk1.ID, chunk2.ID)
		})
	})

	t.Run("GetChunkByHash", func(t *testing.T) {
		t.Parallel()

		t.Run("chunk not existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			hash := helper.MustRandString(32, nil)

			_, err := db.GetChunkByHash(context.Background(), hash)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("chunk existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			chunkHash := helper.MustRandString(32, nil)

			chunk1, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash,
				Size: 1024,
			})
			require.NoError(t, err)

			chunk2, err := db.GetChunkByHash(context.Background(), chunkHash)
			require.NoError(t, err)

			assert.Equal(t, chunk1.ID, chunk2.ID)
			assert.Equal(t, chunk1.Hash, chunk2.Hash)
		})
	})

	t.Run("GetChunkByID", func(t *testing.T) {
		t.Parallel()

		t.Run("chunk not existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			_, err := db.GetChunkByID(context.Background(), 999999)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("chunk existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			chunkHash := helper.MustRandString(32, nil)

			chunk1, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash,
				Size: 1024,
			})
			require.NoError(t, err)

			chunk2, err := db.GetChunkByID(context.Background(), chunk1.ID)
			require.NoError(t, err)

			assert.Equal(t, chunk1.ID, chunk2.ID)
			assert.Equal(t, chunk1.Hash, chunk2.Hash)
		})
	})

	t.Run("LinkNarFileToChunk", func(t *testing.T) {
		t.Parallel()

		t.Run("successful linking", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1024,
			})
			require.NoError(t, err)

			chunkHash := helper.MustRandString(32, nil)

			chunk, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash,
				Size: 512,
			})
			require.NoError(t, err)

			err = db.LinkNarFileToChunk(context.Background(), database.LinkNarFileToChunkParams{
				NarFileID:  nf.ID,
				ChunkID:    chunk.ID,
				ChunkIndex: 0,
			})
			require.NoError(t, err)

			chunks, err := db.GetChunksByNarFileID(context.Background(), nf.ID)
			require.NoError(t, err)

			if assert.Len(t, chunks, 1) {
				assert.Equal(t, chunk.ID, chunks[0].ID)
			}
		})

		t.Run("duplicate link is idempotent", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1024,
			})
			require.NoError(t, err)

			chunkHash := helper.MustRandString(32, nil)

			chunk, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash,
				Size: 512,
			})
			require.NoError(t, err)

			err = db.LinkNarFileToChunk(context.Background(), database.LinkNarFileToChunkParams{
				NarFileID:  nf.ID,
				ChunkID:    chunk.ID,
				ChunkIndex: 0,
			})
			require.NoError(t, err)

			// Link again - should not error
			err = db.LinkNarFileToChunk(context.Background(), database.LinkNarFileToChunkParams{
				NarFileID:  nf.ID,
				ChunkID:    chunk.ID,
				ChunkIndex: 0,
			})
			require.NoError(t, err, "duplicate link should be idempotent")
		})
	})

	t.Run("LinkNarFileToChunks", func(t *testing.T) {
		t.Parallel()

		t.Run("successful bulk linking of multiple chunks", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    3072,
			})
			require.NoError(t, err)

			// Create 3 chunks
			chunkIDs := make([]int64, 3)
			chunkIndices := make([]int64, 3)

			for i := 0; i < 3; i++ {
				chunkHash, err := helper.RandString(32, nil)
				require.NoError(t, err)

				chunk, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
					Hash: chunkHash,
					Size: 1024,
				})
				require.NoError(t, err)

				chunkIDs[i] = chunk.ID
				chunkIndices[i] = int64(i)
			}

			// Link all chunks in bulk
			err = db.LinkNarFileToChunks(context.Background(), database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    chunkIDs,
				ChunkIndex: chunkIndices,
			})
			require.NoError(t, err)

			// Verify all chunks are linked
			chunks, err := db.GetChunksByNarFileID(context.Background(), nf.ID)
			require.NoError(t, err)

			if assert.Len(t, chunks, 3) {
				// Verify order is maintained
				for i := 0; i < 3; i++ {
					assert.Equal(t, chunkIDs[i], chunks[i].ID)
				}
			}
		})

		t.Run("bulk link with single chunk", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1024,
			})
			require.NoError(t, err)

			chunkHash := helper.MustRandString(32, nil)

			chunk, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash,
				Size: 1024,
			})
			require.NoError(t, err)

			// Link single chunk using bulk operation
			err = db.LinkNarFileToChunks(context.Background(), database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    []int64{chunk.ID},
				ChunkIndex: []int64{0},
			})
			require.NoError(t, err)

			chunks, err := db.GetChunksByNarFileID(context.Background(), nf.ID)
			require.NoError(t, err)

			if assert.Len(t, chunks, 1) {
				assert.Equal(t, chunk.ID, chunks[0].ID)
			}
		})

		t.Run("bulk link with empty arrays", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    0,
			})
			require.NoError(t, err)

			// Link with empty arrays - should not error
			err = db.LinkNarFileToChunks(context.Background(), database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    []int64{},
				ChunkIndex: []int64{},
			})
			require.NoError(t, err)

			chunks, err := db.GetChunksByNarFileID(context.Background(), nf.ID)
			require.NoError(t, err)
			assert.Empty(t, chunks)
		})

		t.Run("duplicate bulk links are idempotent", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    2048,
			})
			require.NoError(t, err)

			// Create 2 chunks
			chunkIDs := make([]int64, 2)
			chunkIndices := make([]int64, 2)

			for i := 0; i < 2; i++ {
				chunkHash, err := helper.RandString(32, nil)
				require.NoError(t, err)

				chunk, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
					Hash: chunkHash,
					Size: 1024,
				})
				require.NoError(t, err)

				chunkIDs[i] = chunk.ID
				chunkIndices[i] = int64(i)
			}

			// Link chunks first time
			err = db.LinkNarFileToChunks(context.Background(), database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    chunkIDs,
				ChunkIndex: chunkIndices,
			})
			require.NoError(t, err)

			// Link same chunks again - should be idempotent
			err = db.LinkNarFileToChunks(context.Background(), database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    chunkIDs,
				ChunkIndex: chunkIndices,
			})
			require.NoError(t, err, "duplicate bulk link should be idempotent")

			// Verify we still have only 2 chunks
			chunks, err := db.GetChunksByNarFileID(context.Background(), nf.ID)
			require.NoError(t, err)
			assert.Len(t, chunks, 2)
		})

		t.Run("bulk link maintains correct chunk order", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    5120,
			})
			require.NoError(t, err)

			// Create 5 chunks
			numChunks := 5
			chunkIDs := make([]int64, numChunks)
			chunkIndices := make([]int64, numChunks)

			for i := 0; i < numChunks; i++ {
				chunkHash, err := helper.RandString(32, nil)
				require.NoError(t, err)

				chunk, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
					Hash: chunkHash,
					Size: 1024,
				})
				require.NoError(t, err)

				chunkIDs[i] = chunk.ID
				chunkIndices[i] = int64(i)
			}

			// Link all chunks in bulk
			err = db.LinkNarFileToChunks(context.Background(), database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    chunkIDs,
				ChunkIndex: chunkIndices,
			})
			require.NoError(t, err)

			// Verify chunks are in correct order
			chunks, err := db.GetChunksByNarFileID(context.Background(), nf.ID)
			require.NoError(t, err)

			if assert.Len(t, chunks, numChunks) {
				for i := 0; i < numChunks; i++ {
					assert.Equal(t, chunkIDs[i], chunks[i].ID, "chunk at index %d should match", i)
				}
			}
		})

		t.Run("bulk link with non-sequential indices", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    3072,
			})
			require.NoError(t, err)

			// Create 3 chunks with non-sequential indices (e.g., 10, 20, 30)
			chunkIDs := make([]int64, 3)
			chunkIndices := []int64{10, 20, 30}

			for i := 0; i < 3; i++ {
				chunkHash, err := helper.RandString(32, nil)
				require.NoError(t, err)

				chunk, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
					Hash: chunkHash,
					Size: 1024,
				})
				require.NoError(t, err)

				chunkIDs[i] = chunk.ID
			}

			// Link chunks with non-sequential indices
			err = db.LinkNarFileToChunks(context.Background(), database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    chunkIDs,
				ChunkIndex: chunkIndices,
			})
			require.NoError(t, err)

			// Verify chunks are linked
			chunks, err := db.GetChunksByNarFileID(context.Background(), nf.ID)
			require.NoError(t, err)

			if assert.Len(t, chunks, 3) {
				// Chunks should be returned in order by index
				for i := 0; i < 3; i++ {
					assert.Equal(t, chunkIDs[i], chunks[i].ID)
				}
			}
		})

		t.Run("bulk link mixed with individual links", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    4096,
			})
			require.NoError(t, err)

			// Create 4 chunks
			var allChunkIDs []int64

			for i := 0; i < 4; i++ {
				chunkHash, err := helper.RandString(32, nil)
				require.NoError(t, err)

				chunk, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
					Hash: chunkHash,
					Size: 1024,
				})
				require.NoError(t, err)

				allChunkIDs = append(allChunkIDs, chunk.ID)
			}

			// Link first 2 chunks individually
			for i := 0; i < 2; i++ {
				err = db.LinkNarFileToChunk(context.Background(), database.LinkNarFileToChunkParams{
					NarFileID:  nf.ID,
					ChunkID:    allChunkIDs[i],
					ChunkIndex: int64(i),
				})
				require.NoError(t, err)
			}

			// Link last 2 chunks in bulk
			err = db.LinkNarFileToChunks(context.Background(), database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    allChunkIDs[2:],
				ChunkIndex: []int64{2, 3},
			})
			require.NoError(t, err)

			// Verify all chunks are linked in correct order
			chunks, err := db.GetChunksByNarFileID(context.Background(), nf.ID)
			require.NoError(t, err)

			if assert.Len(t, chunks, 4) {
				for i := 0; i < 4; i++ {
					assert.Equal(t, allChunkIDs[i], chunks[i].ID)
				}
			}
		})

		t.Run("bulk link with mismatched arrays", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash := helper.MustRandString(32, nil)
			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1024,
			})
			require.NoError(t, err)

			chunkHash := helper.MustRandString(32, nil)
			chunk, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash,
				Size: 1024,
			})
			require.NoError(t, err)

			err = db.LinkNarFileToChunks(context.Background(), database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    []int64{chunk.ID},
				ChunkIndex: []int64{}, // Mismatched length
			})
			require.ErrorIs(t, err, database.ErrMismatchedSlices)
		})
	})

	t.Run("GetChunksByNarFileID", func(t *testing.T) {
		t.Parallel()

		t.Run("no chunks linked", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1024,
			})
			require.NoError(t, err)

			chunks, err := db.GetChunksByNarFileID(context.Background(), nf.ID)
			require.NoError(t, err)
			assert.Empty(t, chunks)
		})

		t.Run("multiple chunks linked in order", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1536,
			})
			require.NoError(t, err)

			// Create 3 chunks
			var chunkIDs []int64

			for i := 0; i < 3; i++ {
				chunkHash, err := helper.RandString(32, nil)
				require.NoError(t, err)

				chunk, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
					Hash: chunkHash,
					Size: 512,
				})
				require.NoError(t, err)

				chunkIDs = append(chunkIDs, chunk.ID)

				err = db.LinkNarFileToChunk(context.Background(), database.LinkNarFileToChunkParams{
					NarFileID:  nf.ID,
					ChunkID:    chunk.ID,
					ChunkIndex: int64(i),
				})
				require.NoError(t, err)
			}

			chunks, err := db.GetChunksByNarFileID(context.Background(), nf.ID)
			require.NoError(t, err)

			if assert.Len(t, chunks, 3) {
				// Verify order is maintained
				assert.Equal(t, chunkIDs[0], chunks[0].ID)
				assert.Equal(t, chunkIDs[1], chunks[1].ID)
				assert.Equal(t, chunkIDs[2], chunks[2].ID)
			}
		})
	})

	t.Run("GetChunkByNarFileIDAndIndex", func(t *testing.T) {
		t.Parallel()

		t.Run("chunk not existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1024,
			})
			require.NoError(t, err)

			_, err = db.GetChunkByNarFileIDAndIndex(context.Background(),
				database.GetChunkByNarFileIDAndIndexParams{
					NarFileID:  nf.ID,
					ChunkIndex: 0,
				})
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("chunk existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1024,
			})
			require.NoError(t, err)

			chunkHash := helper.MustRandString(32, nil)

			chunk, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash,
				Size: 512,
			})
			require.NoError(t, err)

			err = db.LinkNarFileToChunk(context.Background(), database.LinkNarFileToChunkParams{
				NarFileID:  nf.ID,
				ChunkID:    chunk.ID,
				ChunkIndex: 0,
			})
			require.NoError(t, err)

			chunk2, err := db.GetChunkByNarFileIDAndIndex(context.Background(),
				database.GetChunkByNarFileIDAndIndexParams{
					NarFileID:  nf.ID,
					ChunkIndex: 0,
				})
			require.NoError(t, err)

			assert.Equal(t, chunk.ID, chunk2.ID)
		})
	})

	t.Run("GetTotalChunkSize", func(t *testing.T) {
		t.Parallel()

		db := factory(t)

		initialSize, err := db.GetTotalChunkSize(context.Background())
		require.NoError(t, err)

		var totalAdded int64

		for i := 0; i < 3; i++ {
			chunkHash := helper.MustRandString(32, nil)

			//nolint:gosec // G115: Safe conversion, i is small and controlled
			size := uint32(512 * (i + 1))
			totalAdded += int64(size)

			_, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash,
				Size: size,
			})
			require.NoError(t, err)
		}

		totalSize, err := db.GetTotalChunkSize(context.Background())
		require.NoError(t, err)

		assert.Equal(t, initialSize+totalAdded, totalSize)
	})

	t.Run("GetChunkCount", func(t *testing.T) {
		t.Parallel()

		db := factory(t)

		initialCount, err := db.GetChunkCount(context.Background())
		require.NoError(t, err)

		for i := 0; i < 4; i++ {
			chunkHash := helper.MustRandString(32, nil)

			_, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash,
				Size: 512,
			})
			require.NoError(t, err)
		}

		count, err := db.GetChunkCount(context.Background())
		require.NoError(t, err)

		assert.Equal(t, initialCount+4, count)
	})

	t.Run("GetOrphanedChunks", func(t *testing.T) {
		t.Parallel()

		t.Run("no orphaned chunks", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			narHash := helper.MustRandString(32, nil)

			nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    512,
			})
			require.NoError(t, err)

			chunkHash := helper.MustRandString(32, nil)

			chunk, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash,
				Size: 512,
			})
			require.NoError(t, err)

			err = db.LinkNarFileToChunk(context.Background(), database.LinkNarFileToChunkParams{
				NarFileID:  nf.ID,
				ChunkID:    chunk.ID,
				ChunkIndex: 0,
			})
			require.NoError(t, err)

			orphaned, err := db.GetOrphanedChunks(context.Background())
			require.NoError(t, err)
			assert.Empty(t, orphaned)
		})

		t.Run("orphaned chunks are returned", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			chunkHash1 := helper.MustRandString(32, nil)

			chunk1, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash1,
				Size: 512,
			})
			require.NoError(t, err)

			chunkHash2 := helper.MustRandString(32, nil)

			chunk2, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash2,
				Size: 1024,
			})
			require.NoError(t, err)

			orphaned, err := db.GetOrphanedChunks(context.Background())
			require.NoError(t, err)

			assert.Len(t, orphaned, 2)
			foundIDs := []int64{orphaned[0].ID, orphaned[1].ID}
			assert.Contains(t, foundIDs, chunk1.ID)
			assert.Contains(t, foundIDs, chunk2.ID)
		})
	})

	t.Run("DeleteChunkByID", func(t *testing.T) {
		t.Parallel()

		t.Run("chunk not existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			err := db.DeleteChunkByID(context.Background(), 999999)
			require.NoError(t, err)
		})

		t.Run("chunk existing", func(t *testing.T) {
			t.Parallel()

			db := factory(t)

			chunkHash := helper.MustRandString(32, nil)

			chunk, err := db.CreateChunk(context.Background(), database.CreateChunkParams{
				Hash: chunkHash,
				Size: 512,
			})
			require.NoError(t, err)

			err = db.DeleteChunkByID(context.Background(), chunk.ID)
			require.NoError(t, err)

			_, err = db.GetChunkByID(context.Background(), chunk.ID)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})
	})

	t.Run("UpdateNarFileTotalChunks", func(t *testing.T) {
		t.Parallel()

		db := factory(t)

		narHash, err := helper.RandString(32, nil)
		require.NoError(t, err)

		nf, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
			Hash:        narHash,
			Compression: "xz",
			FileSize:    1536,
		})
		require.NoError(t, err)

		assert.EqualValues(t, 0, nf.TotalChunks)

		err = db.UpdateNarFileTotalChunks(context.Background(), database.UpdateNarFileTotalChunksParams{
			TotalChunks: 3,
			ID:          nf.ID,
		})
		require.NoError(t, err)

		nf2, err := db.GetNarFileByID(context.Background(), nf.ID)
		require.NoError(t, err)

		assert.EqualValues(t, 3, nf2.TotalChunks)
	})

	t.Run("GetNarInfoHashesToChunk", func(t *testing.T) {
		t.Parallel()

		db := factory(t)

		// Create a migrated narinfo without chunks
		hash1, err := helper.RandString(32, nil)
		require.NoError(t, err)

		_, err = db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{
			Hash: hash1,
			URL:  sql.NullString{String: "nar/test1.nar.xz", Valid: true},
		})
		require.NoError(t, err)

		// Create a migrated narinfo with chunks (total_chunks > 0)
		hash2, err := helper.RandString(32, nil)
		require.NoError(t, err)

		ni2, err := db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{
			Hash: hash2,
			URL:  sql.NullString{String: "nar/test2.nar.xz", Valid: true},
		})
		require.NoError(t, err)

		nfHash2, err := helper.RandString(32, nil)
		require.NoError(t, err)

		nf2, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
			Hash:        nfHash2,
			Compression: "xz",
			FileSize:    1024,
		})
		require.NoError(t, err)

		err = db.LinkNarInfoToNarFile(context.Background(), database.LinkNarInfoToNarFileParams{
			NarInfoID: ni2.ID,
			NarFileID: nf2.ID,
		})
		require.NoError(t, err)

		err = db.UpdateNarFileTotalChunks(context.Background(), database.UpdateNarFileTotalChunksParams{
			TotalChunks: 2,
			ID:          nf2.ID,
		})
		require.NoError(t, err)

		// Create an unmigrated narinfo (should not appear)
		hash3, err := helper.RandString(32, nil)
		require.NoError(t, err)

		_, err = db.CreateNarInfo(context.Background(), database.CreateNarInfoParams{Hash: hash3})
		require.NoError(t, err)

		// Get narinfos to chunk - should only return ni1
		toChunk, err := db.GetNarInfoHashesToChunk(context.Background())
		require.NoError(t, err)

		assert.Len(t, toChunk, 1)

		if len(toChunk) > 0 {
			assert.Equal(t, hash1, toChunk[0].Hash)
			assert.True(t, toChunk[0].URL.Valid)
		}
	})

	t.Run("GetNarFilesToChunk", func(t *testing.T) {
		t.Parallel()

		db := factory(t)

		// Create some nar files
		hash1 := helper.MustRandString(32, nil)
		hash2 := helper.MustRandString(32, nil)
		hash3 := helper.MustRandString(32, nil)

		nf1, err := db.CreateNarFile(context.Background(), database.CreateNarFileParams{
			Hash:        hash1,
			Compression: "xz",
			FileSize:    1024,
			TotalChunks: 0, // Unchunked
		})
		require.NoError(t, err)

		_, err = db.CreateNarFile(context.Background(), database.CreateNarFileParams{
			Hash:        hash2,
			Compression: "xz",
			FileSize:    1024,
			TotalChunks: 0, // Unchunked
		})
		require.NoError(t, err)

		_, err = db.CreateNarFile(context.Background(), database.CreateNarFileParams{
			Hash:        hash3,
			Compression: "xz",
			FileSize:    1024,
			TotalChunks: 5, // Already chunked
		})
		require.NoError(t, err)

		// Check count
		count, err := db.GetNarFilesToChunkCount(context.Background())
		require.NoError(t, err)
		assert.EqualValues(t, 2, count)

		// Get files
		toChunk, err := db.GetNarFilesToChunk(context.Background())
		require.NoError(t, err)
		assert.Len(t, toChunk, 2)

		hashes := []string{toChunk[0].Hash, toChunk[1].Hash}
		assert.Contains(t, hashes, hash1)
		assert.Contains(t, hashes, hash2)
		assert.NotContains(t, hashes, hash3)

		// Update one of them to be chunked
		err = db.UpdateNarFileTotalChunks(context.Background(), database.UpdateNarFileTotalChunksParams{
			TotalChunks: 10,
			ID:          nf1.ID,
		})
		require.NoError(t, err)

		// Check count again
		count, err = db.GetNarFilesToChunkCount(context.Background())
		require.NoError(t, err)
		assert.EqualValues(t, 1, count)

		// Get files again
		toChunk, err = db.GetNarFilesToChunk(context.Background())
		require.NoError(t, err)
		assert.Len(t, toChunk, 1)
		assert.Equal(t, hash2, toChunk[0].Hash)
	})

	t.Run("GetCompressedNarInfos", func(t *testing.T) {
		t.Parallel()

		db := factory(t)
		ctx := context.Background()

		// 1. Create compressed narinfos
		hash1 := "hash1"
		_, err := db.CreateNarInfo(ctx, database.CreateNarInfoParams{
			Hash:        hash1,
			Compression: sql.NullString{String: "zstd", Valid: true},
		})
		require.NoError(t, err)

		hash2 := "hash2"
		_, err = db.CreateNarInfo(ctx, database.CreateNarInfoParams{
			Hash:        hash2,
			Compression: sql.NullString{String: "xz", Valid: true},
		})
		require.NoError(t, err)

		// 2. Create uncompressed narinfos
		hash3 := "hash3"
		_, err = db.CreateNarInfo(ctx, database.CreateNarInfoParams{
			Hash:        hash3,
			Compression: sql.NullString{String: "none", Valid: true},
		})
		require.NoError(t, err)

		hash4 := "hash4"
		_, err = db.CreateNarInfo(ctx, database.CreateNarInfoParams{
			Hash:        hash4,
			Compression: sql.NullString{String: "", Valid: true},
		})
		require.NoError(t, err)

		// 3. Get compressed narinfos
		res, err := db.GetCompressedNarInfos(ctx, database.GetCompressedNarInfosParams{
			Limit:  10,
			Offset: 0,
		})
		require.NoError(t, err)

		hashes := make([]string, 0, len(res))
		for _, ni := range res {
			hashes = append(hashes, ni.Hash)
		}

		assert.Contains(t, hashes, hash1)
		assert.Contains(t, hashes, hash2)
		assert.NotContains(t, hashes, hash3)
		assert.NotContains(t, hashes, hash4)
	})

	t.Run("GetOldCompressedNarFiles", func(t *testing.T) {
		t.Parallel()

		db := factory(t)
		ctx := context.Background()

		// 1. Create compressed nar-files
		hash1 := "hash1"
		_, err := db.CreateNarFile(ctx, database.CreateNarFileParams{
			Hash:        hash1,
			Compression: "zstd",
			FileSize:    100,
		})
		require.NoError(t, err)

		hash2 := "hash2"
		_, err = db.CreateNarFile(ctx, database.CreateNarFileParams{
			Hash:        hash2,
			Compression: "xz",
			FileSize:    200,
		})
		require.NoError(t, err)

		// 2. Create uncompressed nar-files
		hash3 := "hash3"
		_, err = db.CreateNarFile(ctx, database.CreateNarFileParams{
			Hash:        hash3,
			Compression: "none",
			FileSize:    300,
		})
		require.NoError(t, err)

		hash4 := "hash4"
		_, err = db.CreateNarFile(ctx, database.CreateNarFileParams{
			Hash:        hash4,
			Compression: "",
			FileSize:    400,
		})
		require.NoError(t, err)

		// Give it a tiny bit of time to make sure created_at is captured
		time.Sleep(10 * time.Millisecond)

		// 3. Get compressed nar-files
		res, err := db.GetOldCompressedNarFiles(ctx, database.GetOldCompressedNarFilesParams{
			CreatedAt: time.Now().UTC().Add(time.Hour),
			Limit:     10,
			Offset:    0,
		})
		require.NoError(t, err)

		hashes := make([]string, 0, len(res))
		for _, nf := range res {
			hashes = append(hashes, nf.Hash)
		}

		assert.Contains(t, hashes, hash1)
		assert.Contains(t, hashes, hash2)
		assert.NotContains(t, hashes, hash3)
		assert.NotContains(t, hashes, hash4)

		// 4. Test with timestamp before insertion
		res, err = db.GetOldCompressedNarFiles(ctx, database.GetOldCompressedNarFilesParams{
			CreatedAt: time.Now().Add(-1 * time.Hour),
			Limit:     10,
			Offset:    0,
		})
		require.NoError(t, err)
		assert.Empty(t, res)
	})
}
