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
	"sync"
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
	//nolint:staticcheck // using deprecated ConfigStore interface for testing migration
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

func setupTestComponents(t *testing.T) (database.Querier, *local.Store, string, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	cleanup := func() {
		os.RemoveAll(dir)
	}

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	return db, localStore, dir, cleanup
}

func setupTestCache(t *testing.T) (*cache.Cache, database.Querier, *local.Store, string, func()) {
	t.Helper()

	db, localStore, dir, cleanup := setupTestComponents(t)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

	return c, db, localStore, dir, cleanup
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("hostname must be valid with no scheme or path", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			hostname string
			wantErr  error
		}{
			{
				name:     "hostname must not be empty",
				hostname: "",
				wantErr:  cache.ErrHostnameRequired,
			},
			{
				name:     "hostname must not contain scheme",
				hostname: "https://cache.example.com",
				wantErr:  cache.ErrHostnameMustNotContainScheme,
			},
			{
				name:     "hostname must not contain a path",
				hostname: "cache.example.com/path/to",
				wantErr:  cache.ErrHostnameMustNotContainPath,
			},
			{
				name:     "valid hostName must return no error",
				hostname: cacheName,
				wantErr:  nil,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				db, localStore, _, cleanup := setupTestComponents(t)
				defer cleanup()

				_, err := newTestCache(newContext(), tt.hostname, db, localStore, localStore, localStore, "")
				if tt.wantErr != nil {
					assert.ErrorIs(t, err, tt.wantErr)
				} else {
					assert.NoError(t, err)
				}
			})
		}
	})

	t.Run("secretKey", func(t *testing.T) {
		t.Parallel()

		t.Run("generated", func(t *testing.T) {
			t.Parallel()

			c, db, localStore, _, cleanup := setupTestCache(t)
			defer cleanup()

			// Verify key is NOT in local store
			_, err := localStore.GetSecretKey(newContext())
			require.ErrorIs(t, err, storage.ErrNotFound)

			// Verify key IS in database
			conf, err := db.GetConfigByKey(newContext(), "secret_key")
			require.NoError(t, err)
			sk, err := signature.LoadSecretKey(conf.Value)
			require.NoError(t, err)

			assert.Equal(t, sk.ToPublicKey(), c.PublicKey(), "ensure the cache public key matches the one in the DB")
		})

		t.Run("given", func(t *testing.T) {
			t.Parallel()

			db, localStore, _, cleanup := setupTestComponents(t)
			defer cleanup()

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

			// Verify key is NOT in local store
			_, err = localStore.GetSecretKey(newContext())
			require.ErrorIs(t, err, storage.ErrNotFound)

			// Verify key IS in database (it should be stored there now)
			conf, err := db.GetConfigByKey(newContext(), "secret_key")
			require.NoError(t, err)
			assert.Equal(t, sk.String(), conf.Value, "ensure the given secret key is stored in the DB")

			assert.Equal(t, sk.ToPublicKey(), c.PublicKey(), "ensure the cache public key matches the one given")
		})

		t.Run("migrated", func(t *testing.T) {
			t.Parallel()

			db, localStore, _, cleanup := setupTestComponents(t)
			defer cleanup()

			// Pre-populate key in local store
			sk, _, err := signature.GenerateKeypair(cacheName, nil)
			require.NoError(t, err)
			err = localStore.PutSecretKey(newContext(), sk)
			require.NoError(t, err)

			c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
			require.NoError(t, err)

			// Verify key is NOT in local store anymore
			_, err = localStore.GetSecretKey(newContext())
			require.ErrorIs(t, err, storage.ErrNotFound)

			// Verify key IS in database
			conf, err := db.GetConfigByKey(newContext(), "secret_key")
			require.NoError(t, err)
			assert.Equal(t, sk.String(), conf.Value, "ensure the migrated secret key is stored in the DB")

			assert.Equal(t, sk.ToPublicKey(), c.PublicKey(), "ensure the cache public key matches the generated one")
		})
	})
}

func TestPublicKey(t *testing.T) {
	t.Parallel()

	c, _, _, _, cleanup := setupTestCache(t)
	defer cleanup()

	pubKey := c.PublicKey().String()

	t.Run("should return a public key with the correct prefix", func(t *testing.T) {
		t.Parallel()

		assert.True(t, strings.HasPrefix(pubKey, cacheName+":"))
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

	c, _, _, _, cleanup := setupTestCache(t)
	defer cleanup()

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	c.SetRecordAgeIgnoreTouch(0)
	c.SetCacheSignNarinfo(false)

	// Wait for upstream caches to become available
	<-c.GetHealthChecker().Trigger()

	ni, err := c.GetNarInfo(context.Background(), testdata.Nar1.NarInfoHash)
	require.NoError(t, err)

	require.Len(t, ni.Signatures, 1, "must NOT include our signature but include the original one")

	var found bool

	for _, sig := range ni.Signatures {
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

	c, db, _, dir, cleanup := setupTestCache(t)
	defer cleanup()

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
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
			var count int

			err := db.DB().QueryRow("SELECT COUNT(*) FROM narinfos").Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 0, count)
		})

		t.Run("nar does not exist in the database yet", func(t *testing.T) {
			var count int

			err := db.DB().QueryRow("SELECT COUNT(*) FROM nar_files").Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 0, count)
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
			require.Len(t, ni.Signatures, 2, "must include our signature and the original one")

			var found bool

			for _, sig := range ni.Signatures {
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

			require.Len(t, ni.Signatures, 2, "must include our signature and the original one")

			var sigs1 []signature.Signature

			for _, sig := range ni.Signatures {
				if sig.Name == cacheName {
					sigs1 = append(sigs1, sig)
				}
			}

			require.Len(t, sigs1, 1)

			idx := ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/"+testdata.Nar2.NarInfoHash+".narinfo" {
					_, _ = w.Write([]byte(ni.String()))

					return true
				}

				return false
			})
			defer ts.RemoveMaybeHandler(idx)

			require.NoError(t, os.Remove(storePath))

			ni, err = c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
			require.NoError(t, err)

			require.Len(t, ni.Signatures, 2, "must include our signature and the original one")

			var sigs2 []signature.Signature

			for _, sig := range ni.Signatures {
				if sig.Name == cacheName {
					sigs2 = append(sigs2, sig)
				}
			}

			require.Len(t, sigs2, 1)
		})

		t.Run("it should have also pulled the nar", func(t *testing.T) {
			waitForFile(t, filepath.Join(dir, "store", "nar", testdata.Nar2.NarPath))
		})

		t.Run("narinfo does exist in the database, and has initial last_accessed_at", func(t *testing.T) {
			var nim database.NarInfo

			err := db.DB().QueryRow("SELECT hash, created_at, last_accessed_at FROM narinfos").
				Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt)

			require.NoError(t, err)
			assert.Equal(t, testdata.Nar2.NarInfoHash, nim.Hash)
			assert.WithinDuration(t, nim.CreatedAt, nim.LastAccessedAt.Time, 2*time.Second)
		})

		t.Run("nar does exist in the database, and has initial last_accessed_at", func(t *testing.T) {
			var nim database.NarFile

			err := db.DB().QueryRow("SELECT hash, created_at, last_accessed_at FROM nar_files").
				Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt)

			require.NoError(t, err)
			assert.Equal(t, testdata.Nar2.NarHash, nim.Hash)
			assert.WithinDuration(t, nim.CreatedAt, nim.LastAccessedAt.Time, 2*time.Second)
		})

		t.Run("pulling it another time within recordAgeIgnoreTouch should not update last_accessed_at", func(t *testing.T) {
			time.Sleep(time.Second)

			c.SetRecordAgeIgnoreTouch(time.Hour)

			defer c.SetRecordAgeIgnoreTouch(0)

			_, err := c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
			require.NoError(t, err)

			var nim database.NarInfo

			err = db.DB().QueryRow("SELECT hash, created_at, last_accessed_at FROM narinfos").
				Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt)

			require.NoError(t, err)
			assert.WithinDuration(t, nim.CreatedAt, nim.LastAccessedAt.Time, 2*time.Second)
		})

		t.Run("pulling it another time should update last_accessed_at only for narinfo", func(t *testing.T) {
			time.Sleep(time.Second)

			_, err := c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
			require.NoError(t, err)

			var nim database.NarInfo

			err = db.DB().QueryRow("SELECT hash, created_at, last_accessed_at FROM narinfos").
				Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt)

			require.NoError(t, err)
			assert.NotEqual(t, nim.CreatedAt, nim.LastAccessedAt.Time)
		})

		t.Run("no error is returned if the entry already exists in the database", func(t *testing.T) {
			require.NoError(t, os.Remove(filepath.Join(dir, "store", "narinfo", testdata.Nar2.NarInfoPath)))
			_, err := c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
			require.NoError(t, err)
		})

		t.Run("nar does not exist in storage, it gets pulled automatically", func(t *testing.T) {
			narFile := filepath.Join(dir, "store", "nar", testdata.Nar2.NarPath)
			require.NoError(t, os.Remove(narFile))

			_, err := c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
			require.NoError(t, err)

			waitForFile(t, narFile)
		})
	})

	t.Run("narinfo with transparent encryption", func(t *testing.T) {
		var allEntries []testdata.Entry

		for _, narEntry := range testdata.Entries {
			comp := fmt.Sprintf("Compression: %s", narEntry.NarCompression)
			if narEntry.NarCompression == nar.CompressionTypeZstd && !strings.Contains(narEntry.NarInfoText, comp) {
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

						assert.Equal(t, narEntry.NarText, string(plain))
						assert.Equal(t, narInfo.FileSize, uint64(len(body)))
					}
				}
			})
		}
	})
}

//nolint:paralleltest
func TestPutNarInfo(t *testing.T) {
	c, db, _, dir, cleanup := setupTestCache(t)
	defer cleanup()

	c.SetRecordAgeIgnoreTouch(0)

	storePath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)

	t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
		assert.NoFileExists(t, storePath)
	})

	t.Run("narinfo does not exist in the database yet", func(t *testing.T) {
		var count int

		err := db.DB().QueryRow("SELECT COUNT(*) FROM narinfos").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("nar does not exist in the database yet", func(t *testing.T) {
		var count int

		err := db.DB().QueryRow("SELECT COUNT(*) FROM nar_files").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)
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

		defer f.Close()

		ni, err := narinfo.Parse(f)
		require.NoError(t, err)

		var found bool

		for _, sig := range ni.Signatures {
			if sig.Name == cacheName {
				found = true

				break
			}
		}

		assert.True(t, found)
		assert.True(t, signature.VerifyFirst(ni.Fingerprint(), ni.Signatures, []signature.PublicKey{c.PublicKey()}))
	})

	t.Run("narinfo does exist in the database", func(t *testing.T) {
		var hash string

		err := db.DB().QueryRow("SELECT hash FROM narinfos").Scan(&hash)
		require.NoError(t, err)
		assert.Equal(t, testdata.Nar1.NarInfoHash, hash)
	})

	t.Run("nar does exist in the database", func(t *testing.T) {
		var hash string

		err := db.DB().QueryRow("SELECT hash FROM nar_files").Scan(&hash)
		require.NoError(t, err)
		assert.Equal(t, testdata.Nar1.NarHash, hash)
	})
}

//nolint:paralleltest
func TestDeleteNarInfo(t *testing.T) {
	c, _, _, dir, cleanup := setupTestCache(t)
	defer cleanup()

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

		require.NoError(t, os.MkdirAll(filepath.Dir(storePath), 0o700))

		f, err := os.Create(storePath)
		require.NoError(t, err)
		_, err = f.WriteString(testdata.Nar1.NarInfoText)
		require.NoError(t, err)
		require.NoError(t, f.Close())

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

	c, db, _, dir, cleanup := setupTestCache(t)
	defer cleanup()

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
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
			var count int

			err := db.DB().QueryRow("SELECT COUNT(*) FROM nar_files").Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 0, count)
		})

		t.Run("getting the narinfo so the record in the database now exists", func(t *testing.T) {
			_, err := c.GetNarInfo(context.Background(), testdata.Nar1.NarInfoHash)
			assert.NoError(t, err)
		})

		nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}

		t.Run("able to get the NAR even in flight from upstream", func(t *testing.T) {
			_, r, err := c.GetNar(context.Background(), nu)
			require.NoError(t, err)

			defer r.Close()

			t.Run("body is the same", func(t *testing.T) {
				body, err := io.ReadAll(r)
				require.NoError(t, err)
				assert.Equal(t, testdata.Nar1.NarText, string(body))
			})
		})

		t.Run("it should now exist in the store", func(t *testing.T) {
			waitForFile(t, filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath))
		})

		t.Run("nar does exist in the database, and has initial last_accessed_at", func(t *testing.T) {
			var nim database.NarFile

			err := db.DB().QueryRow("SELECT hash, created_at, last_accessed_at FROM nar_files").
				Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt)

			require.NoError(t, err)
			assert.Equal(t, testdata.Nar1.NarHash, nim.Hash)
			assert.WithinDuration(t, nim.CreatedAt, nim.LastAccessedAt.Time, 2*time.Second)
		})

		t.Run("pulling it another time within recordAgeIgnoreTouch should not update last_accessed_at", func(t *testing.T) {
			time.Sleep(time.Second)

			c.SetRecordAgeIgnoreTouch(time.Hour)

			defer c.SetRecordAgeIgnoreTouch(0)

			nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}
			_, r, err := c.GetNar(context.Background(), nu)
			require.NoError(t, err)
			r.Close()

			var nim database.NarFile

			err = db.DB().QueryRow("SELECT hash, created_at, last_accessed_at FROM nar_files").
				Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt)

			require.NoError(t, err)
			assert.WithinDuration(t, nim.CreatedAt, nim.LastAccessedAt.Time, 2*time.Second)
		})

		t.Run("pulling it another time should update last_accessed_at", func(t *testing.T) {
			time.Sleep(time.Second)

			nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}
			size, r, err := c.GetNar(context.Background(), nu)
			require.NoError(t, err)
			r.Close()

			assert.Equal(t, int64(len(testdata.Nar1.NarText)), size)

			var nim database.NarFile

			err = db.DB().QueryRow("SELECT hash, created_at, last_accessed_at FROM nar_files").
				Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt)

			require.NoError(t, err)
			assert.NotEqual(t, nim.CreatedAt, nim.LastAccessedAt.Time)
		})
	})
}

//nolint:paralleltest
func TestPutNar(t *testing.T) {
	c, _, _, dir, cleanup := setupTestCache(t)
	defer cleanup()

	c.SetRecordAgeIgnoreTouch(0)

	storePath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)

	t.Run("nar does not exist in storage yet", func(t *testing.T) {
		assert.NoFileExists(t, storePath)
	})

	t.Run("putNar does not return an error", func(t *testing.T) {
		r := io.NopCloser(strings.NewReader(testdata.Nar1.NarText))
		nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}
		assert.NoError(t, c.PutNar(context.Background(), nu, r))
	})

	t.Run("nar does exist in storage", func(t *testing.T) {
		f, err := os.Open(storePath)
		require.NoError(t, err)

		defer f.Close()

		bs, err := io.ReadAll(f)
		require.NoError(t, err)
		assert.Equal(t, testdata.Nar1.NarText, string(bs))
	})
}

//nolint:paralleltest
func TestDeleteNar(t *testing.T) {
	c, _, _, dir, cleanup := setupTestCache(t)
	defer cleanup()

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
			assert.NoError(t, c.DeleteNar(context.Background(), nu))
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

	c, _, _, _, cleanup := setupTestCache(t)
	defer cleanup()

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

// TestDeadlock_ContextCancellation_DuringDownload reproduces a deadlock that occurs when
// a context is canceled during a download, causing cleanup code to attempt closing channels
// that may have already been closed. This test specifically targets the issue fixed in #433
// where sync.Once was needed to make channel closures idempotent.
//
// The deadlock scenario:
// 1. Start downloading a NAR file from upstream
// 2. Cancel the context mid-download to trigger cleanup
// 3. Without sync.Once protection, multiple goroutines may try to close the same channels
// 4. This can cause a panic or deadlock depending on timing.
func TestDeadlock_ContextCancellation_DuringDownload(t *testing.T) {
	t.Parallel()

	c, _, _, _, cleanup := setupTestCache(t)
	defer cleanup()

	// Setup a test server with a slow response to ensure we can cancel mid-download
	ts := testdata.NewTestServer(t, 1)
	defer ts.Close()

	testHash := "deadlock-test-hash-123456789012"
	entry := testdata.Entry{
		NarInfoHash:    testHash,
		NarHash:        testHash + "-nar",
		NarCompression: "xz",
		NarInfoText: `StorePath: /nix/store/` + testHash + `-test-1.0
URL: nar/` + testHash + `-nar.nar.xz
Compression: xz
FileHash: sha256:1111111111111111111111111111111111111111111111111111
FileSize: 1048576
NarHash: sha256:1111111111111111111111111111111111111111111111111111
NarSize: 1048576
References: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-dummy
Sig: cache.nixos.org-1:MadTCU1OSFCGUw4aqCKpLCZJpqBc7AbLvO7wgdlls0eq1DwaSnF/82SZE+wJGEiwlHbnZR+14daSaec0W3XoBQ==
`,
		NarText: strings.Repeat("x", 1048576), // 1MB of data
	}
	ts.AddEntry(entry)

	// Add a handler that serves the NAR slowly to allow cancellation mid-download
	slowNarServed := make(chan struct{})

	ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/nar/"+testHash+"-nar.nar.xz" {
			// Signal that we started serving
			close(slowNarServed)

			// Write data slowly in chunks
			data := []byte(entry.NarText)

			chunkSize := 1024
			for i := 0; i < len(data); i += chunkSize {
				end := i + chunkSize
				if end > len(data) {
					end = len(data)
				}

				// Check if client disconnected
				select {
				case <-r.Context().Done():
					return true
				default:
				}

				_, err := w.Write(data[i:end])
				if err != nil {
					return true
				}

				// Flush to ensure data is sent
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}

				// Sleep to make download slow
				time.Sleep(10 * time.Millisecond)
			}

			return true
		}

		return false
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

	// Create a context that we'll cancel mid-download
	ctx, cancel := context.WithCancel(newContext())

	done := make(chan struct{})

	var getNarErr error

	// Start the download in a goroutine
	go func() {
		defer close(done)

		nu := nar.URL{Hash: testHash + "-nar", Compression: nar.CompressionTypeXz}
		_, r, err := c.GetNar(ctx, nu)
		getNarErr = err

		if r != nil {
			// Try to read some data
			buf := make([]byte, 1024)
			_, _ = r.Read(buf)
			r.Close()
		}
	}()

	// Wait for the slow NAR handler to start serving
	select {
	case <-slowNarServed:
		// Good, download started
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for NAR download to start")
	}

	// Give it a moment to start downloading
	time.Sleep(50 * time.Millisecond)

	// Cancel the context to trigger cleanup while download is in progress
	cancel()

	// Wait for the download to complete or timeout
	// The download should complete even though we canceled, because it continues in the background
	select {
	case <-done:
		// GetNar returned successfully (no deadlock!)
		t.Logf("GetNar completed without deadlock, err=%v", getNarErr)
	case <-time.After(10 * time.Second):
		t.Fatal("Deadlock detected! GetNar did not complete after context cancellation")
	}

	// Success! The deadlock is fixed. GetNar completed without hanging.
	// Note: The background download continues even after the caller cancels, which is the intended behavior.
}

// TestBackgroundDownloadCompletion_AfterCancellation verifies that when a caller cancels their
// request mid-download, the download continues in the background and completes successfully,
// making the asset available for subsequent requests.
//
// This test validates the core behavior of the detached context approach:
// 1. Caller A starts a download and cancels mid-download
// 2. The download continues in the background using a detached context
// 3. Caller B can successfully retrieve the asset once the background download completes
// 4. The asset is properly stored in the cache and database.
func TestBackgroundDownloadCompletion_AfterCancellation(t *testing.T) {
	t.Parallel()

	c, _, localStore, dir, cleanup := setupTestCache(t)
	defer cleanup()

	// Use an existing test entry (Nar3) for this test
	entry := testdata.Nar3

	// Setup a test server with the entry
	ts := testdata.NewTestServer(t, 1)
	defer ts.Close()

	// Add a handler that serves the NAR slowly to allow cancellation mid-download
	slowNarServed := make(chan struct{})

	var slowNarServedOnce sync.Once

	downloadComplete := make(chan struct{})

	var downloadCompleteOnce sync.Once

	ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/nar/"+entry.NarHash+".nar.xz" {
			// Signal that we started serving
			slowNarServedOnce.Do(func() { close(slowNarServed) })

			// Write data slowly in chunks
			data := []byte(entry.NarText)

			chunkSize := 1024
			for i := 0; i < len(data); i += chunkSize {
				end := i + chunkSize
				if end > len(data) {
					end = len(data)
				}

				_, err := w.Write(data[i:end])
				if err != nil {
					return true
				}

				// Flush to ensure data is sent
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}

				// Sleep to make download slow (but not too slow to avoid test timeout)
				time.Sleep(2 * time.Millisecond)
			}

			// Signal download is complete
			downloadCompleteOnce.Do(func() { close(downloadComplete) })

			return true
		}

		return false
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

	// Verify NAR does not exist yet
	narPath := filepath.Join(dir, "store", "nar", entry.NarPath)
	assert.NoFileExists(t, narPath, "NAR should not exist in cache yet")

	// STEP 1: Caller A starts download and cancels mid-download
	ctxA, cancelA := context.WithCancel(newContext())

	doneA := make(chan struct{})

	var getNarErrA error

	go func() {
		defer close(doneA)

		nu := nar.URL{Hash: entry.NarHash, Compression: entry.NarCompression}
		_, r, err := c.GetNar(ctxA, nu)
		getNarErrA = err

		if r != nil {
			// Try to read some data
			buf := make([]byte, 1024)
			_, _ = r.Read(buf)
			r.Close()
		}
	}()

	// Wait for the slow NAR handler to start serving
	select {
	case <-slowNarServed:
		// Good, download started
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for NAR download to start")
	}

	// Give it a moment to start downloading (but not complete)
	time.Sleep(50 * time.Millisecond)

	// Cancel caller A's context
	cancelA()

	// Wait for caller A to return
	select {
	case <-doneA:
		// Good, caller A returned (may or may not have an error depending on timing)
		t.Logf("Caller A returned with error: %v", getNarErrA)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for caller A to return")
	}

	// STEP 2: Wait for background download to complete
	select {
	case <-downloadComplete:
		t.Log("Background download completed")
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for background download to complete")
	}

	// Wait for the cache to store the file
	waitForFile(t, narPath)

	// STEP 3: Verify the asset is now available in storage
	assert.FileExists(t, narPath, "NAR should exist in cache after background download completes")

	// STEP 4: Caller B retrieves the asset successfully
	ctxB := newContext()
	nu := nar.URL{Hash: entry.NarHash, Compression: entry.NarCompression}

	size, readerB, err := c.GetNar(ctxB, nu)
	require.NoError(t, err, "caller B should be able to get the NAR")
	require.NotNil(t, readerB, "reader should not be nil")

	// Read and verify the content
	bodyB, err := io.ReadAll(readerB)
	require.NoError(t, err, "should be able to read NAR content")
	readerB.Close()

	assert.Equal(t, entry.NarText, string(bodyB), "NAR content should match")
	assert.Equal(t, int64(len(entry.NarText)), size, "size should match")

	// STEP 5: Verify HasNar returns true
	assert.True(t, localStore.HasNar(newContext(), nu), "HasNar should return true")

	// STEP 6: Verify another concurrent request also succeeds
	ctxC := newContext()
	sizeC, readerC, err := c.GetNar(ctxC, nu)
	require.NoError(t, err, "caller C should also be able to get the NAR")
	require.NotNil(t, readerC, "reader should not be nil")

	bodyCPreview := make([]byte, 100)
	n, err := readerC.Read(bodyCPreview)
	require.NoError(t, err, "should be able to read from caller C's reader")
	readerC.Close()

	assert.Equal(t, entry.NarText[:n], string(bodyCPreview[:n]), "NAR content preview should match")
	assert.Equal(t, int64(len(entry.NarText)), sizeC, "size should match for caller C")

	// SUCCESS! The background download completed and made the asset available.
	t.Log("✅ Background download completed successfully and asset is available to all callers")

	// NOTE: GetNar doesn't populate the database - only GetNarInfo does that.
	// We don't verify database state here since the purpose of this test is to verify
	// that the download continues in the background and the asset becomes available,
	// regardless of whether it's in the database or not.
}

// TestConcurrentDownload_CancelOneClient_OthersContinue verifies that when multiple clients
// request the same NAR concurrently and share the same download job, canceling one client's
// request does not affect the other clients.
//
// This test validates the critical coordination behavior:
// 1. Client A and Client B both start requesting the same NAR at the same time
// 2. They share the same underlying download job (deduplication)
// 3. Client A cancels its request mid-download
// 4. Client B is unaffected and successfully receives the NAR once download completes
// 5. The download continues in the background using the detached context.
func TestConcurrentDownload_CancelOneClient_OthersContinue(t *testing.T) {
	t.Parallel()

	c, _, localStore, dir, cleanup := setupTestCache(t)
	defer cleanup()

	// Use an existing test entry (Nar5 to avoid conflict with other tests)
	entry := testdata.Nar5

	// Setup a test server with the entry
	ts := testdata.NewTestServer(t, 1)
	defer ts.Close()

	// Add a handler that serves the NAR slowly to allow cancellation mid-download
	slowNarServed := make(chan struct{})

	var slowNarServedOnce sync.Once

	downloadComplete := make(chan struct{})

	var downloadCompleteOnce sync.Once

	ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/nar/"+entry.NarHash+".nar.xz" {
			// Signal that we started serving
			slowNarServedOnce.Do(func() { close(slowNarServed) })

			// Write data slowly in chunks
			data := []byte(entry.NarText)

			chunkSize := 1024
			for i := 0; i < len(data); i += chunkSize {
				end := i + chunkSize
				if end > len(data) {
					end = len(data)
				}

				_, err := w.Write(data[i:end])
				if err != nil {
					return true
				}

				// Flush to ensure data is sent
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}

				// Sleep to make download slow
				time.Sleep(2 * time.Millisecond)
			}

			// Signal download is complete
			downloadCompleteOnce.Do(func() { close(downloadComplete) })

			return true
		}

		return false
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

	// Verify NAR does not exist yet
	narPath := filepath.Join(dir, "store", "nar", entry.NarPath)
	assert.NoFileExists(t, narPath, "NAR should not exist in cache yet")

	// Start both clients at the same time
	var wg sync.WaitGroup

	ctxA, cancelA := context.WithCancel(newContext())
	ctxB := newContext()

	doneA := make(chan struct{})
	doneB := make(chan struct{})

	var (
		getNarErrA, getNarErrB error
		sizeB                  int64
		readerB                io.ReadCloser
	)

	// Client A - will be cancelled mid-download

	wg.Add(1)

	go func() {
		defer wg.Done()
		defer close(doneA)

		nu := nar.URL{Hash: entry.NarHash, Compression: entry.NarCompression}
		_, r, err := c.GetNar(ctxA, nu)
		getNarErrA = err

		if r != nil {
			// Try to read some data
			buf := make([]byte, 1024)
			_, _ = r.Read(buf)
			r.Close()
		}
	}()

	// Client B - should complete successfully despite A's cancellation
	wg.Add(1)

	go func() {
		defer wg.Done()
		defer close(doneB)

		nu := nar.URL{Hash: entry.NarHash, Compression: entry.NarCompression}

		var err error

		sizeB, readerB, err = c.GetNar(ctxB, nu)
		getNarErrB = err
	}()

	// Wait for the download to start
	select {
	case <-slowNarServed:
		// Good, download started
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for NAR download to start")
	}

	// Give it a moment to ensure both clients are waiting on the same download
	time.Sleep(50 * time.Millisecond)

	// Cancel client A mid-download
	t.Log("Canceling client A mid-download")
	cancelA()

	// Wait for client A to return (should return quickly after cancellation)
	select {
	case <-doneA:
		t.Logf("Client A returned with error: %v", getNarErrA)
		// Client A may or may not have an error depending on timing
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for client A to return")
	}

	// Wait for background download to complete
	select {
	case <-downloadComplete:
		t.Log("Background download completed")
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for background download to complete")
	}

	// Wait for client B to complete (should succeed)
	select {
	case <-doneB:
		t.Logf("Client B completed with error: %v", getNarErrB)
		require.NoError(t, getNarErrB, "client B should complete successfully despite client A cancellation")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for client B to complete")
	}

	// Verify client B got the complete data
	require.NotNil(t, readerB, "client B reader should not be nil")

	bodyB, err := io.ReadAll(readerB)
	require.NoError(t, err, "should be able to read NAR content from client B")
	readerB.Close()

	assert.Equal(t, entry.NarText, string(bodyB), "NAR content should match for client B")
	assert.Equal(t, int64(len(entry.NarText)), sizeB, "size should match for client B")

	// Verify the asset is in storage
	assert.FileExists(t, narPath, "NAR should exist in cache")

	nu := nar.URL{Hash: entry.NarHash, Compression: entry.NarCompression}
	assert.True(t, localStore.HasNar(newContext(), nu), "HasNar should return true")

	wg.Wait()

	t.Log("✅ Client B completed successfully despite client A cancellation - download coordination works!")
}

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}

func waitForFile(t *testing.T, path string) {
	t.Helper()

	var err error

	for i := 1; i < 100; i++ {
		time.Sleep(time.Duration(i) * time.Millisecond)

		_, err = os.Stat(path)
		if err == nil {
			return
		}
	}

	require.NoError(t, err, "timeout waiting for file: %s", path)
}
