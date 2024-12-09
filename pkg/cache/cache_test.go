package cache_test

import (
	"context"
	"database/sql"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inconshreveable/log15/v3"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/testdata"

	// Import the SQLite driver.
	_ "github.com/mattn/go-sqlite3"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("path must be absolute, must exist, and must be a writable directory", func(t *testing.T) {
		t.Parallel()

		t.Run("path is required", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "hello")
			assert.ErrorIs(t, err, cache.ErrPathMustBeAbsolute)
		})

		t.Run("path is not absolute", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "hello")
			assert.ErrorIs(t, err, cache.ErrPathMustBeAbsolute)
		})

		t.Run("path must exist", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "/non-existing")
			assert.ErrorIs(t, err, cache.ErrPathMustExist)
		})

		t.Run("path must be a directory", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "/proc/cpuinfo")
			assert.ErrorIs(t, err, cache.ErrPathMustBeADirectory)
		})

		t.Run("path must be writable", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "/root")
			assert.ErrorIs(t, err, cache.ErrPathMustBeWritable)
		})

		t.Run("valid path must return no error", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", os.TempDir())
			assert.NoError(t, err)
		})

		t.Run("should create directories", func(t *testing.T) {
			dir, err := os.MkdirTemp("", "cache-path-")
			require.NoError(t, err)
			defer os.RemoveAll(dir) // clean up

			_, err = cache.New(logger, "cache.example.com", dir)
			require.NoError(t, err)

			dirs := []string{
				"config",
				"store",
				filepath.Join("store", "nar"),
				filepath.Join("store", "tmp"),
				filepath.Join("var", "ncps", "db"),
			}

			for _, p := range dirs {
				t.Run("Checking that "+p+" exists", func(t *testing.T) {
					assert.DirExists(t, filepath.Join(dir, p))
				})
			}
		})

		t.Run("store/tmp is removed on boot", func(t *testing.T) {
			dir, err := os.MkdirTemp("", "cache-path-")
			require.NoError(t, err)
			defer os.RemoveAll(dir) // clean up

			// create the directory tmp and add a file inside of it
			err = os.MkdirAll(filepath.Join(dir, "store", "tmp"), 0o700)
			require.NoError(t, err)

			f, err := os.CreateTemp(filepath.Join(dir, "store", "tmp"), "hello")
			require.NoError(t, err)

			_, err = cache.New(logger, "cache.example.com", dir)
			require.NoError(t, err)

			assert.NoFileExists(t, f.Name())
		})

		t.Run("should create sqlite3 database", func(t *testing.T) {
			dir, err := os.MkdirTemp("", "cache-path-")
			require.NoError(t, err)
			defer os.RemoveAll(dir) // clean up

			_, err = cache.New(logger, "cache.example.com", dir)
			require.NoError(t, err)

			assert.FileExists(t, filepath.Join(dir, "var", "ncps", "db", "db.sqlite"))
		})
	})

	t.Run("hostname must be valid with no scheme or path", func(t *testing.T) {
		t.Parallel()

		t.Run("hostname must not be empty", func(t *testing.T) {
			_, err := cache.New(logger, "", os.TempDir())
			assert.ErrorIs(t, err, cache.ErrHostnameRequired)
		})

		t.Run("hostname must not contain scheme", func(t *testing.T) {
			_, err := cache.New(logger, "https://cache.example.com", os.TempDir())
			assert.ErrorIs(t, err, cache.ErrHostnameMustNotContainScheme)
		})

		t.Run("hostname must not contain a path", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com/path/to", os.TempDir())
			assert.ErrorIs(t, err, cache.ErrHostnameMustNotContainPath)
		})

		t.Run("valid hostName must return no error", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", os.TempDir())
			require.NoError(t, err)
		})
	})
}

func TestPublicKey(t *testing.T) {
	t.Parallel()

	c, err := cache.New(logger, "cache.example.com", "/tmp")
	require.NoError(t, err)

	pubKey := c.PublicKey().String()

	t.Run("should return a public key with the correct prefix", func(t *testing.T) {
		t.Parallel()

		assert.True(t, strings.HasPrefix(pubKey, "cache.example.com:"))
	})

	t.Run("should return a valid public key", func(t *testing.T) {
		t.Parallel()

		pk, err := signature.ParsePublicKey(pubKey)
		require.NoError(t, err)

		assert.Equal(t, pubKey, pk.String())
	})
}

//nolint:paralleltest
func TestGetNarInfo(t *testing.T) {
	ts := testdata.HTTPTestServer(t, 40)
	defer ts.Close()

	tu, err := url.Parse(ts.URL)
	require.NoError(t, err)

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	uc, err := upstream.New(logger, tu.Host, testdata.PublicKeys())
	require.NoError(t, err)

	c, err := cache.New(logger, "cache.example.com", dir)
	require.NoError(t, err)

	c.AddUpstreamCaches(uc)
	c.SetRecordAgeIgnoreTouch(0)

	db, err := sql.Open("sqlite3", filepath.Join(dir, "var", "ncps", "db", "db.sqlite"))
	require.NoError(t, err)

	t.Run("narinfo does not exist upstream", func(t *testing.T) {
		_, err := c.GetNarInfo("doesnotexist")
		assert.ErrorIs(t, err, cache.ErrNotFound)
	})

	t.Run("narinfo exists upstream", func(t *testing.T) {
		t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
			assert.NoFileExists(t, filepath.Join(dir, "store", testdata.Nar2.NarInfoHash+".narinfo"))
		})

		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			assert.NoFileExists(t, filepath.Join(dir, "store", "nar", testdata.Nar2.NarHash+".nar.xz"))
		})

		t.Run("narinfo does not exist in the database yet", func(t *testing.T) {
			rows, err := db.Query("SELECT hash FROM narinfos")
			require.NoError(t, err)

			var hashes []string

			for rows.Next() {
				var hash string

				err := rows.Scan(&hash)
				require.NoError(t, err)

				hashes = append(hashes, hash)
			}

			require.NoError(t, rows.Err())
			assert.Empty(t, hashes)
		})

		t.Run("nar does not exist in the database yet", func(t *testing.T) {
			rows, err := db.Query("SELECT hash FROM nars")
			require.NoError(t, err)

			var hashes []string

			for rows.Next() {
				var hash string

				err := rows.Scan(&hash)
				require.NoError(t, err)

				hashes = append(hashes, hash)
			}

			require.NoError(t, rows.Err())
			assert.Empty(t, hashes)
		})

		ni, err := c.GetNarInfo(testdata.Nar2.NarInfoHash)
		require.NoError(t, err)

		t.Run("size is correct", func(t *testing.T) {
			assert.Equal(t, uint64(50308), ni.FileSize)
		})

		t.Run("it should now exist in the store", func(t *testing.T) {
			assert.FileExists(t, filepath.Join(dir, "store", testdata.Nar2.NarInfoHash+".narinfo"))
		})

		t.Run("it should be signed by our server", func(t *testing.T) {
			var found bool

			var sig signature.Signature
			for _, sig = range ni.Signatures {
				if sig.Name == "cache.example.com" {
					found = true

					break
				}
			}

			assert.True(t, found)

			assert.True(t, signature.VerifyFirst(ni.Fingerprint(), ni.Signatures, []signature.PublicKey{c.PublicKey()}))
		})

		t.Run("it should have also pulled the nar", func(t *testing.T) {
			// Force the other goroutine to run so it actually download the file
			// Try at least 10 times before announcing an error
			var err error

			for i := 0; i < 9; i++ {
				// NOTE: I tried runtime.Gosched() but it makes the test flaky
				time.Sleep(time.Millisecond)

				_, err = os.Stat(filepath.Join(dir, "store", "nar", testdata.Nar2.NarHash+".nar.xz"))
				if err == nil {
					break
				}
			}

			assert.NoError(t, err)
		})

		t.Run("narinfo does exist in the database, and has initial last_accessed_at", func(t *testing.T) {
			const query = `
			SELECT  hash, created_at,  last_accessed_at
			FROM narinfos
			`

			rows, err := db.Query(query)
			require.NoError(t, err)

			nims := make([]database.NarInfoModel, 0)

			for rows.Next() {
				var nim database.NarInfoModel

				err := rows.Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt)
				require.NoError(t, err)

				nims = append(nims, nim)
			}

			require.NoError(t, rows.Err())

			assert.Len(t, nims, 1)
			assert.Equal(t, testdata.Nar2.NarInfoHash, nims[0].Hash)
			assert.Equal(t, nims[0].CreatedAt, nims[0].LastAccessedAt)
		})

		t.Run("nar does exist in the database, and has initial last_accessed_at", func(t *testing.T) {
			const query = `
				SELECT  hash,  created_at,  last_accessed_at
				FROM nars
				`

			rows, err := db.Query(query)
			require.NoError(t, err)

			nims := make([]database.NarModel, 0)

			for rows.Next() {
				var nim database.NarModel

				err := rows.Scan(
					&nim.Hash,
					&nim.CreatedAt,
					&nim.LastAccessedAt,
				)
				require.NoError(t, err)

				nims = append(nims, nim)
			}

			require.NoError(t, rows.Err())
			assert.Len(t, nims, 1)
			assert.Equal(t, testdata.Nar2.NarHash, nims[0].Hash)
			assert.Equal(t, nims[0].CreatedAt, nims[0].LastAccessedAt)
		})

		t.Run("pulling it another time within recordAgeIgnoreTouch should not update last_accessed_at", func(t *testing.T) {
			time.Sleep(time.Second)

			c.SetRecordAgeIgnoreTouch(time.Hour)

			defer func() {
				c.SetRecordAgeIgnoreTouch(0)
			}()

			_, err := c.GetNarInfo(testdata.Nar2.NarInfoHash)
			require.NoError(t, err)

			t.Run("narinfo does exist in the database with the same last_accessed_at", func(t *testing.T) {
				const query = `
			SELECT  hash, created_at,  last_accessed_at
			FROM narinfos
			`

				rows, err := db.Query(query)
				require.NoError(t, err)

				nims := make([]database.NarInfoModel, 0)

				for rows.Next() {
					var nim database.NarInfoModel

					err := rows.Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt)
					require.NoError(t, err)

					nims = append(nims, nim)
				}

				require.NoError(t, rows.Err())

				assert.Len(t, nims, 1)
				assert.Equal(t, testdata.Nar2.NarInfoHash, nims[0].Hash)
				assert.Equal(t, nims[0].CreatedAt, nims[0].LastAccessedAt)
			})
		})

		t.Run("pulling it another time should update last_accessed_at only for narinfo", func(t *testing.T) {
			time.Sleep(time.Second)

			_, err := c.GetNarInfo(testdata.Nar2.NarInfoHash)
			require.NoError(t, err)

			t.Run("narinfo does exist in the database, and has more recent last_accessed_at", func(t *testing.T) {
				const query = `
			SELECT  hash, created_at,  last_accessed_at
			FROM narinfos
			`

				rows, err := db.Query(query)
				require.NoError(t, err)

				nims := make([]database.NarInfoModel, 0)

				for rows.Next() {
					var nim database.NarInfoModel

					err := rows.Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt)
					require.NoError(t, err)

					nims = append(nims, nim)
				}

				require.NoError(t, rows.Err())

				assert.Len(t, nims, 1)
				assert.Equal(t, testdata.Nar2.NarInfoHash, nims[0].Hash)
				assert.NotEqual(t, nims[0].CreatedAt, nims[0].LastAccessedAt)
			})
		})

		t.Run("no error is returned if the entry already exist in the database", func(t *testing.T) {
			require.NoError(t, os.Remove(filepath.Join(dir, "store", testdata.Nar2.NarInfoHash+".narinfo")))

			_, err := c.GetNarInfo(testdata.Nar2.NarInfoHash)
			assert.NoError(t, err)
		})

		t.Run("nar does not exist in storage, it gets pulled automatically", func(t *testing.T) {
			narFile := filepath.Join(dir, "store", "nar", testdata.Nar2.NarHash+".nar.xz")

			require.NoError(t, os.Remove(narFile))

			t.Run("it should not return an error", func(t *testing.T) {
				_, err := c.GetNarInfo(testdata.Nar2.NarInfoHash)
				assert.NoError(t, err)
			})

			t.Run("it should have also pulled the nar", func(t *testing.T) {
				// Force the other goroutine to run so it actually download the file
				// Try at least 10 times before announcing an error
				var err error

				for i := 0; i < 9; i++ {
					// NOTE: I tried runtime.Gosched() but it makes the test flaky
					time.Sleep(time.Millisecond)

					_, err = os.Stat(narFile)
					if err == nil {
						break
					}
				}

				assert.NoError(t, err)
			})
		})
	})
}

//nolint:paralleltest
func TestPutNarInfo(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	c, err := cache.New(logger, "cache.example.com", dir)
	require.NoError(t, err)

	c.SetRecordAgeIgnoreTouch(0)

	db, err := sql.Open("sqlite3", filepath.Join(dir, "var", "ncps", "db", "db.sqlite"))
	require.NoError(t, err)

	storePath := filepath.Join(dir, "store", testdata.Nar1.NarInfoHash+".narinfo")

	t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
		assert.NoFileExists(t, storePath)
	})

	t.Run("narinfo does not exist in the database yet", func(t *testing.T) {
		rows, err := db.Query("SELECT hash FROM narinfos")
		require.NoError(t, err)

		var hashes []string

		for rows.Next() {
			var hash string

			err := rows.Scan(&hash)
			require.NoError(t, err)

			hashes = append(hashes, hash)
		}

		require.NoError(t, rows.Err())
		assert.Empty(t, hashes)
	})

	t.Run("nar does not exist in the database yet", func(t *testing.T) {
		rows, err := db.Query("SELECT hash FROM nars")
		require.NoError(t, err)

		var hashes []string

		for rows.Next() {
			var hash string

			err := rows.Scan(&hash)
			require.NoError(t, err)

			hashes = append(hashes, hash)
		}

		require.NoError(t, rows.Err())
		assert.Empty(t, hashes)
	})

	t.Run("PutNarInfo does not return an error", func(t *testing.T) {
		r := io.NopCloser(strings.NewReader(testdata.Nar1.NarInfoText))

		assert.NoError(t, c.PutNarInfo(context.Background(), testdata.Nar1.NarInfoHash, r))
	})

	t.Run("narinfo does exist in storage", func(t *testing.T) {
		assert.FileExists(t, storePath)
	})

	t.Run("it should be signed by our server", func(t *testing.T) {
		f, err := os.Open(storePath)
		require.NoError(t, err)

		ni, err := narinfo.Parse(f)
		require.NoError(t, err)

		var found bool

		var sig signature.Signature
		for _, sig = range ni.Signatures {
			if sig.Name == "cache.example.com" {
				found = true

				break
			}
		}

		assert.True(t, found)

		assert.True(t, signature.VerifyFirst(ni.Fingerprint(), ni.Signatures, []signature.PublicKey{c.PublicKey()}))
	})

	t.Run("narinfo does exist in the database", func(t *testing.T) {
		rows, err := db.Query("SELECT hash FROM narinfos")
		require.NoError(t, err)

		var hashes []string

		for rows.Next() {
			var hash string

			err := rows.Scan(&hash)
			require.NoError(t, err)

			hashes = append(hashes, hash)
		}

		require.NoError(t, rows.Err())

		assert.Len(t, hashes, 1)
		assert.Equal(t, testdata.Nar1.NarInfoHash, hashes[0])
	})

	t.Run("nar does exist in the database", func(t *testing.T) {
		rows, err := db.Query("SELECT hash FROM nars")
		require.NoError(t, err)

		var hashes []string

		for rows.Next() {
			var hash string

			err := rows.Scan(&hash)
			require.NoError(t, err)

			hashes = append(hashes, hash)
		}

		require.NoError(t, rows.Err())

		assert.Len(t, hashes, 1)
		assert.Equal(t, testdata.Nar1.NarHash, hashes[0])
	})
}

//nolint:paralleltest
func TestDeleteNarInfo(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	c, err := cache.New(logger, "cache.example.com", dir)
	require.NoError(t, err)

	c.SetRecordAgeIgnoreTouch(0)

	t.Run("file does not exist in the store", func(t *testing.T) {
		storePath := filepath.Join(dir, "store", testdata.Nar1.NarInfoHash+".narinfo")

		t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
			assert.NoFileExists(t, storePath)
		})

		t.Run("DeleteNarInfo does return an error", func(t *testing.T) {
			err := c.DeleteNarInfo(context.Background(), testdata.Nar1.NarInfoHash)
			assert.ErrorIs(t, err, cache.ErrNotFound)
		})
	})

	t.Run("file does exist in the store", func(t *testing.T) {
		storePath := filepath.Join(dir, "store", testdata.Nar1.NarInfoHash+".narinfo")

		t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
			assert.NoFileExists(t, storePath)
		})

		f, err := os.Create(storePath)
		require.NoError(t, err)

		_, err = f.WriteString(testdata.Nar1.NarInfoText)
		require.NoError(t, err)

		require.NoError(t, err)

		t.Run("narinfo does exist in storage", func(t *testing.T) {
			assert.FileExists(t, storePath)
		})

		t.Run("DeleteNarInfo does not return an error", func(t *testing.T) {
			assert.NoError(t, c.DeleteNarInfo(context.Background(), testdata.Nar1.NarInfoHash))
		})

		t.Run("narinfo is gone from the store", func(t *testing.T) {
			assert.NoFileExists(t, storePath)
		})
	})
}

//nolint:paralleltest
func TestGetNar(t *testing.T) {
	ts := testdata.HTTPTestServer(t, 40)
	defer ts.Close()

	tu, err := url.Parse(ts.URL)
	require.NoError(t, err)

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	uc, err := upstream.New(logger, tu.Host, testdata.PublicKeys())
	require.NoError(t, err)

	c, err := cache.New(logger, "cache.example.com", dir)
	require.NoError(t, err)

	c.AddUpstreamCaches(uc)
	c.SetRecordAgeIgnoreTouch(0)

	db, err := sql.Open("sqlite3", filepath.Join(dir, "var", "ncps", "db", "db.sqlite"))
	require.NoError(t, err)

	t.Run("nar does not exist upstream", func(t *testing.T) {
		_, _, err := c.GetNar("doesnotexist", "xz")
		assert.ErrorIs(t, err, cache.ErrNotFound)
	})

	t.Run("nar exists upstream", func(t *testing.T) {
		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			assert.NoFileExists(t, filepath.Join(dir, "store", "nar", testdata.Nar1.NarHash+".nar.xz"))
		})

		t.Run("nar does not exist in database yet", func(t *testing.T) {
			rows, err := db.Query("SELECT hash FROM nars")
			require.NoError(t, err)

			var hashes []string

			for rows.Next() {
				var hash string

				err := rows.Scan(&hash)
				require.NoError(t, err)

				hashes = append(hashes, hash)
			}

			require.NoError(t, rows.Err())
			assert.Empty(t, hashes)
		})

		t.Run("getting the narinfo so the record in the database now exists", func(t *testing.T) {
			_, err := c.GetNarInfo(testdata.Nar1.NarInfoHash)
			assert.NoError(t, err)
		})

		size, r, err := c.GetNar(testdata.Nar1.NarHash, "xz")
		require.NoError(t, err)

		defer r.Close()

		t.Run("size is correct", func(t *testing.T) {
			assert.Equal(t, int64(len(testdata.Nar1.NarText)), size)
		})

		t.Run("body is the same", func(t *testing.T) {
			body, err := io.ReadAll(r)
			require.NoError(t, err)

			if assert.Equal(t, len(testdata.Nar1.NarText), len(string(body))) {
				assert.Equal(t, testdata.Nar1.NarText, string(body))
			}
		})

		t.Run("it should now exist in the store", func(t *testing.T) {
			assert.FileExists(t, filepath.Join(dir, "store", "nar", testdata.Nar1.NarHash+".nar.xz"))
		})

		t.Run("getting the narinfo so the record in the database now exists", func(t *testing.T) {
			_, err := c.GetNarInfo(testdata.Nar1.NarInfoHash)
			assert.NoError(t, err)
		})

		t.Run("nar does exist in the database, and has initial last_accessed_at", func(t *testing.T) {
			const query = `
				SELECT  hash,  created_at,  last_accessed_at
				FROM nars
				`

			rows, err := db.Query(query)
			require.NoError(t, err)

			nims := make([]database.NarModel, 0)

			for rows.Next() {
				var nim database.NarModel

				err := rows.Scan(
					&nim.Hash,
					&nim.CreatedAt,
					&nim.LastAccessedAt,
				)
				require.NoError(t, err)

				nims = append(nims, nim)
			}

			require.NoError(t, rows.Err())

			assert.Len(t, nims, 1)
			assert.Equal(t, testdata.Nar1.NarHash, nims[0].Hash)
			assert.Equal(t, nims[0].CreatedAt, nims[0].LastAccessedAt)
		})

		t.Run("pulling it another time within recordAgeIgnoreTouch should not update last_accessed_at", func(t *testing.T) {
			time.Sleep(time.Second)

			c.SetRecordAgeIgnoreTouch(time.Hour)

			defer func() {
				c.SetRecordAgeIgnoreTouch(0)
			}()

			_, r, err := c.GetNar(testdata.Nar1.NarHash, "xz")
			require.NoError(t, err)
			defer r.Close()

			t.Run("narinfo does exist in the database with the same last_accessed_at", func(t *testing.T) {
				const query = `
				SELECT  hash,  created_at,  last_accessed_at
				FROM nars
				`

				rows, err := db.Query(query)
				require.NoError(t, err)

				nims := make([]database.NarModel, 0)

				for rows.Next() {
					var nim database.NarModel

					err := rows.Scan(
						&nim.Hash,
						&nim.CreatedAt,
						&nim.LastAccessedAt,
					)
					require.NoError(t, err)

					nims = append(nims, nim)
				}

				require.NoError(t, rows.Err())

				assert.Len(t, nims, 1)
				assert.Equal(t, testdata.Nar1.NarHash, nims[0].Hash)
				assert.Equal(t, nims[0].CreatedAt, nims[0].LastAccessedAt)
			})
		})

		t.Run("pulling it another time should update last_accessed_at", func(t *testing.T) {
			time.Sleep(time.Second)

			_, r, err := c.GetNar(testdata.Nar1.NarHash, "xz")
			require.NoError(t, err)
			defer r.Close()

			t.Run("narinfo does exist in the database, and has more recent last_accessed_at", func(t *testing.T) {
				const query = `
				SELECT  hash,  created_at,  last_accessed_at
				FROM nars
				`

				rows, err := db.Query(query)
				require.NoError(t, err)

				nims := make([]database.NarModel, 0)

				for rows.Next() {
					var nim database.NarModel

					err := rows.Scan(
						&nim.Hash,
						&nim.CreatedAt,
						&nim.LastAccessedAt,
					)
					require.NoError(t, err)

					nims = append(nims, nim)
				}

				require.NoError(t, rows.Err())

				assert.Len(t, nims, 1)
				assert.Equal(t, testdata.Nar1.NarHash, nims[0].Hash)
				assert.NotEqual(t, nims[0].CreatedAt, nims[0].LastAccessedAt)
			})
		})
	})
}

//nolint:paralleltest
func TestPutNar(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	c, err := cache.New(logger, "cache.example.com", dir)
	require.NoError(t, err)

	c.SetRecordAgeIgnoreTouch(0)

	storePath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarHash+".nar.xz")

	t.Run("nar does not exist in storage yet", func(t *testing.T) {
		assert.NoFileExists(t, storePath)
	})

	t.Run("putNar does not return an error", func(t *testing.T) {
		r := io.NopCloser(strings.NewReader(testdata.Nar1.NarText))

		err := c.PutNar(context.Background(), testdata.Nar1.NarHash, "xz", r)
		assert.NoError(t, err)
	})

	t.Run("nar does exist in storage", func(t *testing.T) {
		f, err := os.Open(storePath)
		require.NoError(t, err)

		bs, err := io.ReadAll(f)
		require.NoError(t, err)

		assert.Equal(t, testdata.Nar1.NarText, string(bs))
	})
}

//nolint:paralleltest
func TestDeleteNar(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	c, err := cache.New(logger, "cache.example.com", dir)
	require.NoError(t, err)

	c.SetRecordAgeIgnoreTouch(0)

	storePath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarHash+".nar.xz")

	t.Run("file does not exist in the store", func(t *testing.T) {
		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			assert.NoFileExists(t, storePath)
		})

		t.Run("DeleteNar does return an error", func(t *testing.T) {
			err := c.DeleteNar(context.Background(), testdata.Nar1.NarHash, "xz")
			assert.ErrorIs(t, err, cache.ErrNotFound)
		})
	})

	t.Run("file does exist in the store", func(t *testing.T) {
		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			assert.NoFileExists(t, storePath)
		})

		f, err := os.Create(storePath)
		require.NoError(t, err)

		_, err = f.WriteString(testdata.Nar1.NarText)
		require.NoError(t, err)

		require.NoError(t, f.Close())

		t.Run("nar does exist in storage", func(t *testing.T) {
			assert.FileExists(t, storePath)
		})

		t.Run("deleteNar does not return an error", func(t *testing.T) {
			err := c.DeleteNar(context.Background(), testdata.Nar1.NarHash, "xz")
			assert.NoError(t, err)
		})

		t.Run("nar is gone from the store", func(t *testing.T) {
			assert.NoFileExists(t, storePath)
		})
	})
}
