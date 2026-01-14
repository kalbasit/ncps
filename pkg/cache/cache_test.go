package cache_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	locklocal "github.com/kalbasit/ncps/pkg/lock/local"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"

	// Import the SQLite driver.
	_ "github.com/mattn/go-sqlite3"
)

const cacheName = "cache.example.com"

// newTestCache is a helper function that creates a cache with local locks for testing.
func newTestCache(
	ctx context.Context,
	hostName string,
	db database.Querier,
	configStore storage.ConfigStore,
	narInfoStore storage.NarInfoStore,
	narStore storage.NarStore,
	secretKeyPath string,
) (*cache.Cache, error) {
	downloadLocker := locklocal.NewLocker()
	cacheLocker := locklocal.NewRWLocker()

	return cache.New(ctx, hostName, db, configStore, narInfoStore, narStore, secretKeyPath,
		downloadLocker, cacheLocker, 5*time.Minute, 30*time.Minute)
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("hostname must be valid with no scheme or path", func(t *testing.T) {
		t.Parallel()

		t.Run("hostname must not be empty", func(t *testing.T) {
			t.Parallel()

			dir, err := os.MkdirTemp("", "cache-path-")
			require.NoError(t, err)

			defer os.RemoveAll(dir) // clean up

			dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
			testhelper.CreateMigrateDatabase(t, dbFile)

			db, err := database.Open("sqlite:"+dbFile, nil)
			require.NoError(t, err)

			localStore, err := local.New(newContext(), dir)
			require.NoError(t, err)

			_, err = newTestCache(newContext(), "", db, localStore, localStore, localStore, "")
			assert.ErrorIs(t, err, cache.ErrHostnameRequired)
		})

		t.Run("hostname must not contain scheme", func(t *testing.T) {
			t.Parallel()

			dir, err := os.MkdirTemp("", "cache-path-")
			require.NoError(t, err)

			defer os.RemoveAll(dir) // clean up

			dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
			testhelper.CreateMigrateDatabase(t, dbFile)

			db, err := database.Open("sqlite:"+dbFile, nil)
			require.NoError(t, err)

			localStore, err := local.New(newContext(), dir)
			require.NoError(t, err)

			_, err = newTestCache(newContext(), "https://cache.example.com", db, localStore, localStore, localStore, "")
			assert.ErrorIs(t, err, cache.ErrHostnameMustNotContainScheme)
		})

		t.Run("hostname must not contain a path", func(t *testing.T) {
			t.Parallel()

			dir, err := os.MkdirTemp("", "cache-path-")
			require.NoError(t, err)

			defer os.RemoveAll(dir) // clean up

			dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
			testhelper.CreateMigrateDatabase(t, dbFile)

			db, err := database.Open("sqlite:"+dbFile, nil)
			require.NoError(t, err)

			localStore, err := local.New(newContext(), dir)
			require.NoError(t, err)

			_, err = newTestCache(newContext(), "cache.example.com/path/to", db, localStore, localStore, localStore, "")
			assert.ErrorIs(t, err, cache.ErrHostnameMustNotContainPath)
		})

		t.Run("valid hostName must return no error", func(t *testing.T) {
			t.Parallel()

			dir, err := os.MkdirTemp("", "cache-path-")
			require.NoError(t, err)

			defer os.RemoveAll(dir) // clean up

			dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
			testhelper.CreateMigrateDatabase(t, dbFile)

			db, err := database.Open("sqlite:"+dbFile, nil)
			require.NoError(t, err)

			localStore, err := local.New(newContext(), dir)
			require.NoError(t, err)

			_, err = newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
			require.NoError(t, err)
		})
	})

	t.Run("secretKey", func(t *testing.T) {
		t.Parallel()

		t.Run("generated", func(t *testing.T) {
			t.Parallel()

			dir, err := os.MkdirTemp("", "cache-path-")
			require.NoError(t, err)

			defer os.RemoveAll(dir) // clean up

			dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
			testhelper.CreateMigrateDatabase(t, dbFile)

			db, err := database.Open("sqlite:"+dbFile, nil)
			require.NoError(t, err)

			localStore, err := local.New(newContext(), dir)
			require.NoError(t, err)

			c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
			require.NoError(t, err)

			sk, err := localStore.GetSecretKey(newContext())
			require.NoError(t, err)

			assert.Equal(t, sk.ToPublicKey(), c.PublicKey(), "ensure the cache public key matches the one in the local store")
		})

		t.Run("given", func(t *testing.T) {
			t.Parallel()

			dir, err := os.MkdirTemp("", "cache-path-")
			require.NoError(t, err)

			defer os.RemoveAll(dir) // clean up

			dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
			testhelper.CreateMigrateDatabase(t, dbFile)

			db, err := database.Open("sqlite:"+dbFile, nil)
			require.NoError(t, err)

			localStore, err := local.New(newContext(), dir)
			require.NoError(t, err)

			sk, _, err := signature.GenerateKeypair(cacheName, nil)
			require.NoError(t, err)

			skFile, err := os.CreateTemp("", "secret-key")
			require.NoError(t, err)

			defer os.Remove(skFile.Name())

			_, err = skFile.WriteString(sk.String())
			require.NoError(t, err)

			require.NoError(t, skFile.Close())

			c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, skFile.Name())
			require.NoError(t, err)

			_, err = localStore.GetSecretKey(newContext())
			require.ErrorIs(t, err, storage.ErrNotFound)

			assert.Equal(t, sk.ToPublicKey(), c.PublicKey(), "ensure the cache public key matches the one given")
		})
	})
}

func TestPublicKey(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	defer os.RemoveAll(dir) // clean up

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
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

func TestGetNarInfoWithoutSignature(t *testing.T) {
	t.Parallel()

	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	defer os.RemoveAll(dir) // clean up

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	c.SetRecordAgeIgnoreTouch(0)
	c.SetCacheSignNarinfo(false)

	// Wait for upstream caches to become available
	<-c.GetHealthChecker().Trigger()

	ni, err := c.GetNarInfo(context.Background(), testdata.Nar1.NarInfoHash)
	require.NoError(t, err)

	var found bool

	require.Len(t, ni.Signatures, 1, "must include our signature and the orignal one")

	var sig signature.Signature
	for _, sig = range ni.Signatures {
		if sig.Name == cacheName {
			found = true

			break
		}
	}

	assert.False(t, found)

	assert.False(t, signature.VerifyFirst(ni.Fingerprint(), ni.Signatures, []signature.PublicKey{c.PublicKey()}))
}

//nolint:paralleltest
func TestGetNarInfo(t *testing.T) {
	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	defer os.RemoveAll(dir) // clean up

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	c.SetRecordAgeIgnoreTouch(0)

	// Wait for upstream caches to become available
	<-c.GetHealthChecker().Trigger()

	t.Run("narinfo does not exist upstream", func(t *testing.T) {
		_, err := c.GetNarInfo(context.Background(), "doesnotexist")
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("narinfo exists upstream", func(t *testing.T) {
		t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
			assert.NoFileExists(t, filepath.Join(dir, "store", "narinfo", testdata.Nar2.NarInfoPath))
		})

		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			assert.NoFileExists(t, filepath.Join(dir, "store", "nar", testdata.Nar2.NarPath))
		})

		t.Run("narinfo does not exist in the database yet", func(t *testing.T) {
			rows, err := db.DB().Query("SELECT hash FROM narinfos")
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
			rows, err := db.DB().Query("SELECT hash FROM nar_files")
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

		ni, err := c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
		require.NoError(t, err)

		storePath := filepath.Join(dir, "store", "narinfo", testdata.Nar2.NarInfoPath)

		t.Run("size is correct", func(t *testing.T) {
			assert.Equal(t, uint64(50308), ni.FileSize)
		})

		t.Run("it should now exist in the store", func(t *testing.T) {
			assert.FileExists(t, storePath)
		})

		t.Run("it should be signed by our server", func(t *testing.T) {
			var found bool

			require.Len(t, ni.Signatures, 2, "must include our signature and the orignal one")

			var sig signature.Signature
			for _, sig = range ni.Signatures {
				if sig.Name == cacheName {
					found = true

					break
				}
			}

			assert.True(t, found)

			assert.True(t, signature.VerifyFirst(ni.Fingerprint(), ni.Signatures, []signature.PublicKey{c.PublicKey()}))
		})

		t.Run("it should not be signed twice by our server", func(t *testing.T) {
			ni, err := c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
			require.NoError(t, err)

			require.Len(t, ni.Signatures, 2, "must include our signature and the orignal one")

			var sigs1 []signature.Signature

			for _, sig := range ni.Signatures {
				if sig.Name == cacheName {
					sigs1 = append(sigs1, sig)
				}
			}

			require.Len(t, sigs1, 1)

			idx := ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/"+testdata.Nar2.NarInfoHash+".narinfo" {
					_, err := w.Write([]byte(ni.String()))
					if err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
					}

					return true
				}

				return false
			})
			defer ts.RemoveMaybeHandler(idx)

			require.NoError(t, os.Remove(storePath))

			ni, err = c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
			require.NoError(t, err)

			require.Len(t, ni.Signatures, 2, "must include our signature and the orignal one")

			var sigs2 []signature.Signature

			for _, sig := range ni.Signatures {
				if sig.Name == cacheName {
					sigs2 = append(sigs2, sig)
				}
			}

			require.Len(t, sigs2, 1)
		})

		t.Run("it should have also pulled the nar", func(t *testing.T) {
			// Force the other goroutine to run so it actually download the file
			// Try at least 10 times before announcing an error
			var err error

			for i := 1; i < 100; i++ {
				// NOTE: I tried runtime.Gosched() but it makes the test flaky
				time.Sleep(time.Duration(i) * time.Millisecond)

				_, err = os.Stat(filepath.Join(dir, "store", "nar", testdata.Nar2.NarPath))
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

			rows, err := db.DB().Query(query)
			require.NoError(t, err)

			nims := make([]database.NarInfo, 0)

			for rows.Next() {
				var nim database.NarInfo

				err := rows.Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt)
				require.NoError(t, err)

				nims = append(nims, nim)
			}

			require.NoError(t, rows.Err())

			assert.Len(t, nims, 1)
			assert.Equal(t, testdata.Nar2.NarInfoHash, nims[0].Hash)
			assert.WithinDuration(t, nims[0].CreatedAt, nims[0].LastAccessedAt.Time, 2*time.Second)
		})

		t.Run("nar does exist in the database, and has initial last_accessed_at", func(t *testing.T) {
			const query = `
				SELECT  hash,  created_at,  last_accessed_at
				FROM nar_files
				`

			rows, err := db.DB().Query(query)
			require.NoError(t, err)

			nims := make([]database.NarFile, 0)

			for rows.Next() {
				var nim database.NarFile

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
			assert.WithinDuration(t, nims[0].CreatedAt, nims[0].LastAccessedAt.Time, 2*time.Second)
		})

		t.Run("pulling it another time within recordAgeIgnoreTouch should not update last_accessed_at", func(t *testing.T) {
			time.Sleep(time.Second)

			c.SetRecordAgeIgnoreTouch(time.Hour)

			defer func() {
				c.SetRecordAgeIgnoreTouch(0)
			}()

			_, err := c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
			require.NoError(t, err)

			t.Run("narinfo does exist in the database with the same last_accessed_at", func(t *testing.T) {
				const query = `
			SELECT  hash, created_at,  last_accessed_at
			FROM narinfos
			`

				rows, err := db.DB().Query(query)
				require.NoError(t, err)

				nims := make([]database.NarInfo, 0)

				for rows.Next() {
					var nim database.NarInfo

					err := rows.Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt)
					require.NoError(t, err)

					nims = append(nims, nim)
				}

				require.NoError(t, rows.Err())

				assert.Len(t, nims, 1)
				assert.Equal(t, testdata.Nar2.NarInfoHash, nims[0].Hash)
				assert.WithinDuration(t, nims[0].CreatedAt, nims[0].LastAccessedAt.Time, 2*time.Second)
			})
		})

		t.Run("pulling it another time should update last_accessed_at only for narinfo", func(t *testing.T) {
			time.Sleep(time.Second)

			_, err := c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
			require.NoError(t, err)

			t.Run("narinfo does exist in the database, and has more recent last_accessed_at", func(t *testing.T) {
				const query = `
			SELECT  hash, created_at,  last_accessed_at
			FROM narinfos
			`

				rows, err := db.DB().Query(query)
				require.NoError(t, err)

				nims := make([]database.NarInfo, 0)

				for rows.Next() {
					var nim database.NarInfo

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
			require.NoError(t, os.Remove(filepath.Join(dir, "store", "narinfo", testdata.Nar2.NarInfoPath)))

			_, err := c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
			assert.NoError(t, err)
		})

		t.Run("nar does not exist in storage, it gets pulled automatically", func(t *testing.T) {
			narFile := filepath.Join(dir, "store", "nar", testdata.Nar2.NarPath)

			require.NoError(t, os.Remove(narFile))

			t.Run("it should not return an error", func(t *testing.T) {
				_, err := c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
				assert.NoError(t, err)
			})

			t.Run("it should have also pulled the nar", func(t *testing.T) {
				// Force the other goroutine to run so it actually download the file
				// Try at least 10 times before announcing an error
				var err error

				for i := 1; i < 100; i++ {
					// NOTE: I tried runtime.Gosched() but it makes the test flaky
					time.Sleep(time.Duration(i) * time.Millisecond)

					_, err = os.Stat(narFile)
					if err == nil {
						break
					}
				}

				assert.NoError(t, err)
			})
		})
	})

	t.Run("narinfo with transparent encryption", func(t *testing.T) {
		var allEntries []testdata.Entry

		// Filter for the entries that has `Compression: <something>` that does not
		// match the listed compression on the narEntry.
		for _, narEntry := range testdata.Entries {
			c := fmt.Sprintf("Compression: %s", narEntry.NarCompression)
			if narEntry.NarCompression == nar.CompressionTypeZstd && !strings.Contains(narEntry.NarInfoText, c) {
				allEntries = append(allEntries, narEntry)
			}
		}

		for i, narEntry := range allEntries {
			t.Run("nar idx"+strconv.Itoa(i)+" narInfoHash="+narEntry.NarInfoHash, func(t *testing.T) {
				narInfo, err := c.GetNarInfo(context.Background(), narEntry.NarInfoHash)
				require.NoError(t, err)

				storePath := filepath.Join(dir, "store", "nar", narEntry.NarPath)
				if assert.FileExists(t, storePath) {
					body, err := os.ReadFile(storePath)
					require.NoError(t, err)

					if assert.NotEqual(t, narEntry.NarText, string(body), "narText should be stored compressed in the store") {
						decoder, err := zstd.NewReader(nil)
						require.NoError(t, err)

						plain, err := decoder.DecodeAll(body, []byte{})
						require.NoError(t, err)

						assert.Equal(t, narEntry.NarText, string(plain),
							"body stored in the store decompressed should be the same as the narText")

						//nolint:gosec
						assert.Equal(t, int64(narInfo.FileSize), int64(len(body)),
							"narInfo FileSize should match the compressed file size")
					}
				}
			})
		}
	})
}

//nolint:paralleltest
func TestPutNarInfo(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	defer os.RemoveAll(dir) // clean up

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

	c.SetRecordAgeIgnoreTouch(0)

	storePath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)

	t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
		assert.NoFileExists(t, storePath)
	})

	t.Run("narinfo does not exist in the database yet", func(t *testing.T) {
		rows, err := db.DB().Query("SELECT hash FROM narinfos")
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
		rows, err := db.DB().Query("SELECT hash FROM nar_files")
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
			if sig.Name == cacheName {
				found = true

				break
			}
		}

		assert.True(t, found)

		assert.True(t, signature.VerifyFirst(ni.Fingerprint(), ni.Signatures, []signature.PublicKey{c.PublicKey()}))
	})

	t.Run("narinfo does exist in the database", func(t *testing.T) {
		rows, err := db.DB().Query("SELECT hash FROM narinfos")
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
		rows, err := db.DB().Query("SELECT hash FROM nar_files")
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

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

	c.SetRecordAgeIgnoreTouch(0)

	t.Run("file does not exist in the store", func(t *testing.T) {
		storePath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)

		t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
			assert.NoFileExists(t, storePath)
		})

		t.Run("DeleteNarInfo does return an error", func(t *testing.T) {
			err := c.DeleteNarInfo(context.Background(), testdata.Nar1.NarInfoHash)
			assert.ErrorIs(t, err, storage.ErrNotFound)
		})
	})

	t.Run("file does exist in the store", func(t *testing.T) {
		storePath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)

		t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
			assert.NoFileExists(t, storePath)
		})

		require.NoError(t, os.MkdirAll(filepath.Dir(storePath), 0o700))

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
	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	defer os.RemoveAll(dir) // clean up

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	c.SetRecordAgeIgnoreTouch(0)

	// Wait for upstream caches to become available
	<-c.GetHealthChecker().Trigger()

	t.Run("nar does not exist upstream", func(t *testing.T) {
		nu := nar.URL{Hash: "doesnotexist", Compression: nar.CompressionTypeXz}
		_, _, err := c.GetNar(context.Background(), nu)
		assert.ErrorIs(t, err, upstream.ErrNotFound)
	})

	t.Run("nar exists upstream", func(t *testing.T) {
		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			assert.NoFileExists(t, filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath))
		})

		t.Run("nar does not exist in database yet", func(t *testing.T) {
			rows, err := db.DB().Query("SELECT hash FROM nar_files")
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
			_, err := c.GetNarInfo(context.Background(), testdata.Nar1.NarInfoHash)
			assert.NoError(t, err)
		})

		nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}

		t.Run("able to get the NAR even in flight from upstream", func(t *testing.T) {
			_, r, err := c.GetNar(context.Background(), nu)
			require.NoError(t, err)

			t.Run("body is the same", func(t *testing.T) {
				body, err := io.ReadAll(r)
				require.NoError(t, err)

				if assert.Len(t, testdata.Nar1.NarText, len(string(body))) {
					assert.Equal(t, testdata.Nar1.NarText, string(body))
				}
			})
		})

		t.Run("it should now exist in the store", func(t *testing.T) {
			var err error

			for i := 1; i < 100; i++ {
				// NOTE: I tried runtime.Gosched() but it makes the test flaky
				time.Sleep(time.Duration(i) * time.Millisecond)

				_, err = os.Stat(filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath))
				if err == nil {
					break
				}
			}

			assert.NoError(t, err)
		})

		t.Run("nar does exist in the database, and has initial last_accessed_at", func(t *testing.T) {
			const query = `
				SELECT  hash,  created_at,  last_accessed_at
				FROM nar_files
				`

			rows, err := db.DB().Query(query)
			require.NoError(t, err)

			nims := make([]database.NarFile, 0)

			for rows.Next() {
				var nim database.NarFile

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
			assert.WithinDuration(t, nims[0].CreatedAt, nims[0].LastAccessedAt.Time, 2*time.Second)
		})

		t.Run("pulling it another time within recordAgeIgnoreTouch should not update last_accessed_at", func(t *testing.T) {
			time.Sleep(time.Second)

			c.SetRecordAgeIgnoreTouch(time.Hour)

			defer func() {
				c.SetRecordAgeIgnoreTouch(0)
			}()

			nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}

			_, r, err := c.GetNar(context.Background(), nu)
			require.NoError(t, err)

			defer r.Close()

			t.Run("narinfo does exist in the database with the same last_accessed_at", func(t *testing.T) {
				const query = `
				SELECT  hash,  created_at,  last_accessed_at
				FROM nar_files
				`

				rows, err := db.DB().Query(query)
				require.NoError(t, err)

				nims := make([]database.NarFile, 0)

				for rows.Next() {
					var nim database.NarFile

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
				assert.WithinDuration(t, nims[0].CreatedAt, nims[0].LastAccessedAt.Time, 2*time.Second)
			})
		})

		t.Run("pulling it another time should update last_accessed_at", func(t *testing.T) {
			time.Sleep(time.Second)

			nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}

			size, r, err := c.GetNar(context.Background(), nu)
			require.NoError(t, err)

			defer r.Close()

			t.Run("size is correct", func(t *testing.T) {
				assert.Equal(t, int64(len(testdata.Nar1.NarText)), size)
			})

			t.Run("narinfo does exist in the database, and has more recent last_accessed_at", func(t *testing.T) {
				const query = `
				SELECT  hash,  created_at,  last_accessed_at
				FROM nar_files
				`

				rows, err := db.DB().Query(query)
				require.NoError(t, err)

				nims := make([]database.NarFile, 0)

				for rows.Next() {
					var nim database.NarFile

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

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

	c.SetRecordAgeIgnoreTouch(0)

	storePath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)

	t.Run("nar does not exist in storage yet", func(t *testing.T) {
		assert.NoFileExists(t, storePath)
	})

	t.Run("putNar does not return an error", func(t *testing.T) {
		r := io.NopCloser(strings.NewReader(testdata.Nar1.NarText))

		nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}
		err := c.PutNar(context.Background(), nu, r)
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

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

	c.SetRecordAgeIgnoreTouch(0)

	storePath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)

	t.Run("file does not exist in the store", func(t *testing.T) {
		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			assert.NoFileExists(t, storePath)
		})

		t.Run("DeleteNar does return an error", func(t *testing.T) {
			nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}
			err := c.DeleteNar(context.Background(), nu)
			assert.ErrorIs(t, err, storage.ErrNotFound)
		})
	})

	t.Run("file does exist in the store", func(t *testing.T) {
		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			assert.NoFileExists(t, storePath)
		})

		require.NoError(t, os.MkdirAll(filepath.Dir(storePath), 0o700))

		f, err := os.Create(storePath)
		require.NoError(t, err)

		_, err = f.WriteString(testdata.Nar1.NarText)
		require.NoError(t, err)

		require.NoError(t, f.Close())

		t.Run("nar does exist in storage", func(t *testing.T) {
			assert.FileExists(t, storePath)
		})

		t.Run("deleteNar does not return an error", func(t *testing.T) {
			nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}
			err := c.DeleteNar(context.Background(), nu)
			assert.NoError(t, err)
		})

		t.Run("nar is gone from the store", func(t *testing.T) {
			assert.NoFileExists(t, storePath)
		})
	})
}

// TestDeadlock_NarInfo_Triggers_Nar_Refetch reproduces a deadlock where pulling a NarInfo
// triggers a Nar fetch (because compression is none), and both waiting on each other
// if they share the same lock/job key.
func TestDeadlock_NarInfo_Triggers_Nar_Refetch(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	defer os.RemoveAll(dir) // clean up

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

	// 1. Setup a test server
	ts := testdata.NewTestServer(t, 1)
	defer ts.Close()

	// CRITICAL: We must ensure NarInfoHash == NarHash to cause the collision in upstreamJobs map.
	// The deadlock happens because pullNarInfo starts a job with key=hash, and then prePullNar
	// tries to start a job with key=hash (derived from URL).

	// NarInfoHash is 32 chars.
	// narURL.Hash comes from URL.
	// We want narURL.Hash == NarInfoHash.
	collisionHash := "11111111111111111111111111111111"

	entry := testdata.Entry{
		NarInfoHash:    collisionHash,
		NarHash:        collisionHash,
		NarCompression: "none",
		NarInfoText: `StorePath: /nix/store/11111111111111111111111111111111-test-1.0
URL: nar/11111111111111111111111111111111.nar
Compression: none
FileHash: sha256:1111111111111111111111111111111111111111111111111111
FileSize: 123
NarHash: sha256:1111111111111111111111111111111111111111111111111111
NarSize: 123
References: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-dummy
Deriver: dddddddddddddddddddddddddddddddd-test-1.0.drv
Sig: cache.nixos.org-1:MadTCU1OSFCGUw4aqCKpLCZJpqBc7AbLvO7wgdlls0eq1DwaSnF/82SZE+wJGEiwlHbnZR+14daSaec0W3XoBQ==
`,
		NarText: "content-of-the-nar",
	}
	ts.AddEntry(entry)

	// Add debug handler to see what's being requested and serve content manually
	ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/"+collisionHash+".narinfo" {
			_, _ = w.Write([]byte(entry.NarInfoText))

			return true
		}

		if r.URL.Path == "/nar/"+collisionHash+".nar" {
			_, _ = w.Write([]byte(entry.NarText))

			return true
		}

		return false // Let the real handler process other things
	})

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), nil)
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)

	// Wait for health check
	select {
	case <-c.GetHealthChecker().Trigger():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for upstream health check")
	}

	// 2. Trigger the download
	// We use a timeout to detect the deadlock
	ctx, cancel := context.WithTimeout(newContext(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})

	var narInfo *narinfo.NarInfo

	go func() {
		defer close(done)

		narInfo, err = c.GetNarInfo(ctx, entry.NarInfoHash)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatal("Deadlock detected! GetNarInfo timed out.")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for GetNarInfo to complete")
	}

	require.NoError(t, err)
	assert.NotNil(t, narInfo)
}

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}
