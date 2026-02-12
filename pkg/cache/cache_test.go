package cache_test

import (
	"context"
	"database/sql"
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

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	cleanup := func() {
		db.DB().Close()
		os.RemoveAll(dir)
	}

	return db, localStore, dir, cleanup
}

func setupTestCache(t *testing.T) (*cache.Cache, database.Querier, *local.Store, string, func()) {
	t.Helper()

	db, localStore, dir, cleanupComponents := setupTestComponents(t)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

	cleanup := func() {
		c.Close()
		cleanupComponents()
	}

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

			err := db.DB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM narinfos").Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 0, count)
		})

		t.Run("nar does not exist in the database yet", func(t *testing.T) {
			var count int

			err := db.DB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM nar_files").Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 0, count)
		})

		ni, err := c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
		require.NoError(t, err)

		t.Run("size is correct", func(t *testing.T) {
			assert.Equal(t, uint64(50308), ni.FileSize)
		})

		t.Run("it should now exist in the database (not storage)", func(t *testing.T) {
			// Narinfos are now stored only in the database, not in storage
			var count int

			err := db.DB().QueryRowContext(context.Background(),
				"SELECT COUNT(*) FROM narinfos WHERE hash = ?",
				testdata.Nar2.NarInfoHash).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "narinfo should exist in database")
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

			// Remove narinfo from database (since it's no longer in storage)
			_, err = db.DB().ExecContext(context.Background(),
				"DELETE FROM narinfos WHERE hash = ?", testdata.Nar2.NarInfoHash)
			require.NoError(t, err)

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

			err := db.DB().QueryRowContext(context.Background(),
				"SELECT hash, created_at, last_accessed_at FROM narinfos").
				Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt)

			require.NoError(t, err)
			assert.Equal(t, testdata.Nar2.NarInfoHash, nim.Hash)
			assert.WithinDuration(t, nim.CreatedAt, nim.LastAccessedAt.Time, 2*time.Second)
		})

		t.Run("nar does exist in the database, and has initial last_accessed_at", func(t *testing.T) {
			var nim database.NarFile

			err := db.DB().QueryRowContext(context.Background(), "SELECT hash, created_at, last_accessed_at FROM nar_files").
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

			err = db.DB().QueryRowContext(context.Background(), "SELECT hash, created_at, last_accessed_at FROM narinfos").
				Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt)

			require.NoError(t, err)
			assert.WithinDuration(t, nim.CreatedAt, nim.LastAccessedAt.Time, 2*time.Second)
		})

		t.Run("pulling it another time should update last_accessed_at only for narinfo", func(t *testing.T) {
			time.Sleep(time.Second)

			_, err := c.GetNarInfo(context.Background(), testdata.Nar2.NarInfoHash)
			require.NoError(t, err)

			var nim database.NarInfo

			err = db.DB().QueryRowContext(context.Background(), "SELECT hash, created_at, last_accessed_at FROM narinfos").
				Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt)

			require.NoError(t, err)
			assert.NotEqual(t, nim.CreatedAt, nim.LastAccessedAt.Time)
		})

		t.Run("no error is returned if the entry already exists in the database", func(t *testing.T) {
			narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar2.NarInfoPath)
			if _, err := os.Stat(narInfoPath); err == nil {
				require.NoError(t, os.Remove(narInfoPath))
			}

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

		err := db.DB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM narinfos").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("nar does not exist in the database yet", func(t *testing.T) {
		var count int

		err := db.DB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM nar_files").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("PutNarInfo does not return an error", func(t *testing.T) {
		r := io.NopCloser(strings.NewReader(testdata.Nar1.NarInfoText))
		assert.NoError(t, c.PutNarInfo(context.Background(), testdata.Nar1.NarInfoHash, r))
	})

	t.Run("narinfo should NOT exist in storage (only in database)", func(t *testing.T) {
		assert.NoFileExists(t, storePath)
	})

	t.Run("it should be signed by our server", func(t *testing.T) {
		// Query database directly to check signatures since GetNarInfo would purge
		// the narinfo if the NAR file doesn't exist (which it doesn't in this test)
		var sigsStr []string

		rows, err := db.DB().QueryContext(context.Background(),
			`SELECT signature FROM narinfo_signatures
			 WHERE narinfo_id = (SELECT id FROM narinfos WHERE hash = ?)`,
			testdata.Nar1.NarInfoHash)
		require.NoError(t, err)

		defer rows.Close()

		for rows.Next() {
			var sigStr string

			require.NoError(t, rows.Scan(&sigStr))
			sigsStr = append(sigsStr, sigStr)
		}

		require.NoError(t, rows.Err())

		assert.GreaterOrEqual(t, len(sigsStr), 2, "narinfo should have at least 2 signatures")

		var parsedSigs []signature.Signature

		for _, sigStr := range sigsStr {
			sig, err := signature.ParseSignature(sigStr)
			require.NoError(t, err)

			parsedSigs = append(parsedSigs, sig)
		}

		ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
		require.NoError(t, err)

		var found bool

		for _, sig := range parsedSigs {
			if sig.Name == cacheName {
				found = true

				break
			}
		}

		assert.True(t, found, "cache signature should be present")
		assert.True(t, signature.VerifyFirst(ni.Fingerprint(), parsedSigs, []signature.PublicKey{c.PublicKey()}),
			"cache signature should be valid")
	})

	t.Run("narinfo does exist in the database", func(t *testing.T) {
		var hash string

		err := db.DB().QueryRowContext(context.Background(), "SELECT hash FROM narinfos").Scan(&hash)
		require.NoError(t, err)
		assert.Equal(t, testdata.Nar1.NarInfoHash, hash)
	})

	t.Run("nar does exist in the database", func(t *testing.T) {
		var hash string

		err := db.DB().QueryRowContext(context.Background(), "SELECT hash FROM nar_files").Scan(&hash)
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

			err := db.DB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM nar_files").Scan(&count)
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

			err := db.DB().QueryRowContext(context.Background(), "SELECT hash, created_at, last_accessed_at FROM nar_files").
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

			err = db.DB().QueryRowContext(context.Background(), "SELECT hash, created_at, last_accessed_at FROM nar_files").
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

			err = db.DB().QueryRowContext(context.Background(), "SELECT hash, created_at, last_accessed_at FROM nar_files").
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

func TestGetNarInfo_MigratesInvalidURL(t *testing.T) {
	t.Parallel()

	c, db, localStore, _, cleanup := setupTestCache(t)
	defer cleanup()

	c.SetRecordAgeIgnoreTouch(0)

	// 1. Put NarInfo into the file store (Storage) ONLY
	// We use localStore directly to avoid the Cache.PutNarInfo logic which would write to the DB.
	ctx := context.Background()

	niParsed, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
	require.NoError(t, err)

	require.NoError(t, localStore.PutNarInfo(ctx, testdata.Nar1.NarInfoHash, niParsed))

	nu := nar.URL{
		Hash:        testdata.Nar1.NarHash,
		Compression: testdata.Nar1.NarCompression,
	}
	_, err = localStore.PutNar(ctx, nu, io.NopCloser(strings.NewReader(testdata.Nar1.NarText)))
	require.NoError(t, err)

	// 2. Insert a minimal record into the database
	// This simulates a record created before the de-normalization migration (schema 20260117195000)
	// or a record that was only partially created. The key aspect is that URL is NULL.
	query := "INSERT INTO narinfos (hash, created_at) VALUES (?, ?)"
	_, err = db.DB().ExecContext(ctx, query, testdata.Nar1.NarInfoHash, time.Now())
	require.NoError(t, err)

	// Verify it is indeed NULL and correctly inserted
	var url sql.NullString

	err = db.DB().QueryRowContext(ctx, "SELECT url FROM narinfos WHERE hash = ?", testdata.Nar1.NarInfoHash).Scan(&url)
	require.NoError(t, err)
	require.False(t, url.Valid, "URL should be NULL before the test")

	// 3. Call GetNarInfo
	// This should trigger the background migration (if fixed)
	ni, err := c.GetNarInfo(ctx, testdata.Nar1.NarInfoHash)
	require.NoError(t, err)
	// SHA256 of the NAR file (compressed)
	assert.Equal(t, "sha256:1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri", ni.FileHash.String())

	// 4. Verify DB record is updated
	expectedURL := "nar/1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri.nar.xz"

	assert.Eventually(t, func() bool {
		err = db.DB().QueryRowContext(ctx, "SELECT url FROM narinfos WHERE hash = ?", testdata.Nar1.NarInfoHash).Scan(&url)

		return err == nil && url.Valid && url.String == expectedURL
	}, 2*time.Second, 100*time.Millisecond, "URL should be populated in the database after GetNarInfo")

	// 5. Verify the narinfo is gone from the store (migration logic includes deleting from store)
	assert.Eventually(t, func() bool {
		return !localStore.HasNarInfo(ctx, testdata.Nar1.NarInfoHash)
	}, 2*time.Second, 100*time.Millisecond, "NarInfo should be removed from the store after migration")
}

func TestGetNarInfo_ConcurrentMigrationAttempts(t *testing.T) {
	t.Parallel()

	c, db, localStore, _, cleanup := setupTestCache(t)
	defer cleanup()

	c.SetRecordAgeIgnoreTouch(0)

	ctx := context.Background()

	// 1. Setup: Put NarInfo in storage and insert minimal DB record with NULL URL
	niParsed, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
	require.NoError(t, err)

	require.NoError(t, localStore.PutNarInfo(ctx, testdata.Nar1.NarInfoHash, niParsed))

	nu := nar.URL{
		Hash:        testdata.Nar1.NarHash,
		Compression: testdata.Nar1.NarCompression,
	}
	_, err = localStore.PutNar(ctx, nu, io.NopCloser(strings.NewReader(testdata.Nar1.NarText)))
	require.NoError(t, err)

	query := "INSERT INTO narinfos (hash, created_at) VALUES (?, ?)"
	_, err = db.DB().ExecContext(ctx, query, testdata.Nar1.NarInfoHash, time.Now())
	require.NoError(t, err)

	// 2. Trigger multiple concurrent GetNarInfo requests
	const concurrency = 10

	var wg sync.WaitGroup

	errChan := make(chan error, concurrency)
	results := make([]*narinfo.NarInfo, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()

			ni, err := c.GetNarInfo(ctx, testdata.Nar1.NarInfoHash)
			if err != nil {
				errChan <- err

				return
			}

			results[idx] = ni
		}(i)
	}

	wg.Wait()
	close(errChan)

	// 3. Verify all requests succeeded
	for err := range errChan {
		require.NoError(t, err, "All concurrent requests should succeed")
	}

	// 4. Verify all results are identical
	for i, result := range results {
		require.NotNil(t, result, "Result %d should not be nil", i)
		assert.Equal(t, "sha256:1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri", result.FileHash.String())
	}

	// 5. Verify DB was updated exactly once (eventually, due to background migration)
	expectedURL := "nar/1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri.nar.xz"

	var url sql.NullString

	assert.Eventually(t, func() bool {
		err = db.DB().QueryRowContext(ctx, "SELECT url FROM narinfos WHERE hash = ?", testdata.Nar1.NarInfoHash).Scan(&url)

		return err == nil && url.Valid && url.String == expectedURL
	}, 2*time.Second, 100*time.Millisecond, "URL should be populated exactly once")

	// 6. Verify storage deletion happened (background migration deletes from storage)
	assert.Eventually(t, func() bool {
		return !localStore.HasNarInfo(ctx, testdata.Nar1.NarInfoHash)
	}, 2*time.Second, 100*time.Millisecond, "NarInfo should be removed from store after migration")
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
	collisionHash := "1111111111111111111111111111111111111111111111111111"

	entry := testdata.Entry{
		NarInfoHash:    collisionHash,
		NarHash:        collisionHash,
		NarCompression: "none",
		NarInfoText: `StorePath: /nix/store/1111111111111111111111111111111111111111111111111111-test-1.0
URL: nar/1111111111111111111111111111111111111111111111111111.nar
Compression: none
FileHash: sha256:1111111111111111111111111111111111111111111111111111
FileSize: 123
NarHash: sha256:1111111111111111111111111111111111111111111111111111
NarSize: 123
References: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-dummy
Deriver: dddddddddddddddddddddddddddddddddddddddddddddddddddd-test-1.0.drv
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
	slowNarRequestDone := make(chan struct{})

	ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/nar/"+testHash+"-nar.nar.xz" {
			defer close(slowNarRequestDone)

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

	// Wait for the slow handler to finish to avoid "httptest.Server blocked in Close"
	select {
	case <-slowNarRequestDone:
	case <-time.After(15 * time.Second):
		t.Fatal("handler did not finish within 5s after context cancellation")
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

	if sizeB != -1 {
		assert.Equal(t, int64(len(entry.NarText)), sizeB, "size should match for client B")
	}

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

func TestGetNarInfo_BackgroundMigration(t *testing.T) { //nolint:paralleltest
	c, db, _, dir, cleanup := setupTestCache(t)
	defer cleanup()

	hash := testdata.Nar1.NarInfoHash
	narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
	narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)

	// 1. Manually put the NarInfo and Nar into storage but NOT in the database
	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o700))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o700))
	require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

	// Verify it's not in the database
	var count int

	err := db.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM narinfos WHERE hash = ?", hash).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Clear it from DB first
	_, err = db.DB().ExecContext(context.Background(), "DELETE FROM narinfos WHERE hash = ?", hash)
	require.NoError(t, err)

	// Ensure it's in storage
	if _, err := os.Stat(narInfoPath); os.IsNotExist(err) {
		require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))
	}

	// Call GetNarInfo
	_, err = c.GetNarInfo(context.Background(), hash)
	require.NoError(t, err)

	// Wait for background migration and deletion
	require.Eventually(t, func() bool {
		var count int

		err := db.DB().QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM narinfos WHERE hash = ?", hash).Scan(&count)
		if err != nil || count == 0 {
			return false
		}

		_, err = os.Stat(narInfoPath)

		return os.IsNotExist(err)
	}, 5*time.Second, 100*time.Millisecond)
}

type migrationSpy struct {
	database.Querier
	getNarInfoByHashCalls *int
	createNarInfoCalls    *int
	mu                    *sync.Mutex
}

func (s *migrationSpy) GetNarInfoByHash(ctx context.Context, hash string) (database.NarInfo, error) {
	s.mu.Lock()
	*s.getNarInfoByHashCalls++
	s.mu.Unlock()

	return s.Querier.GetNarInfoByHash(ctx, hash)
}

func (s *migrationSpy) CreateNarInfo(
	ctx context.Context,
	params database.CreateNarInfoParams,
) (database.NarInfo, error) {
	s.mu.Lock()
	*s.createNarInfoCalls++
	s.mu.Unlock()

	return s.Querier.CreateNarInfo(ctx, params)
}

func (s *migrationSpy) WithTx(tx *sql.Tx) database.Querier {
	return &migrationSpy{
		Querier:               s.Querier.WithTx(tx),
		getNarInfoByHashCalls: s.getNarInfoByHashCalls,
		createNarInfoCalls:    s.createNarInfoCalls,
		mu:                    s.mu,
	}
}

func (s *migrationSpy) DB() *sql.DB {
	return s.Querier.DB()
}

func TestBackgroundMigrateNarInfo_ThunderingHerd(t *testing.T) {
	t.Parallel()

	// Setup components
	db, localStore, dir, cleanup := setupTestComponents(t)
	defer cleanup()

	hash := testdata.Nar1.NarInfoHash
	narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
	narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)

	// Manually put the NarInfo and Nar into storage but NOT in the database
	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o700))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o700))
	require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

	// Create a spy that captures calls to GetNarInfoByHash
	spy := &migrationSpy{
		Querier:               db,
		getNarInfoByHashCalls: new(int),
		createNarInfoCalls:    new(int),
		mu:                    new(sync.Mutex),
	}

	// Increase MaxOpenConns to avoid deadlocks during concurrent transactions in the test
	db.DB().SetMaxOpenConns(10)

	c, err := newTestCache(newContext(), "test.example.com", spy, localStore, localStore, localStore, "")
	require.NoError(t, err)

	// Call GetNarInfo multiple times concurrently
	const concurrency = 3

	var wg sync.WaitGroup

	t.Logf("Starting %d concurrent GetNarInfo calls", concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)

		go func(id int) {
			defer wg.Done()

			t.Logf("[%d] Calling GetNarInfo", id)

			_, err := c.GetNarInfo(context.Background(), hash)
			t.Logf("[%d] GetNarInfo returned: err=%v", id, err)
		}(i)
	}

	// Wait for all calls to finish (they should return quickly because migrations are background)
	wg.Wait()

	// Wait for the background migration to complete.
	require.Eventually(t, func() bool {
		var count int

		err := spy.DB().
			QueryRowContext(context.Background(), "SELECT COUNT(*) FROM narinfos WHERE hash = ?", hash).
			Scan(&count)

		return err == nil && count > 0
	}, 5*time.Second, 100*time.Millisecond, "background migration should complete")

	spy.mu.Lock()
	count := *spy.createNarInfoCalls
	spy.mu.Unlock()

	// If count > 1, we have a thundering herd!
	// We expect count to be 1 because only one background migration should proceed.
	assert.Equal(t, 1, count,
		"Thundering herd detected: %d CreateNarInfo call(s) for %d concurrent requests",
		count, concurrency)
	t.Logf("Detected %d concurrent CreateNarInfo calls", count)
}

func TestBackgroundMigrateNarInfo_AfterCancellation(t *testing.T) {
	t.Parallel()

	c, db, _, dir, cleanup := setupTestCache(t)
	defer cleanup()

	// Use a unique hash for this test
	entry := testdata.Nar2
	hash := entry.NarInfoHash
	narInfoPath := filepath.Join(dir, "store", "narinfo", entry.NarInfoPath)
	narPath := filepath.Join(dir, "store", "nar", entry.NarPath)

	// 1. Manually put the NarInfo and Nar into storage but NOT in the database
	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o700))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(entry.NarInfoText), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o700))
	require.NoError(t, os.WriteFile(narPath, []byte(entry.NarText), 0o600))

	// Verify it's not in the database
	var count int

	err := db.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM narinfos WHERE hash = ?", hash).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// 2. Call GetNarInfo with a context that we'll cancel
	ctx, cancel := context.WithCancel(newContext())

	// Start GetNarInfo in a goroutine
	done := make(chan struct{})

	go func() {
		defer close(done)

		_, _ = c.GetNarInfo(ctx, hash)
	}()

	// Give it a tiny bit of time to reach the DB check and trigger the background migration
	time.Sleep(50 * time.Millisecond)

	// 3. Cancel the context immediately to simulate a disconnected client
	cancel()
	<-done

	// 4. Wait for background migration to complete
	// If the implementation incorrectly uses the request context for the background job,
	// this might fail because the background job will be canceled.
	require.Eventually(t, func() bool {
		var count int

		err := db.DB().QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM narinfos WHERE hash = ?", hash).Scan(&count)

		return err == nil && count > 0
	}, 10*time.Second, 100*time.Millisecond, "background migration should complete even if request context is canceled")

	// 5. Verify it's in the database
	err = db.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM narinfos WHERE hash = ?", hash).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestGetNarInfo_ConcurrentPutNarInfoDuringMigration(t *testing.T) { //nolint:paralleltest
	// This test verifies that if a PutNarInfo operation occurs while a background
	// migration is happening for the same hash, both operations handle the duplicate
	// key error correctly and the final state is consistent.
	ctx := newContext()
	tmpDir := t.TempDir()
	dbFile := filepath.Join(tmpDir, "ncps.db")

	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	store, err := local.New(ctx, tmpDir)
	require.NoError(t, err)

	hash := testdata.Nar1.NarInfoHash
	entry := testdata.Nar1

	// Create a cache with background migration enabled
	c, err := newTestCache(ctx, cacheName, db, store, store, store, "")
	require.NoError(t, err)

	// 1. Pre-populate storage with narinfo (simulating old data before migration)
	narInfoPath := filepath.Join(tmpDir, "store", "narinfo", entry.NarInfoPath)
	narPath := filepath.Join(tmpDir, "store", "nar", entry.NarPath)

	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o700))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(entry.NarInfoText), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o700))
	require.NoError(t, os.WriteFile(narPath, []byte(entry.NarText), 0o600))

	// Verify it's not in the database
	var count int

	err = db.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM narinfos WHERE hash = ?", hash).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// 2. Start GetNarInfo which will trigger background migration
	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()

		ni, err := c.GetNarInfo(ctx, hash)
		assert.NoError(t, err)
		assert.NotNil(t, ni)
	}()

	// Give GetNarInfo time to start the background migration
	time.Sleep(100 * time.Millisecond)

	// 3. Concurrently call PutNarInfo with the same hash
	//    This simulates a client uploading the same narinfo while migration is happening
	wg.Add(1)

	go func() {
		defer wg.Done()

		narInfoReader := io.NopCloser(strings.NewReader(entry.NarInfoText))
		err := c.PutNarInfo(ctx, hash, narInfoReader)

		// PutNarInfo should either succeed or handle the duplicate gracefully
		assert.NoError(t, err)
	}()

	// Wait for both operations to complete
	wg.Wait()

	// 4. Verify final state: narinfo should be in database exactly once
	err = db.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM narinfos WHERE hash = ?", hash).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "narinfo should exist exactly once in database")

	// 5. Verify we can still read it back
	ni, err := c.GetNarInfo(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, ni)
	assert.NotEmpty(t, ni.StorePath, "narinfo should have a store path")
}

func TestGetNarInfo_MultipleConcurrentPutsDuringMigration(t *testing.T) { //nolint:paralleltest
	// This test simulates multiple concurrent PutNarInfo operations for the same
	// hash while a background migration is happening. This is a more extreme version
	// of the thundering herd scenario.
	ctx := newContext()
	tmpDir := t.TempDir()
	dbFile := filepath.Join(tmpDir, "ncps.db")

	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	// Increase connection pool for concurrent operations
	db.DB().SetMaxOpenConns(20)

	store, err := local.New(ctx, tmpDir)
	require.NoError(t, err)

	hash := testdata.Nar1.NarInfoHash
	entry := testdata.Nar1

	c, err := newTestCache(ctx, cacheName, db, store, store, store, "")
	require.NoError(t, err)

	defer c.Close()
	defer db.DB().Close()

	// Pre-populate storage
	narInfoPath := filepath.Join(tmpDir, "store", "narinfo", entry.NarInfoPath)
	narPath := filepath.Join(tmpDir, "store", "nar", entry.NarPath)

	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o700))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(entry.NarInfoText), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o700))
	require.NoError(t, os.WriteFile(narPath, []byte(entry.NarText), 0o600))

	// Start multiple concurrent operations
	const numConcurrent = 10

	var wg sync.WaitGroup

	wg.Add(numConcurrent)

	for i := 0; i < numConcurrent; i++ {
		go func(n int) {
			defer wg.Done()

			if n%2 == 0 {
				// Half do GetNarInfo (triggers migration)
				ni, err := c.GetNarInfo(ctx, hash)
				assert.NoError(t, err)
				assert.NotNil(t, ni)
			} else {
				// Half do PutNarInfo
				narInfoReader := io.NopCloser(strings.NewReader(entry.NarInfoText))
				err := c.PutNarInfo(ctx, hash, narInfoReader)
				assert.NoError(t, err)
			}
		}(i)
	}

	wg.Wait()

	// Verify final state: exactly one record
	var count int

	err = db.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM narinfos WHERE hash = ?", hash).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "should have exactly one narinfo record despite concurrent operations")

	// Verify that the legacy narinfo was deleted from storage (migration complete)
	assert.Eventually(t, func() bool {
		_, err := os.Stat(narInfoPath)

		return os.IsNotExist(err)
	}, 10*time.Second, 100*time.Millisecond, "legacy narinfo should be deleted after migration")

	// Verify the record is correct
	ni, err := c.GetNarInfo(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, ni)
	assert.NotEmpty(t, ni.StorePath, "narinfo should have a store path")
}

func TestNarStreaming(t *testing.T) {
	t.Parallel()

	// Setup test components
	c, _, localStore, _, cleanup := setupTestCache(t)
	defer cleanup()

	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	narEntry := testdata.Nar1
	narURL := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}

	// Use a channel to coordinate the slow upstream response
	continueServer := make(chan struct{})
	serverStarted := make(chan struct{})

	// Add a handler that simulates a slow download
	ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/nar/"+narEntry.NarHash+".nar.xz" {
			// Use simplified size for streaming test - actual content doesn't matter
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)

			// Write the first byte
			_, _ = w.Write([]byte("a"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}

			close(serverStarted)
			<-continueServer

			// Write the rest
			_, _ = w.Write([]byte(string(make([]byte, 99))))

			return true
		}

		return false
	})

	uc, err := upstream.New(context.Background(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)
	c.AddUpstreamCaches(context.Background(), uc)
	<-c.GetHealthChecker().Trigger()

	// Start GetNar in a goroutine
	var wg sync.WaitGroup
	wg.Add(1)

	streamingStarted := make(chan struct{})

	var (
		firstByteRead bool
		getNarErr     error
	)

	go func() {
		defer wg.Done()

		_, r, err := c.GetNar(context.Background(), narURL)
		if err != nil {
			getNarErr = err

			return
		}
		defer r.Close()

		// Try to read the first byte
		buf := make([]byte, 1)

		n, err := r.Read(buf)
		if err == nil && n == 1 {
			firstByteRead = true

			close(streamingStarted)
		}

		// Continue reading everything else
		_, _ = io.ReadAll(r)
	}()

	// Wait for server to start and write the first byte
	select {
	case <-serverStarted:
		// Server started
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for server to start")
	}

	// Check if streaming started (it should if we have fixed it)
	select {
	case <-streamingStarted:
		// Success!
	case <-time.After(5 * time.Second):
		t.Error("Streaming should have started but it did not")
	}

	// Now allow the server to finish
	close(continueServer)
	wg.Wait()

	require.NoError(t, getNarErr)
	assert.True(t, firstByteRead)

	// Verify the asset is in storage
	nu := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}
	assert.True(t, localStore.HasNar(context.Background(), nu), "NAR should exist in storage after streaming completes")
}

// storageWithHook wraps a local.Store and allows injecting behavior.
type storageWithHook struct {
	*local.Store
	beforeGetNarInfo func(hash string)
	beforeHasNarInfo func(hash string)
}

func (s *storageWithHook) GetNarInfo(ctx context.Context, hash string) (*narinfo.NarInfo, error) {
	if s.beforeGetNarInfo != nil {
		s.beforeGetNarInfo(hash)
	}

	return s.Store.GetNarInfo(ctx, hash)
}

func (s *storageWithHook) HasNarInfo(ctx context.Context, hash string) bool {
	if s.beforeHasNarInfo != nil {
		s.beforeHasNarInfo(hash)

		// For the deterministic race test, we want to return true
		// even if the hook just deleted the file, to simulate
		// that GetNarInfo already decided it's a hit.
		return true
	}

	return s.Store.HasNarInfo(ctx, hash)
}

// TestGetNarInfo_RaceConditionDuringMigrationDeletion tests the race condition where:
// 1. GetNarInfo checks database (not found or NULL URL)
// 2. GetNarInfo checks HasNarInfo (returns true - file exists)
// 3. Migration runs concurrently: writes to database AND deletes from storage
// 4. GetNarInfo tries to read from storage (file now deleted!)
// 5. Expected: Should retry database and succeed (migration completed)
// 6. Current Bug: Returns error because storage read fails.
func TestGetNarInfo_RaceConditionDuringMigrationDeletion(t *testing.T) { //nolint:paralleltest
	ctx := newContext()
	tmpDir := t.TempDir()
	dbFile := filepath.Join(tmpDir, "ncps.db")

	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	defer db.DB().Close()

	baseStore, err := local.New(ctx, tmpDir)
	require.NoError(t, err)

	hash := testdata.Nar1.NarInfoHash
	entry := testdata.Nar1

	// Create a partial database record (simulating what GetNarInfo creates as a placeholder)
	// This has hash but NULL URL, which causes getNarInfoFromDatabase to return ErrNotFound
	_, err = db.DB().ExecContext(ctx, `
		INSERT INTO narinfos (hash, store_path, url, compression, file_hash, file_size, nar_hash, nar_size)
		VALUES (?, '', NULL, '', '', 0, '', 0)
	`, hash)
	require.NoError(t, err)

	// Put narinfo and nar files in storage (simulating legacy data)
	narInfoPath := filepath.Join(tmpDir, "store", "narinfo", entry.NarInfoPath)
	narPath := filepath.Join(tmpDir, "store", "nar", entry.NarPath)

	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o700))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(entry.NarInfoText), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o700))
	require.NoError(t, os.WriteFile(narPath, []byte(entry.NarText), 0o600))

	// Channel to coordinate the race
	migrationComplete := make(chan struct{})

	var migrationErr error

	// Wrap the store to inject migration at the critical moment
	storeWithHook := &storageWithHook{
		Store: baseStore,
		beforeGetNarInfo: func(h string) {
			if h == hash {
				// This is the critical race window:
				// GetNarInfo has checked HasNarInfo (returned true)
				// Now it's about to call GetNarInfo on storage
				// We trigger migration here which will delete the file

				// Parse the narinfo for migration
				ni, parseErr := baseStore.GetNarInfo(ctx, hash)
				require.NoError(t, parseErr, "should be able to read narinfo before migration")

				// Run migration (writes to DB and deletes from storage)
				locker := locklocal.NewLocker()
				migrationErr = cache.MigrateNarInfo(ctx, locker, db, baseStore, hash, ni)

				close(migrationComplete)

				// Now the file is deleted from storage!
				// When GetNarInfo continues, it will try to read a file that no longer exists
			}
		},
	}

	// Create cache with the hooked store (same for all three store types)
	c, err := newTestCache(ctx, cacheName, db, storeWithHook, storeWithHook, storeWithHook, "")
	require.NoError(t, err)

	defer c.Close()

	// Call GetNarInfo - this will trigger the race condition
	ni, err := c.GetNarInfo(ctx, hash)

	// Wait for migration to complete
	<-migrationComplete
	require.NoError(t, migrationErr, "migration should succeed")

	// This is where the bug manifests:
	// Current behavior: err != nil (storage read failed, file was deleted)
	// Expected behavior: err == nil (should retry database after storage failure)
	//
	// After the fix, GetNarInfo should:
	// 1. Try database -> ErrNotFound (NULL URL)
	// 2. Check HasNarInfo -> true
	// 3. Try storage -> fails (migration deleted file)
	// 4. Retry database -> SUCCESS (migration completed)
	require.NoError(t, err, "GetNarInfo should succeed by retrying database after storage deletion")
	require.NotNil(t, ni)
	assert.NotEmpty(t, ni.StorePath)

	// Verify the narinfo is now in the database with full data
	var dbURL sql.NullString

	err = db.DB().QueryRowContext(ctx,
		"SELECT url FROM narinfos WHERE hash = ?", hash).Scan(&dbURL)
	require.NoError(t, err)
	assert.True(t, dbURL.Valid, "URL should be populated after migration")
	assert.NotEmpty(t, dbURL.String)

	// Verify the narinfo was deleted from storage (migration cleanup)
	_, statErr := os.Stat(narInfoPath)
	assert.True(t, os.IsNotExist(statErr), "narinfo should be deleted from storage after migration")
}

func TestGetNarInfo_RaceWithPutNarInfoDeterministic(t *testing.T) { //nolint:paralleltest
	// This test determines if legacy narinfo is deleted even if PutNarInfo
	// finishes before GetNarInfo can trigger migration.
	ctx := newContext()
	tmpDir := t.TempDir()
	dbFile := filepath.Join(tmpDir, "ncps.db")

	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	defer db.DB().Close()

	baseStore, err := local.New(ctx, tmpDir)
	require.NoError(t, err)

	hash := testdata.Nar2.NarInfoHash
	entry := testdata.Nar2

	// Put narinfo and nar files in storage (simulating legacy data)
	narInfoPath := filepath.Join(tmpDir, "store", "narinfo", entry.NarInfoPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o700))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(entry.NarInfoText), 0o600))

	narPath := filepath.Join(tmpDir, "store", "nar", entry.NarPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o700))
	require.NoError(t, os.WriteFile(narPath, []byte(entry.NarText), 0o600))

	// Channel to coordinate the race
	putFinished := make(chan struct{})

	var putOnce sync.Once

	// Wrap the store to inject PutNarInfo at the critical moment
	storeWithHook := &storageWithHook{
		Store: baseStore,
		beforeHasNarInfo: func(h string) {
			if h == hash {
				putOnce.Do(func() {
					// This is the critical race window:
					// 1. GetNarInfo has checked the database and found nothing.
					// 2. It is now checking if the file exists in storage.
					// 3. We use this hook to trigger PutNarInfo which will:
					//    - Insert the record into the database.
					//    - (If fixed) Delete the file from storage.
					// 4. Then GetNarInfo will continue.

					// Create a separate cache instance for the concurrent PutNarInfo
					// to avoid locking issues within the same instance.
					c2, err := newTestCache(ctx, cacheName, db, baseStore, baseStore, baseStore, "")
					require.NoError(t, err)

					defer c2.Close()

					narInfoReader := io.NopCloser(strings.NewReader(entry.NarInfoText))
					err = c2.PutNarInfo(ctx, hash, narInfoReader)
					require.NoError(t, err)

					close(putFinished)
				})
			}
		},
	}

	// Create cache with the hooked store
	c, err := newTestCache(ctx, cacheName, db, storeWithHook, storeWithHook, storeWithHook, "")
	require.NoError(t, err)

	defer c.Close()

	// Call GetNarInfo
	_, err = c.GetNarInfo(ctx, hash)
	require.NoError(t, err)

	// Ensure PutNarInfo actually ran
	select {
	case <-putFinished:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatal("PutNarInfo was not triggered or did not complete in time")
	}

	// Verify the narinfo was deleted from storage
	_, statErr := os.Stat(narInfoPath)
	assert.True(t, os.IsNotExist(statErr), "legacy narinfo should be deleted from storage")
}
