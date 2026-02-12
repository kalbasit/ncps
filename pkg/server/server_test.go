package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/server"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

const (
	cacheName           = "cache.example.com"
	uncompressedNarData = "uncompressed nar data"
)

func newTestCache(
	ctx context.Context,
	db database.Querier,
	//nolint:staticcheck // using deprecated ConfigStore interface for testing migration
	configStore storage.ConfigStore,
	narInfoStore storage.NarInfoStore,
	narStore storage.NarStore,
) (*cache.Cache, error) {
	downloadLocker := locklocal.NewLocker()
	cacheLocker := locklocal.NewRWLocker()

	return cache.New(ctx, cacheName, db, configStore, narInfoStore, narStore, "",
		downloadLocker, cacheLocker, 5*time.Minute, 30*time.Second, 30*time.Minute)
}

//nolint:paralleltest
func TestServeHTTP(t *testing.T) {
	hts := testdata.NewTestServer(t, 40)
	t.Cleanup(hts.Close)

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, hts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	t.Run("GET /pubkey", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		t.Cleanup(func() { os.RemoveAll(dir) })

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
		t.Cleanup(ts.Close)

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

		t.Cleanup(func() { os.RemoveAll(dir) })

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
				t.Run("narinfo does not exist in the database yet", func(t *testing.T) {
					var count int

					err := db.DB().QueryRowContext(newContext(),
						"SELECT COUNT(*) FROM narinfos WHERE hash = ?",
						testdata.Nar1.NarInfoHash).Scan(&count)
					require.NoError(t, err)
					assert.Equal(t, 0, count)
				})

				_, err := c.GetNarInfo(newContext(), testdata.Nar1.NarInfoHash)
				require.NoError(t, err)

				t.Run("narinfo does exist in the database", func(t *testing.T) {
					var count int

					err := db.DB().QueryRowContext(newContext(),
						"SELECT COUNT(*) FROM narinfos WHERE hash = ?",
						testdata.Nar1.NarInfoHash).Scan(&count)
					require.NoError(t, err)
					assert.Equal(t, 1, count, "narinfo should exist in database")
				})

				t.Run("DELETE returns no error", func(t *testing.T) {
					url := ts.URL + "/" + testdata.Nar1.NarInfoHash + ".narinfo"

					r, err := http.NewRequestWithContext(newContext(), http.MethodDelete, url, nil)
					require.NoError(t, err)

					resp, err := ts.Client().Do(r)
					require.NoError(t, err)

					assert.Equal(t, http.StatusNoContent, resp.StatusCode)
				})

				t.Run("narinfo is gone from the database", func(t *testing.T) {
					var count int

					err := db.DB().QueryRowContext(newContext(),
						"SELECT COUNT(*) FROM narinfos WHERE hash = ?",
						testdata.Nar1.NarInfoHash).Scan(&count)
					require.NoError(t, err)
					assert.Equal(t, 0, count, "narinfo should be gone from database")
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

		t.Cleanup(func() { os.RemoveAll(dir) })

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
			t.Run("returns normalized URL in narinfo response", func(t *testing.T) {
				// This test verifies that when a narinfo is returned, the URL
				// inside the narinfo is normalized (i.e., narinfo hash prefix is removed)
				//
				// The issue: upstream caches may return narinfo with URLs like:
				// URL: nar/narinfo-hash-actual-hash.nar.zst
				// But the server only serves NARs with the actual hash:
				// /nar/actual-hash.nar.zst
				//
				// The server should normalize the URL before returning it.

				// Use Nar8 for this test to avoid polluting the shared localStore with modified narinfo
				narInfoHash := testdata.Nar8.NarInfoHash
				actualNarHash := testdata.Nar8.NarHash

				// Create a narinfo string with a prefixed URL (simulating upstream behavior)
				prefixedNarHash := narInfoHash + "-" + actualNarHash
				narInfoWithPrefixedURL := `StorePath: /nix/store/test-path
URL: nar/` + prefixedNarHash + `.nar.xz
Compression: xz
FileHash: sha256:` + actualNarHash + `
FileSize: 50160
NarHash: sha256:` + actualNarHash + `
NarSize: 226552
References: test-path
Deriver: test.drv
`

				// Parse the narinfo string into a NarInfo object
				parsedNarInfo, err := narinfo.Parse(strings.NewReader(narInfoWithPrefixedURL))
				require.NoError(t, err)

				// Create a separate local store for this test to avoid polluting the shared store
				testDir, err := os.MkdirTemp("", "narinfo-normalize-test-")
				require.NoError(t, err)
				t.Cleanup(func() { os.RemoveAll(testDir) })

				testLocalStore, err := local.New(newContext(), testDir)
				require.NoError(t, err)

				// Store this narinfo in the test store
				err = testLocalStore.PutNarInfo(newContext(), narInfoHash, parsedNarInfo)
				require.NoError(t, err)

				// Also store the actual NAR so we can verify it can be fetched
				narURL := nar.URL{
					Hash:        actualNarHash,
					Compression: nar.CompressionTypeXz,
				}
				narSize, err := testLocalStore.PutNar(newContext(), narURL, strings.NewReader(testdata.Nar8.NarText))
				require.NoError(t, err)
				require.Positive(t, narSize)

				// Create a cache using the test store
				testDBFile := filepath.Join(testDir, "db.sqlite")
				testhelper.CreateMigrateDatabase(t, testDBFile)
				testDB, err := database.Open("sqlite:"+testDBFile, nil)
				require.NoError(t, err)

				testCache, err := newTestCache(newContext(), testDB, testLocalStore, testLocalStore, testLocalStore)
				require.NoError(t, err)

				// Create a server using the test cache
				testServer := server.New(testCache)

				// Request the narinfo via HTTP server
				r := httptest.NewRequest(http.MethodGet, "/"+narInfoHash+".narinfo", nil)
				w := httptest.NewRecorder()
				testServer.ServeHTTP(w, r)

				require.Equal(t, http.StatusOK, w.Code, "should return 200 OK for valid narinfo request")

				// Parse the response to get the narinfo
				body := w.Body.String()
				require.NotEmpty(t, body)

				// Parse the narinfo to extract the URL
				respNarInfo, err := narinfo.Parse(strings.NewReader(body))
				require.NoError(t, err)
				require.NotNil(t, respNarInfo)

				// The URL should be normalized (no narinfo hash prefix)
				url := respNarInfo.URL
				require.NotEmpty(t, url)

				// Parse the URL to verify its structure
				parsedNarURL, err := nar.ParseURL(url)
				require.NoError(t, err, "returned URL should be parseable: %s", url)

				// The hash in the URL should be the actual NAR hash, not the prefixed version
				assert.Equal(t, actualNarHash, parsedNarURL.Hash,
					"URL hash should be normalized (prefix should be stripped), got: %s", parsedNarURL.Hash)

				// Verify we can actually fetch the NAR using the URL from narinfo
				// This is the critical test - the server should serve the NAR using the normalized URL
				ts := httptest.NewServer(testServer)
				t.Cleanup(ts.Close)

				narURL2 := nar.URL{
					Hash:        parsedNarURL.Hash,
					Compression: parsedNarURL.Compression,
				}
				httpURL := ts.URL + "/" + narURL2.String()

				req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, httpURL, nil)
				require.NoError(t, err)

				resp, err := ts.Client().Do(req)
				require.NoError(t, err)

				defer resp.Body.Close()

				// This should NOT be 404 - it should return the NAR successfully
				assert.Equal(t, http.StatusOK, resp.StatusCode,
					"should be able to fetch NAR with normalized URL from narinfo at %s", httpURL)
			})

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

				ni, err := narinfo.Parse(strings.NewReader(string(body)))
				require.NoError(t, err)

				assert.Contains(t, ni.StorePath, testdata.Nar1.NarInfoHash)

				var found bool

				for _, sig := range ni.Signatures {
					if sig.Name == cacheName {
						found = true

						break
					}
				}

				assert.True(t, found, "cache signature should be present")
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

		t.Cleanup(func() { os.RemoveAll(dir) })

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

				t.Run("narinfo does exist in the database", func(t *testing.T) {
					var count int

					err := db.DB().QueryRowContext(newContext(),
						"SELECT COUNT(*) FROM narinfos WHERE hash = ?",
						testdata.Nar1.NarInfoHash).Scan(&count)
					require.NoError(t, err)
					assert.Equal(t, 1, count, "narinfo should exist in database")
				})

				t.Run("it should be signed by our server", func(t *testing.T) {
					var sigsStr []string

					rows, err := db.DB().QueryContext(newContext(),
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

					assert.GreaterOrEqual(t, len(sigsStr), 1, "narinfo should have at least 1 signature")

					var parsedSigs []signature.Signature

					for _, sigStr := range sigsStr {
						sig, err := signature.ParseSignature(sigStr)
						require.NoError(t, err)

						parsedSigs = append(parsedSigs, sig)
					}

					ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
					require.NoError(t, err)

					var found bool

					for _, parsedSig := range parsedSigs {
						if parsedSig.Name == cacheName {
							found = true

							break
						}
					}

					assert.True(t, found, "cache signature should be present")
					assert.True(t, signature.VerifyFirst(ni.Fingerprint(), parsedSigs, []signature.PublicKey{c.PublicKey()}),
						"cache signature should be valid")
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

func TestGetNar_HeadOptimization(t *testing.T) {
	t.Parallel()

	// create a temporary directory for the cache
	dir, err := os.MkdirTemp("", "cache-path-opt-")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), db, localStore, localStore, localStore)
	require.NoError(t, err)

	// create the server
	s := server.New(c)
	s.SetPutPermitted(true)

	// create the test server
	ts := httptest.NewServer(s)
	defer ts.Close()

	// 1. Put a NarInfo into the cache. This will create a NarFile record in the database.
	putURL := ts.URL + "/" + testdata.Nar1.NarInfoHash + ".narinfo"
	req, err := http.NewRequestWithContext(
		newContext(),
		http.MethodPut,
		putURL,
		strings.NewReader(testdata.Nar1.NarInfoText),
	)
	require.NoError(t, err)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// 2. Verify the NAR itself is NOT in the store
	storePath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
	assert.NoFileExists(t, storePath)

	// 3. Make a HEAD request for the NAR. It should return 204 No Content and the correct size.
	narURL := ts.URL + "/nar/" + testdata.Nar1.NarHash + ".nar.xz"
	req, err = http.NewRequestWithContext(newContext(), http.MethodHead, narURL, nil)
	require.NoError(t, err)
	resp, err = ts.Client().Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, strconv.Itoa(len(testdata.Nar1.NarText)), resp.Header.Get("Content-Length"))
	resp.Body.Close()
}

func TestGetNarInfo_Head(t *testing.T) {
	t.Parallel()

	// create a temporary directory for the cache
	dir, err := os.MkdirTemp("", "cache-path-ni-head-")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), db, localStore, localStore, localStore)
	require.NoError(t, err)

	// create the server
	s := server.New(c)
	s.SetPutPermitted(true)

	// 1. Put a Nar into the cache.
	req := httptest.NewRequest(
		http.MethodPut,
		"/nar/"+testdata.Nar1.NarHash+".nar.xz",
		strings.NewReader(testdata.Nar1.NarText),
	)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// 2. Put a NarInfo into the cache.
	req = httptest.NewRequest(
		http.MethodPut,
		"/"+testdata.Nar1.NarInfoHash+".narinfo",
		strings.NewReader(testdata.Nar1.NarInfoText),
	)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify it's in the database
	var count int

	err = db.DB().
		QueryRowContext(newContext(), "SELECT COUNT(*) FROM narinfos WHERE hash = ?", testdata.Nar1.NarInfoHash).
		Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "narinfo should be in the database after PUT")

	// 3. Make a HEAD request for the NarInfo.
	req = httptest.NewRequest(http.MethodHead, "/"+testdata.Nar1.NarInfoHash+".narinfo", nil)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)

	// Verify it returns 200 OK and Content-Length
	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Header().Get("Content-Length"))
}

func TestGetNar_HeadFallback(t *testing.T) {
	t.Parallel()

	// create a temporary directory for the cache
	dir, err := os.MkdirTemp("", "cache-path-nar-fallback-")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), db, localStore, localStore, localStore)
	require.NoError(t, err)

	// create the server
	s := server.New(c)
	s.SetPutPermitted(true)

	// create the test server
	ts := httptest.NewServer(s)
	defer ts.Close()

	// 1. Put a Nar into the cache directly (skipping NarInfo to avoid optimization)
	// Wait, to put a Nar we usually need a NarInfo if we use the cache API.
	// We can just use the server's PUT /nar
	narURL := ts.URL + "/nar/" + testdata.Nar1.NarHash + ".nar.xz"
	req, err := http.NewRequestWithContext(
		newContext(),
		http.MethodPut,
		narURL,
		strings.NewReader(testdata.Nar1.NarText),
	)
	require.NoError(t, err)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// 2. Make a HEAD request for the Nar.
	// Since there is no NarInfo, the optimization will fail and it will fall back to GetNar.
	req, err = http.NewRequestWithContext(newContext(), http.MethodHead, narURL, nil)
	require.NoError(t, err)
	resp, err = ts.Client().Do(req)
	require.NoError(t, err)

	// Verify it returns 200 OK and Content-Length
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, strconv.Itoa(len(testdata.Nar1.NarText)), resp.Header.Get("Content-Length"))
	resp.Body.Close()
}

func TestGetNar_ZstdCompression(t *testing.T) {
	t.Parallel()

	// create a temporary directory for the cache
	dir, err := os.MkdirTemp("", "cache-path-zstd-")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), db, localStore, localStore, localStore)
	require.NoError(t, err)

	// create the server
	s := server.New(c)
	s.SetPutPermitted(true)

	// create the test server
	ts := httptest.NewServer(s)
	defer ts.Close()

	// 1. Put an uncompressed Nar into the cache.
	narData := strings.Repeat("uncompressed nar data ", 1000)
	narHash := "0000000000000000000000000000000000000000000000000001" // dummy 52-char hash
	narURL := ts.URL + "/nar/" + narHash + ".nar"

	req, err := http.NewRequestWithContext(
		newContext(),
		http.MethodPut,
		narURL,
		strings.NewReader(narData),
	)
	require.NoError(t, err)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// 2. Request the NAR with Accept-Encoding: zstd
	req = httptest.NewRequest(http.MethodGet, "/nar/"+narHash+".nar", nil)
	req.Header.Set("Accept-Encoding", "zstd")

	w := httptest.NewRecorder()

	s.ServeHTTP(w, req)

	resp = w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "zstd", resp.Header.Get("Content-Encoding"))
	assert.Equal(t, "application/x-nix-nar", resp.Header.Get("Content-Type"))
	assert.Empty(t, resp.Header.Get("Content-Length"))

	// 3. Decompress the body and verify content
	dec, err := zstd.NewReader(resp.Body)
	require.NoError(t, err)

	defer dec.Close()

	decompressed, err := io.ReadAll(dec)
	require.NoError(t, err)
	assert.Equal(t, narData, string(decompressed))
}

func TestGetNar_NoZstdCompression(t *testing.T) {
	t.Parallel()

	// create a temporary directory for the cache
	dir, err := os.MkdirTemp("", "cache-path-no-zstd-")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), db, localStore, localStore, localStore)
	require.NoError(t, err)

	// create the server
	s := server.New(c)
	s.SetPutPermitted(true)

	// create the test server
	ts := httptest.NewServer(s)
	defer ts.Close()

	// 1. Put an uncompressed Nar into the cache.
	narData := uncompressedNarData
	narHash := "0000000000000000000000000000000000000000000000000002" // dummy 52-char hash
	narURL := ts.URL + "/nar/" + narHash + ".nar"

	req, err := http.NewRequestWithContext(
		newContext(),
		http.MethodPut,
		narURL,
		strings.NewReader(narData),
	)
	require.NoError(t, err)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// 2. Request the NAR WITHOUT Accept-Encoding: zstd
	req, err = http.NewRequestWithContext(newContext(), http.MethodGet, narURL, nil)
	require.NoError(t, err)

	resp, err = ts.Client().Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEqual(t, "zstd", resp.Header.Get("Content-Encoding"))
	assert.Equal(t, "application/x-nix-nar", resp.Header.Get("Content-Type"))
	assert.Equal(t, strconv.Itoa(len(narData)), resp.Header.Get("Content-Length"))

	// 3. Verify content
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, narData, string(body))
}

func TestGetNar_ZstdCompression_Head(t *testing.T) {
	t.Parallel()

	// create a temporary directory for the cache
	dir, err := os.MkdirTemp("", "cache-path-zstd-head-")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), db, localStore, localStore, localStore)
	require.NoError(t, err)

	// create the server
	s := server.New(c)
	s.SetPutPermitted(true)

	// create the test server
	ts := httptest.NewServer(s)
	defer ts.Close()

	// 1. Put an uncompressed Nar into the cache.
	narData := uncompressedNarData
	narHash := "0000000000000000000000000000000000000000000000000003" // dummy 52-char hash
	narURL := ts.URL + "/nar/" + narHash + ".nar"

	req, err := http.NewRequestWithContext(
		newContext(),
		http.MethodPut,
		narURL,
		strings.NewReader(narData),
	)
	require.NoError(t, err)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// 2. Request the NAR with HEAD and Accept-Encoding: zstd
	req = httptest.NewRequest(http.MethodHead, "/nar/"+narHash+".nar", nil)
	req.Header.Set("Accept-Encoding", "zstd")

	w := httptest.NewRecorder()

	s.ServeHTTP(w, req)

	resp = w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// Currently the implementation only enables zstd if withBody is true.
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	assert.Equal(t, strconv.Itoa(len(narData)), resp.Header.Get("Content-Length"))
}

type responseWriterRecorder struct {
	headers http.Header
	status  int
	events  []string
}

func (r *responseWriterRecorder) Header() http.Header {
	return r.headers
}

func (r *responseWriterRecorder) WriteHeader(status int) {
	// Record current state of Content-Encoding before WriteHeader
	ce := r.headers.Get("Content-Encoding")
	if ce != "" {
		r.events = append(r.events, "Header:Content-Encoding:"+ce)
	}

	r.status = status

	r.events = append(r.events, "WriteHeader")
}

func (r *responseWriterRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}

	r.events = append(r.events, "Write")

	return len(b), nil
}

func TestGetNar_HeaderSettingSequence(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "cache-path-header-seq-")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)
	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), db, localStore, localStore, localStore)
	require.NoError(t, err)

	s := server.New(c)
	s.SetPutPermitted(true)

	// Put an uncompressed Nar into the cache.
	narData := uncompressedNarData
	narHash := "0000000000000000000000000000000000000000000000000004"
	narURL := "/nar/" + narHash + ".nar"

	req := httptest.NewRequest(http.MethodPut, narURL, strings.NewReader(narData))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	t.Run("Content-Encoding header is set AFTER determining compression is used", func(t *testing.T) {
		req = httptest.NewRequest(http.MethodGet, narURL, nil)
		req.Header.Set("Accept-Encoding", "zstd")

		recorder := &responseWriterRecorder{
			headers: make(http.Header),
		}

		s.ServeHTTP(recorder, req)

		// In the current BUGGY code, the events are:
		// ["Header:Content-Encoding:zstd", "WriteHeader", "Write"]
		// This is because h.Set("Content-Encoding", "zstd") is called at line 562
		// and w.WriteHeader(http.StatusOK) is called at line 595.

		assert.Equal(t, []string{"Header:Content-Encoding:zstd", "WriteHeader", "Write"}, recorder.events)
	})
}
