package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	locklocal "github.com/kalbasit/ncps/pkg/lock/local"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/server"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

const cacheName = "cache.example.com"

func newTestCache(
	ctx context.Context,
	db database.Querier,
	configStore storage.ConfigStore,
	narInfoStore storage.NarInfoStore,
	narStore storage.NarStore,
) (*cache.Cache, error) {
	downloadLocker := locklocal.NewLocker()
	cacheLocker := locklocal.NewRWLocker()

	return cache.New(ctx, cacheName, db, configStore, narInfoStore, narStore, "",
		downloadLocker, cacheLocker, 5*time.Minute, 30*time.Minute)
}

//nolint:paralleltest
func TestServeHTTP(t *testing.T) {
	hts := testdata.NewTestServer(t, 40)
	defer hts.Close()

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, hts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	t.Run("GET /pubkey", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		defer os.RemoveAll(dir) // clean up

		dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
		testhelper.CreateMigrateDatabase(t, dbFile)

		db, err := database.Open("sqlite:"+dbFile, nil)
		require.NoError(t, err)

		localStore, err := local.New(newContext(), dir)
		require.NoError(t, err)

		c, err := newTestCache(newContext(), db, localStore, localStore, localStore)
		require.NoError(t, err)

		c.AddUpstreamCaches(newContext(), uc)
		c.SetRecordAgeIgnoreTouch(0)

		// Wait for upstream caches to become available
		<-c.GetHealthChecker().Trigger()

		s := server.New(c)

		ts := httptest.NewServer(s)
		defer ts.Close()

		url := ts.URL + "/pubkey"

		r, err := http.NewRequestWithContext(newContext(), http.MethodGet, url, nil)
		require.NoError(t, err)

		resp, err := ts.Client().Do(r)
		require.NoError(t, err)

		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, c.PublicKey().String(), string(body))
	})

	t.Run("DELETE requests", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		defer os.RemoveAll(dir) // clean up

		dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
		testhelper.CreateMigrateDatabase(t, dbFile)

		db, err := database.Open("sqlite:"+dbFile, nil)
		require.NoError(t, err)

		localStore, err := local.New(newContext(), dir)
		require.NoError(t, err)

		c, err := newTestCache(newContext(), db, localStore, localStore, localStore)
		require.NoError(t, err)

		c.AddUpstreamCaches(newContext(), uc)
		c.SetRecordAgeIgnoreTouch(0)

		// Wait for upstream caches to become available
		<-c.GetHealthChecker().Trigger()

		t.Run("DELETE is not permitted", func(t *testing.T) {
			s := server.New(c)
			s.SetDeletePermitted(false)

			ts := httptest.NewServer(s)
			defer ts.Close()

			t.Run("narInfo", func(t *testing.T) {
				url := ts.URL + "/" + testdata.Nar1.NarInfoHash + ".narinfo"

				r, err := http.NewRequestWithContext(newContext(), http.MethodDelete, url, nil)
				require.NoError(t, err)

				resp, err := ts.Client().Do(r)
				require.NoError(t, err)

				assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
			})

			t.Run("nar", func(t *testing.T) {
				url := ts.URL + "/nar/" + testdata.Nar1.NarHash + ".nar.xz"

				r, err := http.NewRequestWithContext(newContext(), http.MethodDelete, url, nil)
				require.NoError(t, err)

				resp, err := ts.Client().Do(r)
				require.NoError(t, err)

				assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
			})
		})

		t.Run("DELETE is permitted", func(t *testing.T) {
			s := server.New(c)
			s.SetDeletePermitted(true)

			ts := httptest.NewServer(s)
			defer ts.Close()

			t.Run("narInfo", func(t *testing.T) {
				storePath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)

				t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
					assert.NoFileExists(t, storePath)
				})

				_, err := c.GetNarInfo(newContext(), testdata.Nar1.NarInfoHash)
				require.NoError(t, err)

				t.Run("narinfo does exist in storage", func(t *testing.T) {
					assert.FileExists(t, storePath)
				})

				t.Run("DELETE returns no error", func(t *testing.T) {
					url := ts.URL + "/" + testdata.Nar1.NarInfoHash + ".narinfo"

					r, err := http.NewRequestWithContext(newContext(), http.MethodDelete, url, nil)
					require.NoError(t, err)

					resp, err := ts.Client().Do(r)
					require.NoError(t, err)

					assert.Equal(t, http.StatusNoContent, resp.StatusCode)
				})

				t.Run("narinfo is gone from the store", func(t *testing.T) {
					assert.NoFileExists(t, storePath)
				})
			})

			t.Run("nar", func(t *testing.T) {
				storePath := filepath.Join(dir, "store", "nar", testdata.Nar2.NarPath)

				t.Run("nar does not exist in storage yet", func(t *testing.T) {
					assert.NoFileExists(t, storePath)
				})

				nu := nar.URL{Hash: testdata.Nar2.NarHash, Compression: nar.CompressionTypeXz}
				size, reader, err := c.GetNar(newContext(), nu)
				require.NoError(t, err)

				// Continusly Get the NAR to ensure it finally makes it to the store
				// otherwise the test below will never pass.
				for size < 0 {
					// discard the contents of the previous reader
					_, err = io.Copy(io.Discard, reader)
					require.NoError(t, err)

					// read the NAR again
					size, reader, err = c.GetNar(newContext(), nu)
					require.NoError(t, err)
				}

				t.Run("nar does exist in storage", func(t *testing.T) {
					assert.FileExists(t, storePath)
				})

				t.Run("DELETE returns no error", func(t *testing.T) {
					url := ts.URL + "/nar/" + testdata.Nar2.NarHash + ".nar.xz"

					r, err := http.NewRequestWithContext(newContext(), http.MethodDelete, url, nil)
					require.NoError(t, err)

					resp, err := ts.Client().Do(r)
					require.NoError(t, err)

					assert.Equal(t, http.StatusNoContent, resp.StatusCode)
				})

				t.Run("narinfo is gone from the store", func(t *testing.T) {
					assert.NoFileExists(t, storePath)
				})
			})
		})
	})

	t.Run("GET requests", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		defer os.RemoveAll(dir) // clean up

		dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
		testhelper.CreateMigrateDatabase(t, dbFile)

		db, err := database.Open("sqlite:"+dbFile, nil)
		require.NoError(t, err)

		localStore, err := local.New(newContext(), dir)
		require.NoError(t, err)

		c, err := newTestCache(newContext(), db, localStore, localStore, localStore)
		require.NoError(t, err)

		c.AddUpstreamCaches(newContext(), uc)
		c.SetRecordAgeIgnoreTouch(0)

		// Wait for upstream caches to become available
		<-c.GetHealthChecker().Trigger()

		s := server.New(c)

		t.Run("narinfo", func(t *testing.T) {
			t.Run("narinfo does not exist upstream", func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, "/doesnotexist.narinfo", nil)
				w := httptest.NewRecorder()

				s.ServeHTTP(w, r)

				assert.Equal(t, http.StatusNotFound, w.Code)
			})

			t.Run("narinfo exists upstream", func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, helper.NarInfoURLPath(testdata.Nar1.NarInfoHash), nil)
				w := httptest.NewRecorder()

				s.ServeHTTP(w, r)

				require.Equal(t, http.StatusOK, w.Code)

				resp := w.Result()
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)

				// NOTE: HasPrefix instead equality because we add our signature to the narInfo.
				assert.True(t, strings.HasPrefix(string(body), testdata.Nar1.NarInfoText))
			})
		})

		t.Run("nar", func(t *testing.T) {
			t.Run("nar does not exist upstream", func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, "/nar/doesnotexist.nar", nil)
				w := httptest.NewRecorder()

				s.ServeHTTP(w, r)

				assert.Equal(t, http.StatusNotFound, w.Code)
			})

			t.Run("nar exists upstream", func(t *testing.T) {
				u, err := url.Parse("http://example.com")
				require.NoError(t, err)

				nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}
				r := httptest.NewRequest(http.MethodGet, nu.JoinURL(u).String(), nil)
				w := httptest.NewRecorder()

				s.ServeHTTP(w, r)

				assert.Equal(t, http.StatusOK, w.Code)

				resp := w.Result()
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)

				if assert.Len(t, testdata.Nar1.NarText, len(string(body))) {
					assert.Equal(t, testdata.Nar1.NarText, string(body))
				}
			})

			t.Run("nar exists upstream with query", func(t *testing.T) {
				u, err := url.Parse("http://example.com")
				require.NoError(t, err)

				q, err := url.ParseQuery("fakesize=123")
				require.NoError(t, err)

				nu := nar.URL{Hash: testdata.Nar2.NarHash, Compression: nar.CompressionTypeXz, Query: q}

				r := httptest.NewRequest(http.MethodGet, nu.JoinURL(u).String(), nil)
				w := httptest.NewRecorder()

				s.ServeHTTP(w, r)

				assert.Equal(t, http.StatusOK, w.Code)

				resp := w.Result()
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)

				if assert.Len(t, string(body), 123) {
					assert.Equal(t, strings.Repeat("a", 123), string(body))
				}
			})
		})
	})

	t.Run("PUT requests", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		defer os.RemoveAll(dir) // clean up

		dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
		testhelper.CreateMigrateDatabase(t, dbFile)

		db, err := database.Open("sqlite:"+dbFile, nil)
		require.NoError(t, err)

		localStore, err := local.New(newContext(), dir)
		require.NoError(t, err)

		c, err := newTestCache(newContext(), db, localStore, localStore, localStore)
		require.NoError(t, err)

		c.AddUpstreamCaches(newContext(), uc)
		c.SetRecordAgeIgnoreTouch(0)

		// Wait for upstream caches to become available
		<-c.GetHealthChecker().Trigger()

		t.Run("PUT is not permitted", func(t *testing.T) {
			s := server.New(c)
			s.SetPutPermitted(false)

			ts := httptest.NewServer(s)
			defer ts.Close()

			t.Run("narInfo", func(t *testing.T) {
				p := ts.URL + "/" + testdata.Nar1.NarInfoHash + ".narinfo"

				r, err := http.NewRequestWithContext(newContext(), http.MethodPut, p, strings.NewReader(testdata.Nar1.NarInfoText))
				require.NoError(t, err)

				resp, err := ts.Client().Do(r)
				require.NoError(t, err)

				assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
			})

			t.Run("nar", func(t *testing.T) {
				t.Run("without compression", func(t *testing.T) {
					p := ts.URL + "/nar/" + testdata.Nar1.NarInfoHash + ".nar"

					r, err := http.NewRequestWithContext(newContext(), http.MethodPut, p, strings.NewReader(testdata.Nar1.NarText))
					require.NoError(t, err)

					resp, err := ts.Client().Do(r)
					require.NoError(t, err)

					assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
				})

				t.Run("with compression", func(t *testing.T) {
					p := ts.URL + "/nar/" + testdata.Nar1.NarInfoHash + ".nar.xz"

					r, err := http.NewRequestWithContext(newContext(), http.MethodPut, p, strings.NewReader(testdata.Nar1.NarText))
					require.NoError(t, err)

					resp, err := ts.Client().Do(r)
					require.NoError(t, err)

					assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
				})
			})
		})

		t.Run("PUT is permitted", func(t *testing.T) {
			s := server.New(c)
			s.SetPutPermitted(true)

			ts := httptest.NewServer(s)
			defer ts.Close()

			t.Run("narInfo", func(t *testing.T) {
				storePath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)

				t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
					assert.NoFileExists(t, storePath)
				})

				t.Run("putNarInfo does not return an error", func(t *testing.T) {
					p := ts.URL + "/" + testdata.Nar1.NarInfoHash + ".narinfo"

					r, err := http.NewRequestWithContext(newContext(), http.MethodPut, p, strings.NewReader(testdata.Nar1.NarInfoText))
					require.NoError(t, err)

					resp, err := ts.Client().Do(r)
					require.NoError(t, err)

					assert.Equal(t, http.StatusNoContent, resp.StatusCode)
				})

				t.Run("narinfo does exist in storage", func(t *testing.T) {
					_, err := os.Stat(storePath)
					require.NoError(t, err)
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
			})

			storePath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)

			t.Run("nar does not exist in storage yet", func(t *testing.T) {
				assert.NoFileExists(t, storePath)
			})

			t.Run("putNar does not return an error", func(t *testing.T) {
				p := ts.URL + "/nar/" + testdata.Nar1.NarHash + ".nar.xz"

				r, err := http.NewRequestWithContext(newContext(), http.MethodPut, p, strings.NewReader(testdata.Nar1.NarText))
				require.NoError(t, err)

				resp, err := ts.Client().Do(r)
				require.NoError(t, err)

				assert.Equal(t, http.StatusNoContent, resp.StatusCode)
			})

			t.Run("nar does exist in storage", func(t *testing.T) {
				f, err := os.Open(storePath)
				require.NoError(t, err)

				bs, err := io.ReadAll(f)
				require.NoError(t, err)

				assert.Equal(t, testdata.Nar1.NarText, string(bs))
			})
		})
	})
}

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}
