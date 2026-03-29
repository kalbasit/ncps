package database_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// querierFactory is a function that returns a clean, ready-to-use *bun.DB and
// it takes care of cleaning up once the test is done.
type querierFactory func(t *testing.T) (*bun.DB, func())

func runComplianceSuite(t *testing.T, factory querierFactory) {
	t.Helper()

	t.Run("GetConfigByKey", func(t *testing.T) {
		t.Parallel()

		t.Run("key not existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			key := testhelper.MustRandString(32)

			_, err := database.GetConfigByKey(context.Background(), db, key)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("key existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			key := testhelper.MustRandString(32)

			value := testhelper.MustRandString(32)

			conf1, err := database.CreateConfig(context.Background(), db, key, value)
			require.NoError(t, err)

			conf2, err := database.GetConfigByKey(context.Background(), db, key)
			require.NoError(t, err)

			assert.Equal(t, conf1, conf2)
		})
	})

	t.Run("GetNarInfoByHash", func(t *testing.T) {
		t.Parallel()

		t.Run("narinfo not existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			_, err := database.GetNarInfoByHash(context.Background(), db, hash)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("narinfo existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			ni1, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			ni2, err := database.GetNarInfoByHash(context.Background(), db, hash)
			require.NoError(t, err)

			assert.Equal(t, ni1.Hash, ni2.Hash)
		})
	})

	t.Run("InsertNarInfo", func(t *testing.T) {
		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		t.Run("inserting one record", func(t *testing.T) {
			hash := testhelper.MustRandString(32)

			nio, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			rows, err := db.DB.QueryContext(
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
				assert.True(t, nims[0].UpdatedAt.IsZero())
				assert.Equal(t, nims[0].CreatedAt, nims[0].LastAccessedAt.Time)
			}
		})

		t.Run("hash is unique", func(t *testing.T) {
			hash := testhelper.MustRandString(32)

			ni1, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			ni2, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			assert.Equal(t, ni1.ID, ni2.ID)
		})

		t.Run("can write many narinfos", func(t *testing.T) {
			var wg sync.WaitGroup

			const numWrites = 10000

			errC := make(chan error, numWrites)

			for range numWrites {
				wg.Go(func() {
					hash := testhelper.MustRandString(128)

					if _, err := database.CreateNarInfo(
						context.Background(), db, database.CreateNarInfoParams{Hash: hash},
					); err != nil {
						errC <- fmt.Errorf("error creating the narinfo record: %w", err)
					}
				})
			}

			wg.Wait()
			close(errC)

			for err := range errC {
				assert.NoError(t, err)
			}
		})

		t.Run("CreateNarInfoUpdateFromPlaceholder", func(t *testing.T) {
			hash := testhelper.MustRandString(32)

			// 1. Create a placeholder (url IS NULL)
			_, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{
				Hash: hash,
			})
			require.NoError(t, err)

			// 2. Perform the "migration" upsert
			fileHash := "sha256:1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri"
			narURL := "nar/1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri.nar.xz"
			_, err = database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{
				Hash:     hash,
				URL:      sql.NullString{String: narURL, Valid: true},
				FileHash: sql.NullString{String: fileHash, Valid: true},
			})
			require.NoError(t, err)

			// 3. Verify it was correctly updated
			ni, err := database.GetNarInfoByHash(context.Background(), db, hash)
			require.NoError(t, err)

			assert.True(t, ni.URL.Valid)
			assert.Equal(t, narURL, ni.URL.String)
			assert.True(t, ni.FileHash.Valid)
			assert.Equal(t, fileHash, ni.FileHash.String)
		})
	})

	t.Run("UpdateNarInfo", func(t *testing.T) {
		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		ctx := context.Background()

		t.Run("updating an existing narinfo", func(t *testing.T) {
			hash := testhelper.MustRandString(32)

			// 1. Create a narinfo
			_, err := database.CreateNarInfo(ctx, db, database.CreateNarInfoParams{
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

			updated, err := database.UpdateNarInfo(ctx, db, params)
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
			ni, err := database.GetNarInfoByHash(ctx, db, hash)
			require.NoError(t, err)

			verifyFields(t, ni)
		})

		t.Run("updating a non-existing narinfo", func(t *testing.T) {
			hash := testhelper.MustRandString(32)
			params := database.UpdateNarInfoParams{
				Hash: hash,
			}

			_, err := database.UpdateNarInfo(ctx, db, params)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})
	})

	t.Run("TouchNarInfo", func(t *testing.T) {
		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		t.Run("narinfo not existing", func(t *testing.T) {
			hash := testhelper.MustRandString(32)

			ra, err := database.TouchNarInfo(context.Background(), db, hash)
			require.NoError(t, err)

			assert.Zero(t, ra)
		})

		t.Run("narinfo existing", func(t *testing.T) {
			hash := testhelper.MustRandString(32)

			_, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			t.Run("confirm created_at == last_accessed_at, and no updated_at", func(t *testing.T) {
				rows, err := db.DB.QueryContext(
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
				assert.True(t, nims[0].UpdatedAt.IsZero())
			})

			t.Run("touch the narinfo", func(t *testing.T) {
				time.Sleep(time.Second)

				ra, err := database.TouchNarInfo(context.Background(), db, hash)
				require.NoError(t, err)
				assert.EqualValues(t, 1, ra)
			})

			t.Run("confirm created_at != last_accessed_at and updated_at == last_accessed_at", func(t *testing.T) {
				rows, err := db.DB.QueryContext(
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

				if assert.False(t, nims[0].UpdatedAt.IsZero()) {
					assert.Equal(t, nims[0].UpdatedAt.Time, nims[0].LastAccessedAt.Time)
				}
			})
		})
	})

	t.Run("DeleteNarInfo", func(t *testing.T) {
		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		t.Run("narinfo not existing", func(t *testing.T) {
			hash := testhelper.MustRandString(32)

			ra, err := database.DeleteNarInfoByHash(context.Background(), db, hash)
			require.NoError(t, err)

			assert.Zero(t, ra)
		})

		t.Run("narinfo existing", func(t *testing.T) {
			hash := testhelper.MustRandString(32)

			t.Run("create the narinfo", func(t *testing.T) {
				_, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
				require.NoError(t, err)
			})

			t.Run("delete the narinfo", func(t *testing.T) {
				time.Sleep(time.Second)

				ra, err := database.DeleteNarInfoByHash(context.Background(), db, hash)
				require.NoError(t, err)

				assert.EqualValues(t, 1, ra)
			})

			t.Run("confirm it has been removed", func(t *testing.T) {
				rows, err := db.DB.QueryContext(
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

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			key := testhelper.MustRandString(32)

			value := testhelper.MustRandString(32)

			createdConf, err := database.CreateConfig(context.Background(), db, key, value)
			require.NoError(t, err)

			fetchedConf, err := database.GetConfigByKey(context.Background(), db, key)
			require.NoError(t, err)

			assert.Equal(t, createdConf, fetchedConf)
		})

		t.Run("duplicate key", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			key := testhelper.MustRandString(32)

			value := testhelper.MustRandString(32)

			_, err := database.CreateConfig(context.Background(), db, key, value)
			require.NoError(t, err)

			// Try to create again with the same key
			_, err = database.CreateConfig(context.Background(), db, key, "another value")
			assert.True(t, database.IsDuplicateKeyError(err))
		})
	})

	t.Run("GetNarFileByHashAndCompressionAndQuery", func(t *testing.T) {
		t.Parallel()

		t.Run("can store multiple representations of same hash", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf1, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: nar.CompressionTypeXz.String(),
				Query:       "hash=123&key=value",
				FileSize:    123,
			})
			require.NoError(t, err)

			nf2, err := database.GetNarFileByHashAndCompressionAndQuery(
				context.Background(),
				db,
				narHash,
				nar.CompressionTypeXz.String(),
				"hash=123&key=value",
			)
			require.NoError(t, err)

			assert.Equal(t, nf1.Hash, nf2.Hash)
			assert.Equal(t, nf1.Compression, nf2.Compression)
			assert.Equal(t, nf1.Query, nf2.Query)

			// Store another one with different compression
			nf3, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: nar.CompressionTypeNone.String(),
				Query:       "hash=123&key=value",
				FileSize:    456,
			})
			require.NoError(t, err)
			assert.NotEqual(t, nf1.ID, nf3.ID)

			// Store another one with different query
			nf4, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
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

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			_, err := database.GetNarFileByHashAndCompressionAndQuery(
				context.Background(),
				db,
				narHash,
				"xz",
				"",
			)
			require.Error(t, err)
			assert.True(t, database.IsNotFoundError(err))
		})

		t.Run("nar existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf1, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "zstd",
				FileSize:    123,
			})
			require.NoError(t, err)

			nf2, err := database.GetNarFileByHashAndCompressionAndQuery(
				context.Background(),
				db,
				narHash,
				"zstd",
				"",
			)
			require.NoError(t, err)

			assert.Equal(t, nf1.ID, nf2.ID)
			assert.Equal(t, nf1.Hash, nf2.Hash)
		})
	})

	t.Run("InsertNar", func(t *testing.T) {
		db, cleanup := factory(t)
		t.Cleanup(cleanup)

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
				_, err := db.DB.ExecContext(context.Background(), "DELETE FROM nar_files")
				require.NoError(t, err)

				t.Run("inserting one record", func(t *testing.T) {
					hash, err := testhelper.RandString(32)
					require.NoError(t, err)

					narFile, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
						Hash:        hash,
						Compression: compression.String(),
						FileSize:    123,
					})
					require.NoError(t, err)

					const query = `
 				SELECT id, hash, compression, file_size, created_at, updated_at, last_accessed_at, query
 				FROM nar_files
 				`

					rows, err := db.DB.QueryContext(context.Background(), query)
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
						assert.True(t, narFiles[0].UpdatedAt.IsZero())
						assert.Equal(t, narFiles[0].CreatedAt, narFiles[0].LastAccessedAt.Time)
					}
				})

				t.Run("upsert on duplicate hash", func(t *testing.T) {
					hash, err := testhelper.RandString(32)
					require.NoError(t, err)

					nf1, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
						Hash:        hash,
						Compression: "",
						FileSize:    123,
					})
					require.NoError(t, err)

					nf2, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
						Hash:        hash,
						Compression: "",
						FileSize:    123,
					})
					require.NoError(t, err)

					nf3, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
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
		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		t.Run("nar not existing", func(t *testing.T) {
			hash := testhelper.MustRandString(32)

			ra, err := database.TouchNarFile(context.Background(), db, database.TouchNarFileParams{
				Hash: hash,
			})
			require.NoError(t, err)

			assert.Zero(t, ra)
		})

		t.Run("nar existing", func(t *testing.T) {
			hash := testhelper.MustRandString(32)

			t.Run("create the nar", func(t *testing.T) {
				_, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
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

				rows, err := db.DB.QueryContext(context.Background(), query)
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
					assert.True(t, narFiles[0].UpdatedAt.IsZero())
					assert.Equal(t, narFiles[0].CreatedAt, narFiles[0].LastAccessedAt.Time)
				}
			})

			t.Run("touch the nar", func(t *testing.T) {
				time.Sleep(time.Second)

				ra, err := database.TouchNarFile(context.Background(), db, database.TouchNarFileParams{
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

				rows, err := db.DB.QueryContext(context.Background(), query)
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

					if assert.False(t, narFiles[0].UpdatedAt.IsZero()) {
						assert.Equal(t, narFiles[0].UpdatedAt.Time, narFiles[0].LastAccessedAt.Time)
					}
				}
			})
		})
	})

	t.Run("DeleteNar", func(t *testing.T) {
		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		t.Run("nar not existing", func(t *testing.T) {
			hash := testhelper.MustRandString(32)

			ra, err := database.DeleteNarFileByHash(context.Background(), db, database.DeleteNarFileByHashParams{
				Hash: hash,
			})
			require.NoError(t, err)

			assert.Zero(t, ra)
		})

		t.Run("nar existing", func(t *testing.T) {
			hash := testhelper.MustRandString(32)

			t.Run("create the nar", func(t *testing.T) {
				_, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
					Hash:        hash,
					Compression: "zstd",
					Query:       "foo=bar",
					FileSize:    123,
				})
				require.NoError(t, err)
			})

			t.Run("delete the narinfo", func(t *testing.T) {
				time.Sleep(time.Second)

				ra, err := database.DeleteNarFileByHash(context.Background(), db, database.DeleteNarFileByHashParams{
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

				rows, err := db.DB.QueryContext(context.Background(), query)
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
			hash := testhelper.MustRandString(32)

			// Create two variants
			_, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        hash,
				Compression: "xz",
				Query:       "q1",
				FileSize:    100,
			})
			require.NoError(t, err)

			v2, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        hash,
				Compression: "zstd",
				Query:       "q2",
				FileSize:    200,
			})
			require.NoError(t, err)

			// Delete only v1
			ra, err := database.DeleteNarFileByHash(context.Background(), db, database.DeleteNarFileByHashParams{
				Hash:        hash,
				Compression: "xz",
				Query:       "q1",
			})
			require.NoError(t, err)
			assert.EqualValues(t, 1, ra)

			// Confirm v1 is gone
			_, err = database.GetNarFileByHashAndCompressionAndQuery(
				context.Background(),
				db,
				hash,
				"xz",
				"q1",
			)
			assert.True(t, database.IsNotFoundError(err))

			// Confirm v2 still exists
			retV2, err := database.GetNarFileByHashAndCompressionAndQuery(
				context.Background(),
				db,
				hash,
				"zstd",
				"q2",
			)
			require.NoError(t, err)
			assert.Equal(t, v2.ID, retV2.ID)
		})
	})

	t.Run("NarTotalSize", func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		var expectedSize int64

		for _, narEntry := range testdata.Entries {
			narSize := int64(len(narEntry.NarText))
			expectedSize += narSize

			_, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{
				Hash:    narEntry.NarHash,
				NarSize: sql.NullInt64{Int64: narSize, Valid: true},
			})
			require.NoError(t, err)
		}

		size, err := database.GetNarTotalSize(context.Background(), db)
		require.NoError(t, err)

		assert.Equal(t, expectedSize, size)
	})

	t.Run("SetConfig", func(t *testing.T) {
		t.Parallel()

		t.Run("key not existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			key := testhelper.MustRandString(32)

			value := testhelper.MustRandString(32)

			err := database.SetConfig(context.Background(), db, key, value)
			require.NoError(t, err)

			conf, err := database.GetConfigByKey(context.Background(), db, key)
			require.NoError(t, err)

			assert.Equal(t, key, conf.Key)
			assert.Equal(t, value, conf.Value)
		})

		t.Run("key existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			key := testhelper.MustRandString(32)

			value := testhelper.MustRandString(32)

			err := database.SetConfig(context.Background(), db, key, value)
			require.NoError(t, err)

			value2 := testhelper.MustRandString(32)

			err = database.SetConfig(context.Background(), db, key, value2)
			require.NoError(t, err)

			conf, err := database.GetConfigByKey(context.Background(), db, key)
			require.NoError(t, err)

			assert.Equal(t, key, conf.Key)
			assert.Equal(t, value2, conf.Value)
		})
	})

	t.Run("AddNarInfoReference", func(t *testing.T) {
		t.Parallel()

		t.Run("successful insertion", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			reference := testhelper.MustRandString(32)

			err = database.AddNarInfoReference(context.Background(), db, database.AddNarInfoReferenceParams{
				NarInfoID: ni.ID,
				Reference: reference,
			})
			require.NoError(t, err)

			refs, err := database.GetNarInfoReferences(context.Background(), db, ni.ID)
			require.NoError(t, err)

			if assert.Len(t, refs, 1) {
				assert.Equal(t, reference, refs[0])
			}
		})

		t.Run("duplicate reference is idempotent", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			reference := testhelper.MustRandString(32)

			// Insert first time
			err = database.AddNarInfoReference(context.Background(), db, database.AddNarInfoReferenceParams{
				NarInfoID: ni.ID,
				Reference: reference,
			})
			require.NoError(t, err)

			// Insert duplicate - should not error
			err = database.AddNarInfoReference(context.Background(), db, database.AddNarInfoReferenceParams{
				NarInfoID: ni.ID,
				Reference: reference,
			})
			require.NoError(t, err, "duplicate reference insertion should be idempotent")

			// Verify only one reference exists
			refs, err := database.GetNarInfoReferences(context.Background(), db, ni.ID)
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

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			references := make([]string, 3)
			for i := range references {
				ref, err := testhelper.RandString(32)
				require.NoError(t, err)

				references[i] = ref
			}

			err = database.AddNarInfoReferences(context.Background(), db, database.AddNarInfoReferencesParams{
				NarInfoID: ni.ID,
				Reference: references,
			})
			require.NoError(t, err)

			refs, err := database.GetNarInfoReferences(context.Background(), db, ni.ID)
			require.NoError(t, err)

			assert.Len(t, refs, 3)
		})

		t.Run("duplicate references in same batch are idempotent", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			reference := testhelper.MustRandString(32)

			// Insert same reference multiple times in one batch
			references := []string{reference, reference, reference}

			err = database.AddNarInfoReferences(context.Background(), db, database.AddNarInfoReferencesParams{
				NarInfoID: ni.ID,
				Reference: references,
			})
			require.NoError(t, err, "duplicate references in batch should be idempotent")

			// Verify only one reference exists
			refs, err := database.GetNarInfoReferences(context.Background(), db, ni.ID)
			require.NoError(t, err)

			if assert.Len(t, refs, 1) {
				assert.Equal(t, reference, refs[0])
			}
		})

		t.Run("duplicate references across batches are idempotent", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			ref1 := testhelper.MustRandString(32)

			ref2 := testhelper.MustRandString(32)

			// First batch
			err = database.AddNarInfoReferences(context.Background(), db, database.AddNarInfoReferencesParams{
				NarInfoID: ni.ID,
				Reference: []string{ref1, ref2},
			})
			require.NoError(t, err)

			// Second batch with duplicates
			err = database.AddNarInfoReferences(context.Background(), db, database.AddNarInfoReferencesParams{
				NarInfoID: ni.ID,
				Reference: []string{ref1, ref2},
			})
			require.NoError(t, err, "duplicate references across batches should be idempotent")

			// Verify only two unique references exist
			refs, err := database.GetNarInfoReferences(context.Background(), db, ni.ID)
			require.NoError(t, err)

			assert.Len(t, refs, 2)
		})
	})

	t.Run("AddNarInfoSignature", func(t *testing.T) {
		t.Parallel()

		t.Run("successful insertion", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			signature := testhelper.MustRandString(32)

			err = database.AddNarInfoSignature(context.Background(), db, database.AddNarInfoSignatureParams{
				NarInfoID: ni.ID,
				Signature: signature,
			})
			require.NoError(t, err)

			sigs, err := database.GetNarInfoSignatures(context.Background(), db, ni.ID)
			require.NoError(t, err)

			if assert.Len(t, sigs, 1) {
				assert.Equal(t, signature, sigs[0])
			}
		})

		t.Run("duplicate signature is idempotent", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			signature := testhelper.MustRandString(32)

			// Insert first time
			err = database.AddNarInfoSignature(context.Background(), db, database.AddNarInfoSignatureParams{
				NarInfoID: ni.ID,
				Signature: signature,
			})
			require.NoError(t, err)

			// Insert duplicate - should not error
			err = database.AddNarInfoSignature(context.Background(), db, database.AddNarInfoSignatureParams{
				NarInfoID: ni.ID,
				Signature: signature,
			})
			require.NoError(t, err, "duplicate signature insertion should be idempotent")

			// Verify only one signature exists
			sigs, err := database.GetNarInfoSignatures(context.Background(), db, ni.ID)
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

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			signatures := make([]string, 3)
			for i := range signatures {
				sig, err := testhelper.RandString(32)
				require.NoError(t, err)

				signatures[i] = sig
			}

			err = database.AddNarInfoSignatures(context.Background(), db, database.AddNarInfoSignaturesParams{
				NarInfoID: ni.ID,
				Signature: signatures,
			})
			require.NoError(t, err)

			sigs, err := database.GetNarInfoSignatures(context.Background(), db, ni.ID)
			require.NoError(t, err)

			assert.Len(t, sigs, 3)
		})

		t.Run("duplicate signatures in same batch are idempotent", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			signature := testhelper.MustRandString(32)

			// Insert same signature multiple times in one batch
			signatures := []string{signature, signature, signature}

			err = database.AddNarInfoSignatures(context.Background(), db, database.AddNarInfoSignaturesParams{
				NarInfoID: ni.ID,
				Signature: signatures,
			})
			require.NoError(t, err, "duplicate signatures in batch should be idempotent")

			// Verify only one signature exists
			sigs, err := database.GetNarInfoSignatures(context.Background(), db, ni.ID)
			require.NoError(t, err)

			if assert.Len(t, sigs, 1) {
				assert.Equal(t, signature, sigs[0])
			}
		})

		t.Run("duplicate signatures across batches are idempotent", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			sig1 := testhelper.MustRandString(32)

			sig2 := testhelper.MustRandString(32)

			// First batch
			err = database.AddNarInfoSignatures(context.Background(), db, database.AddNarInfoSignaturesParams{
				NarInfoID: ni.ID,
				Signature: []string{sig1, sig2},
			})
			require.NoError(t, err)

			// Second batch with duplicates
			err = database.AddNarInfoSignatures(context.Background(), db, database.AddNarInfoSignaturesParams{
				NarInfoID: ni.ID,
				Signature: []string{sig1, sig2},
			})
			require.NoError(t, err, "duplicate signatures across batches should be idempotent")

			// Verify only two unique signatures exist
			sigs, err := database.GetNarInfoSignatures(context.Background(), db, ni.ID)
			require.NoError(t, err)

			assert.Len(t, sigs, 2)
		})
	})

	t.Run("GetConfigByID", func(t *testing.T) {
		t.Parallel()

		t.Run("config not existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			_, err := database.GetConfigByID(context.Background(), db, 999999)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("config existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			key := testhelper.MustRandString(32)

			value := testhelper.MustRandString(32)

			conf1, err := database.CreateConfig(context.Background(), db, key, value)
			require.NoError(t, err)

			conf2, err := database.GetConfigByID(context.Background(), db, conf1.ID)
			require.NoError(t, err)

			assert.Equal(t, conf1, conf2)
		})
	})

	t.Run("GetNarInfoByID", func(t *testing.T) {
		t.Parallel()

		t.Run("narinfo not existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			_, err := database.GetNarInfoByID(context.Background(), db, 999999)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("narinfo existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			ni1, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			ni2, err := database.GetNarInfoByID(context.Background(), db, ni1.ID)
			require.NoError(t, err)

			assert.Equal(t, ni1.ID, ni2.ID)
			assert.Equal(t, ni1.Hash, ni2.Hash)
		})
	})

	t.Run("GetNarFileByID", func(t *testing.T) {
		t.Parallel()

		t.Run("nar file not existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			_, err := database.GetNarFileByID(context.Background(), db, 999999)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("nar file existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			nf1, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        hash,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			nf2, err := database.GetNarFileByID(context.Background(), db, nf1.ID)
			require.NoError(t, err)

			assert.Equal(t, nf1.ID, nf2.ID)
			assert.Equal(t, nf1.Hash, nf2.Hash)
		})
	})

	t.Run("UpdateNarInfoFileSize", func(t *testing.T) {
		t.Parallel()

		t.Run("update file size", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			assert.False(t, ni.FileSize.Valid)

			err = database.UpdateNarInfoFileSize(context.Background(), db, hash, sql.NullInt64{Int64: 456, Valid: true})
			require.NoError(t, err)

			ni2, err := database.GetNarInfoByHash(context.Background(), db, hash)
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

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hashes, err := database.GetNarInfoHashesByURL(context.Background(),
				db, sql.NullString{String: "nonexistent.nar", Valid: true})
			require.NoError(t, err)
			assert.Empty(t, hashes)
		})

		t.Run("multiple narinfos with same url", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			url := "nar/test.nar.xz"
			hash1 := testhelper.MustRandString(32)

			hash2 := testhelper.MustRandString(32)

			_, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{
				Hash: hash1,
				URL:  sql.NullString{String: url, Valid: true},
			})
			require.NoError(t, err)

			_, err = database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{
				Hash: hash2,
				URL:  sql.NullString{String: url, Valid: true},
			})
			require.NoError(t, err)

			hashes, err := database.GetNarInfoHashesByURL(context.Background(),
				db, sql.NullString{String: url, Valid: true})
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

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash1 := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash1})
			require.NoError(t, err)

			hash2 := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        hash2,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			err = database.LinkNarInfoToNarFile(context.Background(), db, database.LinkNarInfoToNarFileParams{
				NarInfoID: ni.ID,
				NarFileID: nf.ID,
			})
			require.NoError(t, err)

			// Verify the link by fetching nar file by narinfo id
			nf2, err := database.GetNarFileByNarInfoID(context.Background(), db, ni.ID)
			require.NoError(t, err)

			assert.Equal(t, nf.ID, nf2.ID)
		})

		t.Run("duplicate link is idempotent", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash1 := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash1})
			require.NoError(t, err)

			hash2 := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        hash2,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			err = database.LinkNarInfoToNarFile(context.Background(), db, database.LinkNarInfoToNarFileParams{
				NarInfoID: ni.ID,
				NarFileID: nf.ID,
			})
			require.NoError(t, err)

			// Link again - should not error
			err = database.LinkNarInfoToNarFile(context.Background(), db, database.LinkNarInfoToNarFileParams{
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

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			_, err = database.GetNarFileByNarInfoID(context.Background(), db, ni.ID)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("link exists", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash1 := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash1})
			require.NoError(t, err)

			hash2 := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        hash2,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			err = database.LinkNarInfoToNarFile(context.Background(), db, database.LinkNarInfoToNarFileParams{
				NarInfoID: ni.ID,
				NarFileID: nf.ID,
			})
			require.NoError(t, err)

			nf2, err := database.GetNarFileByNarInfoID(context.Background(), db, ni.ID)
			require.NoError(t, err)

			assert.Equal(t, nf.ID, nf2.ID)
		})
	})

	t.Run("DeleteNarInfoByID", func(t *testing.T) {
		t.Parallel()

		t.Run("narinfo not existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			ra, err := database.DeleteNarInfoByID(context.Background(), db, 999999)
			require.NoError(t, err)
			assert.Zero(t, ra)
		})

		t.Run("narinfo existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)

			ra, err := database.DeleteNarInfoByID(context.Background(), db, ni.ID)
			require.NoError(t, err)
			assert.EqualValues(t, 1, ra)

			_, err = database.GetNarInfoByID(context.Background(), db, ni.ID)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})
	})

	t.Run("DeleteOrphanedNarFiles", func(t *testing.T) {
		t.Parallel()

		t.Run("no orphaned nar files", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash1 := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash1})
			require.NoError(t, err)

			hash2 := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        hash2,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			err = database.LinkNarInfoToNarFile(context.Background(), db, database.LinkNarInfoToNarFileParams{
				NarInfoID: ni.ID,
				NarFileID: nf.ID,
			})
			require.NoError(t, err)

			ra, err := database.DeleteOrphanedNarFiles(context.Background(), db)
			require.NoError(t, err)
			assert.Zero(t, ra)
		})

		t.Run("orphaned nar files are deleted", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash1 := testhelper.MustRandString(32)

			nf1, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        hash1,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			hash2 := testhelper.MustRandString(32)

			nf2, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        hash2,
				Compression: "xz",
				FileSize:    456,
			})
			require.NoError(t, err)

			ra, err := database.DeleteOrphanedNarFiles(context.Background(), db)
			require.NoError(t, err)
			assert.EqualValues(t, 2, ra)

			_, err = database.GetNarFileByID(context.Background(), db, nf1.ID)
			require.ErrorIs(t, err, database.ErrNotFound)

			_, err = database.GetNarFileByID(context.Background(), db, nf2.ID)
			require.ErrorIs(t, err, database.ErrNotFound)
		})
	})

	t.Run("DeleteOrphanedChunks", func(t *testing.T) {
		t.Parallel()

		t.Run("no orphaned chunks", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    512,
			})
			require.NoError(t, err)

			chunkHash := testhelper.MustRandString(32)

			chunk, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash,
				Size: 512,
			})
			require.NoError(t, err)

			err = database.LinkNarFileToChunk(context.Background(), db, database.LinkNarFileToChunkParams{
				NarFileID:  nf.ID,
				ChunkID:    chunk.ID,
				ChunkIndex: 0,
			})
			require.NoError(t, err)

			ra, err := database.DeleteOrphanedChunks(context.Background(), db)
			require.NoError(t, err)
			assert.Zero(t, ra)
		})

		t.Run("orphaned chunks are deleted", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			chunkHash1 := testhelper.MustRandString(32)

			chunk1, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash1,
				Size: 512,
			})
			require.NoError(t, err)

			chunkHash2 := testhelper.MustRandString(32)

			chunk2, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash2,
				Size: 1024,
			})
			require.NoError(t, err)

			ra, err := database.DeleteOrphanedChunks(context.Background(), db)
			require.NoError(t, err)
			assert.EqualValues(t, 2, ra)

			_, err = database.GetChunkByID(context.Background(), db, chunk1.ID)
			require.ErrorIs(t, err, database.ErrNotFound)

			_, err = database.GetChunkByID(context.Background(), db, chunk2.ID)
			require.ErrorIs(t, err, database.ErrNotFound)
		})
	})

	t.Run("GetNarInfoCount", func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		initialCount, err := database.GetNarInfoCount(context.Background(), db)
		require.NoError(t, err)

		for range 5 {
			hash := testhelper.MustRandString(32)

			_, err = database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash})
			require.NoError(t, err)
		}

		count, err := database.GetNarInfoCount(context.Background(), db)
		require.NoError(t, err)

		assert.Equal(t, initialCount+5, count)
	})

	t.Run("GetNarFileCount", func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		initialCount, err := database.GetNarFileCount(context.Background(), db)
		require.NoError(t, err)

		for i := range 3 {
			hash := testhelper.MustRandString(32)

			//nolint:gosec // G115: Safe conversion, i is small and controlled
			_, err = database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        hash,
				Compression: "xz",
				FileSize:    uint64(100 * (i + 1)),
			})
			require.NoError(t, err)
		}

		count, err := database.GetNarFileCount(context.Background(), db)
		require.NoError(t, err)

		assert.Equal(t, initialCount+3, count)
	})

	t.Run("GetLeastUsedNarInfos", func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Create 3 narinfos with different nar files of different sizes
		hash1 := testhelper.MustRandString(32)

		ni1, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{
			Hash:     hash1,
			FileSize: sql.NullInt64{Int64: 100, Valid: true},
		})
		require.NoError(t, err)

		nfHash1, err := testhelper.RandString(32)
		require.NoError(t, err)

		nf1, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
			Hash:        nfHash1,
			Compression: "xz",
			FileSize:    100,
		})
		require.NoError(t, err)

		err = database.LinkNarInfoToNarFile(context.Background(), db, database.LinkNarInfoToNarFileParams{
			NarInfoID: ni1.ID,
			NarFileID: nf1.ID,
		})
		require.NoError(t, err)

		hash2, err := testhelper.RandString(32)
		require.NoError(t, err)

		ni2, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{
			Hash:     hash2,
			FileSize: sql.NullInt64{Int64: 200, Valid: true},
		})
		require.NoError(t, err)

		nfHash2, err := testhelper.RandString(32)
		require.NoError(t, err)

		nf2, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
			Hash:        nfHash2,
			Compression: "xz",
			FileSize:    200,
		})
		require.NoError(t, err)

		err = database.LinkNarInfoToNarFile(context.Background(), db, database.LinkNarInfoToNarFileParams{
			NarInfoID: ni2.ID,
			NarFileID: nf2.ID,
		})
		require.NoError(t, err)

		hash3, err := testhelper.RandString(32)
		require.NoError(t, err)

		ni3, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{
			Hash:     hash3,
			FileSize: sql.NullInt64{Int64: 300, Valid: true},
		})
		require.NoError(t, err)

		nfHash3, err := testhelper.RandString(32)
		require.NoError(t, err)

		nf3, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
			Hash:        nfHash3,
			Compression: "xz",
			FileSize:    300,
		})
		require.NoError(t, err)

		err = database.LinkNarInfoToNarFile(context.Background(), db, database.LinkNarInfoToNarFileParams{
			NarInfoID: ni3.ID,
			NarFileID: nf3.ID,
		})
		require.NoError(t, err)

		// Wait for one second to ensure that last_accessed_at is different from
		// created_at. This is needed because some databases (like SQLite) might
		// not have sub-second precision for CURRENT_TIMESTAMP.
		time.Sleep(time.Second)

		// Touch ni2 and ni3, making ni1 the least used
		_, err = database.TouchNarInfo(context.Background(), db, hash2)
		require.NoError(t, err)

		_, err = database.TouchNarInfo(context.Background(), db, hash3)
		require.NoError(t, err)

		// Query for narinfos with cumulative file_size <= 200 - should return ni1
		// because ni1 was never touched (least recently used) and its file_size=100
		// is less than the threshold. ni2 and ni3 have cumulative sums (including
		// ni1's file_size) that exceed 200.
		narInfos, err := database.GetLeastUsedNarInfos(context.Background(), db, 200)
		require.NoError(t, err)

		// ni1 is the only one with cumulative sum <= 200 (0 + 100 = 100)
		if assert.Len(t, narInfos, 1) {
			assert.Equal(t, hash1, narInfos[0].Hash)
		}
	})

	t.Run("GetOrphanedNarFiles", func(t *testing.T) {
		t.Parallel()

		t.Run("no orphaned nar files", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash1 := testhelper.MustRandString(32)

			ni, err := database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash1})
			require.NoError(t, err)

			hash2 := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        hash2,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			err = database.LinkNarInfoToNarFile(context.Background(), db, database.LinkNarInfoToNarFileParams{
				NarInfoID: ni.ID,
				NarFileID: nf.ID,
			})
			require.NoError(t, err)

			orphaned, err := database.GetOrphanedNarFiles(context.Background(), db)
			require.NoError(t, err)
			assert.Empty(t, orphaned)
		})

		t.Run("orphaned nar files are returned", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash1 := testhelper.MustRandString(32)

			nf1, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        hash1,
				Compression: "xz",
				FileSize:    123,
			})
			require.NoError(t, err)

			hash2 := testhelper.MustRandString(32)

			nf2, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        hash2,
				Compression: "xz",
				FileSize:    456,
			})
			require.NoError(t, err)

			orphaned, err := database.GetOrphanedNarFiles(context.Background(), db)
			require.NoError(t, err)

			assert.Len(t, orphaned, 2)
			foundIDs := []int64{orphaned[0].ID, orphaned[1].ID}
			assert.Contains(t, foundIDs, nf1.ID)
			assert.Contains(t, foundIDs, nf2.ID)
		})
	})

	t.Run("GetUnmigratedNarInfoHashes", func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		hash1, err := testhelper.RandString(32)
		require.NoError(t, err)

		_, err = database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash1})
		require.NoError(t, err)

		hash2, err := testhelper.RandString(32)
		require.NoError(t, err)

		_, err = database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{
			Hash: hash2,
			URL:  sql.NullString{String: "nar/test.nar.xz", Valid: true},
		})
		require.NoError(t, err)

		unmigrated, err := database.GetUnmigratedNarInfoHashes(context.Background(), db)
		require.NoError(t, err)

		assert.Contains(t, unmigrated, hash1)
		assert.NotContains(t, unmigrated, hash2)
	})

	t.Run("GetMigratedNarInfoHashes", func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		hash1, err := testhelper.RandString(32)
		require.NoError(t, err)

		_, err = database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{Hash: hash1})
		require.NoError(t, err)

		hash2, err := testhelper.RandString(32)
		require.NoError(t, err)

		_, err = database.CreateNarInfo(context.Background(), db, database.CreateNarInfoParams{
			Hash: hash2,
			URL:  sql.NullString{String: "nar/test.nar.xz", Valid: true},
		})
		require.NoError(t, err)

		migrated, err := database.GetMigratedNarInfoHashes(context.Background(), db)
		require.NoError(t, err)

		assert.NotContains(t, migrated, hash1)
		assert.Contains(t, migrated, hash2)
	})

	t.Run("CreateChunk", func(t *testing.T) {
		t.Parallel()

		t.Run("create new chunk", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			chunkHash := testhelper.MustRandString(32)

			chunk, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash,
				Size: 1024,
			})
			require.NoError(t, err)

			assert.Equal(t, chunkHash, chunk.Hash)
			assert.EqualValues(t, 1024, chunk.Size)
		})

		t.Run("duplicate chunk is idempotent", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			chunkHash := testhelper.MustRandString(32)

			chunk1, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash,
				Size: 1024,
			})
			require.NoError(t, err)

			chunk2, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
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

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			hash := testhelper.MustRandString(32)

			_, err := database.GetChunkByHash(context.Background(), db, hash)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("chunk existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			chunkHash := testhelper.MustRandString(32)

			chunk1, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash,
				Size: 1024,
			})
			require.NoError(t, err)

			chunk2, err := database.GetChunkByHash(context.Background(), db, chunkHash)
			require.NoError(t, err)

			assert.Equal(t, chunk1.ID, chunk2.ID)
			assert.Equal(t, chunk1.Hash, chunk2.Hash)
		})
	})

	t.Run("GetChunkByID", func(t *testing.T) {
		t.Parallel()

		t.Run("chunk not existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			_, err := database.GetChunkByID(context.Background(), db, 999999)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("chunk existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			chunkHash := testhelper.MustRandString(32)

			chunk1, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash,
				Size: 1024,
			})
			require.NoError(t, err)

			chunk2, err := database.GetChunkByID(context.Background(), db, chunk1.ID)
			require.NoError(t, err)

			assert.Equal(t, chunk1.ID, chunk2.ID)
			assert.Equal(t, chunk1.Hash, chunk2.Hash)
		})
	})

	t.Run("LinkNarFileToChunk", func(t *testing.T) {
		t.Parallel()

		t.Run("successful linking", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1024,
			})
			require.NoError(t, err)

			chunkHash := testhelper.MustRandString(32)

			chunk, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash,
				Size: 512,
			})
			require.NoError(t, err)

			err = database.LinkNarFileToChunk(context.Background(), db, database.LinkNarFileToChunkParams{
				NarFileID:  nf.ID,
				ChunkID:    chunk.ID,
				ChunkIndex: 0,
			})
			require.NoError(t, err)

			chunks, err := database.GetChunksByNarFileID(context.Background(), db, nf.ID)
			require.NoError(t, err)

			if assert.Len(t, chunks, 1) {
				assert.Equal(t, chunk.ID, chunks[0].ID)
			}
		})

		t.Run("duplicate link is idempotent", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1024,
			})
			require.NoError(t, err)

			chunkHash := testhelper.MustRandString(32)

			chunk, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash,
				Size: 512,
			})
			require.NoError(t, err)

			err = database.LinkNarFileToChunk(context.Background(), db, database.LinkNarFileToChunkParams{
				NarFileID:  nf.ID,
				ChunkID:    chunk.ID,
				ChunkIndex: 0,
			})
			require.NoError(t, err)

			// Link again - should not error
			err = database.LinkNarFileToChunk(context.Background(), db, database.LinkNarFileToChunkParams{
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

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    3072,
			})
			require.NoError(t, err)

			// Create 3 chunks
			chunkIDs := make([]int64, 3)
			chunkIndices := make([]int64, 3)

			for i := range 3 {
				chunkHash, err := testhelper.RandString(32)
				require.NoError(t, err)

				chunk, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
					Hash: chunkHash,
					Size: 1024,
				})
				require.NoError(t, err)

				chunkIDs[i] = chunk.ID
				chunkIndices[i] = int64(i)
			}

			// Link all chunks in bulk
			err = database.LinkNarFileToChunks(context.Background(), db, database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    chunkIDs,
				ChunkIndex: chunkIndices,
			})
			require.NoError(t, err)

			// Verify all chunks are linked
			chunks, err := database.GetChunksByNarFileID(context.Background(), db, nf.ID)
			require.NoError(t, err)

			if assert.Len(t, chunks, 3) {
				// Verify order is maintained
				for i := range 3 {
					assert.Equal(t, chunkIDs[i], chunks[i].ID)
				}
			}
		})

		t.Run("bulk link with single chunk", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1024,
			})
			require.NoError(t, err)

			chunkHash := testhelper.MustRandString(32)

			chunk, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash,
				Size: 1024,
			})
			require.NoError(t, err)

			// Link single chunk using bulk operation
			err = database.LinkNarFileToChunks(context.Background(), db, database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    []int64{chunk.ID},
				ChunkIndex: []int64{0},
			})
			require.NoError(t, err)

			chunks, err := database.GetChunksByNarFileID(context.Background(), db, nf.ID)
			require.NoError(t, err)

			if assert.Len(t, chunks, 1) {
				assert.Equal(t, chunk.ID, chunks[0].ID)
			}
		})

		t.Run("bulk link with empty arrays", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    0,
			})
			require.NoError(t, err)

			// Link with empty arrays - should not error
			err = database.LinkNarFileToChunks(context.Background(), db, database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    []int64{},
				ChunkIndex: []int64{},
			})
			require.NoError(t, err)

			chunks, err := database.GetChunksByNarFileID(context.Background(), db, nf.ID)
			require.NoError(t, err)
			assert.Empty(t, chunks)
		})

		t.Run("duplicate bulk links are idempotent", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    2048,
			})
			require.NoError(t, err)

			// Create 2 chunks
			chunkIDs := make([]int64, 2)
			chunkIndices := make([]int64, 2)

			for i := range 2 {
				chunkHash, err := testhelper.RandString(32)
				require.NoError(t, err)

				chunk, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
					Hash: chunkHash,
					Size: 1024,
				})
				require.NoError(t, err)

				chunkIDs[i] = chunk.ID
				chunkIndices[i] = int64(i)
			}

			// Link chunks first time
			err = database.LinkNarFileToChunks(context.Background(), db, database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    chunkIDs,
				ChunkIndex: chunkIndices,
			})
			require.NoError(t, err)

			// Link same chunks again - should be idempotent
			err = database.LinkNarFileToChunks(context.Background(), db, database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    chunkIDs,
				ChunkIndex: chunkIndices,
			})
			require.NoError(t, err, "duplicate bulk link should be idempotent")

			// Verify we still have only 2 chunks
			chunks, err := database.GetChunksByNarFileID(context.Background(), db, nf.ID)
			require.NoError(t, err)
			assert.Len(t, chunks, 2)
		})

		t.Run("bulk link maintains correct chunk order", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    5120,
			})
			require.NoError(t, err)

			// Create 5 chunks
			numChunks := 5
			chunkIDs := make([]int64, numChunks)
			chunkIndices := make([]int64, numChunks)

			for i := range numChunks {
				chunkHash, err := testhelper.RandString(32)
				require.NoError(t, err)

				chunk, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
					Hash: chunkHash,
					Size: 1024,
				})
				require.NoError(t, err)

				chunkIDs[i] = chunk.ID
				chunkIndices[i] = int64(i)
			}

			// Link all chunks in bulk
			err = database.LinkNarFileToChunks(context.Background(), db, database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    chunkIDs,
				ChunkIndex: chunkIndices,
			})
			require.NoError(t, err)

			// Verify chunks are in correct order
			chunks, err := database.GetChunksByNarFileID(context.Background(), db, nf.ID)
			require.NoError(t, err)

			if assert.Len(t, chunks, numChunks) {
				for i := range numChunks {
					assert.Equal(t, chunkIDs[i], chunks[i].ID, "chunk at index %d should match", i)
				}
			}
		})

		t.Run("bulk link with non-sequential indices", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    3072,
			})
			require.NoError(t, err)

			// Create 3 chunks with non-sequential indices (e.g., 10, 20, 30)
			chunkIDs := make([]int64, 3)
			chunkIndices := []int64{10, 20, 30}

			for i := range 3 {
				chunkHash, err := testhelper.RandString(32)
				require.NoError(t, err)

				chunk, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
					Hash: chunkHash,
					Size: 1024,
				})
				require.NoError(t, err)

				chunkIDs[i] = chunk.ID
			}

			// Link chunks with non-sequential indices
			err = database.LinkNarFileToChunks(context.Background(), db, database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    chunkIDs,
				ChunkIndex: chunkIndices,
			})
			require.NoError(t, err)

			// Verify chunks are linked
			chunks, err := database.GetChunksByNarFileID(context.Background(), db, nf.ID)
			require.NoError(t, err)

			if assert.Len(t, chunks, 3) {
				// Chunks should be returned in order by index
				for i := range 3 {
					assert.Equal(t, chunkIDs[i], chunks[i].ID)
				}
			}
		})

		t.Run("bulk link mixed with individual links", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    4096,
			})
			require.NoError(t, err)

			// Create 4 chunks
			var allChunkIDs []int64

			for range 4 {
				chunkHash, err := testhelper.RandString(32)
				require.NoError(t, err)

				chunk, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
					Hash: chunkHash,
					Size: 1024,
				})
				require.NoError(t, err)

				allChunkIDs = append(allChunkIDs, chunk.ID)
			}

			// Link first 2 chunks individually
			for i := range 2 {
				err = database.LinkNarFileToChunk(context.Background(), db, database.LinkNarFileToChunkParams{
					NarFileID:  nf.ID,
					ChunkID:    allChunkIDs[i],
					ChunkIndex: int64(i),
				})
				require.NoError(t, err)
			}

			// Link last 2 chunks in bulk
			err = database.LinkNarFileToChunks(context.Background(), db, database.LinkNarFileToChunksParams{
				NarFileID:  nf.ID,
				ChunkID:    allChunkIDs[2:],
				ChunkIndex: []int64{2, 3},
			})
			require.NoError(t, err)

			// Verify all chunks are linked in correct order
			chunks, err := database.GetChunksByNarFileID(context.Background(), db, nf.ID)
			require.NoError(t, err)

			if assert.Len(t, chunks, 4) {
				for i := range 4 {
					assert.Equal(t, allChunkIDs[i], chunks[i].ID)
				}
			}
		})

		t.Run("bulk link with mismatched arrays", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)
			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1024,
			})
			require.NoError(t, err)

			chunkHash := testhelper.MustRandString(32)
			chunk, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash,
				Size: 1024,
			})
			require.NoError(t, err)

			err = database.LinkNarFileToChunks(context.Background(), db, database.LinkNarFileToChunksParams{
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

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1024,
			})
			require.NoError(t, err)

			chunks, err := database.GetChunksByNarFileID(context.Background(), db, nf.ID)
			require.NoError(t, err)
			assert.Empty(t, chunks)
		})

		t.Run("multiple chunks linked in order", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1536,
			})
			require.NoError(t, err)

			// Create 3 chunks
			var chunkIDs []int64

			for i := range 3 {
				chunkHash, err := testhelper.RandString(32)
				require.NoError(t, err)

				chunk, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
					Hash: chunkHash,
					Size: 512,
				})
				require.NoError(t, err)

				chunkIDs = append(chunkIDs, chunk.ID)

				err = database.LinkNarFileToChunk(context.Background(), db, database.LinkNarFileToChunkParams{
					NarFileID:  nf.ID,
					ChunkID:    chunk.ID,
					ChunkIndex: int64(i),
				})
				require.NoError(t, err)
			}

			chunks, err := database.GetChunksByNarFileID(context.Background(), db, nf.ID)
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

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1024,
			})
			require.NoError(t, err)

			_, err = database.GetChunkByNarFileIDAndIndex(context.Background(),
				db,
				database.GetChunkByNarFileIDAndIndexParams{
					NarFileID:  nf.ID,
					ChunkIndex: 0,
				})
			assert.ErrorIs(t, err, database.ErrNotFound)
		})

		t.Run("chunk existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    1024,
			})
			require.NoError(t, err)

			chunkHash := testhelper.MustRandString(32)

			chunk, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash,
				Size: 512,
			})
			require.NoError(t, err)

			err = database.LinkNarFileToChunk(context.Background(), db, database.LinkNarFileToChunkParams{
				NarFileID:  nf.ID,
				ChunkID:    chunk.ID,
				ChunkIndex: 0,
			})
			require.NoError(t, err)

			chunk2, err := database.GetChunkByNarFileIDAndIndex(context.Background(),
				db,
				database.GetChunkByNarFileIDAndIndexParams{
					NarFileID:  nf.ID,
					ChunkIndex: 0,
				})
			require.NoError(t, err)

			assert.Equal(t, chunk.ID, chunk2.ID)
		})
	})

	t.Run("GetChunkCount", func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		initialCount, err := database.GetChunkCount(context.Background(), db)
		require.NoError(t, err)

		for range 4 {
			chunkHash := testhelper.MustRandString(32)

			_, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash,
				Size: 512,
			})
			require.NoError(t, err)
		}

		count, err := database.GetChunkCount(context.Background(), db)
		require.NoError(t, err)

		assert.Equal(t, initialCount+4, count)
	})

	t.Run("GetOrphanedChunks", func(t *testing.T) {
		t.Parallel()

		t.Run("no orphaned chunks", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			narHash := testhelper.MustRandString(32)

			nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
				Hash:        narHash,
				Compression: "xz",
				FileSize:    512,
			})
			require.NoError(t, err)

			chunkHash := testhelper.MustRandString(32)

			chunk, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash,
				Size: 512,
			})
			require.NoError(t, err)

			err = database.LinkNarFileToChunk(context.Background(), db, database.LinkNarFileToChunkParams{
				NarFileID:  nf.ID,
				ChunkID:    chunk.ID,
				ChunkIndex: 0,
			})
			require.NoError(t, err)

			orphaned, err := database.GetOrphanedChunks(context.Background(), db)
			require.NoError(t, err)
			assert.Empty(t, orphaned)
		})

		t.Run("orphaned chunks are returned", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			chunkHash1 := testhelper.MustRandString(32)

			chunk1, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash1,
				Size: 512,
			})
			require.NoError(t, err)

			chunkHash2 := testhelper.MustRandString(32)

			chunk2, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash2,
				Size: 1024,
			})
			require.NoError(t, err)

			orphaned, err := database.GetOrphanedChunks(context.Background(), db)
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

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			err := database.DeleteChunkByID(context.Background(), db, 999999)
			require.NoError(t, err)
		})

		t.Run("chunk existing", func(t *testing.T) {
			t.Parallel()

			db, cleanup := factory(t)
			t.Cleanup(cleanup)

			chunkHash := testhelper.MustRandString(32)

			chunk, err := database.CreateChunk(context.Background(), db, database.CreateChunkParams{
				Hash: chunkHash,
				Size: 512,
			})
			require.NoError(t, err)

			err = database.DeleteChunkByID(context.Background(), db, chunk.ID)
			require.NoError(t, err)

			_, err = database.GetChunkByID(context.Background(), db, chunk.ID)
			assert.ErrorIs(t, err, database.ErrNotFound)
		})
	})

	t.Run("UpdateNarFileTotalChunks", func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		narHash, err := testhelper.RandString(32)
		require.NoError(t, err)

		nf, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
			Hash:        narHash,
			Compression: "xz",
			FileSize:    1536,
		})
		require.NoError(t, err)

		assert.EqualValues(t, 0, nf.TotalChunks)

		err = database.UpdateNarFileTotalChunks(context.Background(), db, database.UpdateNarFileTotalChunksParams{
			TotalChunks: 3,
			ID:          nf.ID,
		})
		require.NoError(t, err)

		nf2, err := database.GetNarFileByID(context.Background(), db, nf.ID)
		require.NoError(t, err)

		assert.EqualValues(t, 3, nf2.TotalChunks)
	})

	t.Run("GetNarFilesToChunk", func(t *testing.T) {
		t.Parallel()

		db, cleanup := factory(t)
		t.Cleanup(cleanup)

		// Create some nar files
		hash1 := testhelper.MustRandString(32)
		hash2 := testhelper.MustRandString(32)
		hash3 := testhelper.MustRandString(32)

		nf1, err := database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
			Hash:        hash1,
			Compression: "xz",
			FileSize:    1024,
			TotalChunks: 0, // Unchunked
		})
		require.NoError(t, err)

		_, err = database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
			Hash:        hash2,
			Compression: "xz",
			FileSize:    1024,
			TotalChunks: 0, // Unchunked
		})
		require.NoError(t, err)

		_, err = database.CreateNarFile(context.Background(), db, database.CreateNarFileParams{
			Hash:        hash3,
			Compression: "xz",
			FileSize:    1024,
			TotalChunks: 5, // Already chunked
		})
		require.NoError(t, err)

		// Check count
		count, err := database.GetNarFilesToChunkCount(context.Background(), db)
		require.NoError(t, err)
		assert.EqualValues(t, 2, count)

		// Get files
		toChunk, err := database.GetNarFilesToChunk(context.Background(), db)
		require.NoError(t, err)
		assert.Len(t, toChunk, 2)

		hashes := []string{toChunk[0].Hash, toChunk[1].Hash}
		assert.Contains(t, hashes, hash1)
		assert.Contains(t, hashes, hash2)
		assert.NotContains(t, hashes, hash3)

		// Update one of them to be chunked
		err = database.UpdateNarFileTotalChunks(context.Background(), db, database.UpdateNarFileTotalChunksParams{
			TotalChunks: 10,
			ID:          nf1.ID,
		})
		require.NoError(t, err)

		// Check count again
		count, err = database.GetNarFilesToChunkCount(context.Background(), db)
		require.NoError(t, err)
		assert.EqualValues(t, 1, count)

		// Get files again
		toChunk, err = database.GetNarFilesToChunk(context.Background(), db)
		require.NoError(t, err)
		assert.Len(t, toChunk, 1)
		assert.Equal(t, hash2, toChunk[0].Hash)
	})
}
