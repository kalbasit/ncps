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
	//nolint:staticcheck // using deprecated ConfigStore interface for testing migration
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

// TestGetNar_NixServeUpstream_PrefixedNarURL tests that ncps correctly proxies NARs when
// the nix-serve upstream uses prefixed URLs (narinfo-hash-nar-hash), and the test
// server only serves the NAR at the prefixed path (not the normalized path).
// This reproduces the race condition bug where GetNar tries to fetch from upstream
// using the normalized URL (which the upstream doesn't recognize).
func TestGetNar_NixServeUpstream_PrefixedNarURL(t *testing.T) {
	t.Parallel()

	hts := testdata.NewTestServer(t, 40)
	t.Cleanup(hts.Close)

	// Create a test entry with a prefixed NAR URL (nix-serve style)
	// Based on Nar7 but with a prefixed URL
	prefixedNarInfoHash := testdata.Nar7.NarInfoHash + "-" + testdata.Nar7.NarHash
	parsedNarInfo, err := narinfo.Parse(strings.NewReader(testdata.Nar7.NarInfoText))
	require.NoError(t, err)

	parsedNarInfo.URL = "nar/" + prefixedNarInfoHash + ".nar"
	prefixedNarInfoText := parsedNarInfo.String()

	prefixedEntry := testdata.Entry{
		NarInfoHash:    testdata.Nar7.NarInfoHash,
		NarInfoPath:    testdata.Nar7.NarInfoPath,
		NarInfoText:    prefixedNarInfoText,
		NarHash:        testdata.Nar7.NarHash,
		NarCompression: testdata.Nar7.NarCompression,
		NarPath:        testdata.Nar7.NarPath,
		NarText:        testdata.Nar7.NarText,
		NarInfoNarHash: prefixedNarInfoHash,
	}
	hts.AddEntry(prefixedEntry)

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, hts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	dir, err := os.MkdirTemp("", "cache-nix-serve-prefixed-")
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

	<-c.GetHealthChecker().Trigger()

	s := server.New(c)

	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)

	// Fetch narinfo first to trigger background NAR pull.
	// This uses the prefixed URL from the upstream.
	narInfoReq, err := http.NewRequestWithContext(
		newContext(),
		http.MethodGet,
		ts.URL+"/"+prefixedEntry.NarInfoHash+".narinfo",
		nil,
	)
	require.NoError(t, err)

	//nolint:bodyclose // closed below
	narInfoResp, err := ts.Client().Do(narInfoReq)
	require.NoError(t, err)

	defer narInfoResp.Body.Close()

	require.Equal(t, http.StatusOK, narInfoResp.StatusCode)

	narInfoBody, err := io.ReadAll(narInfoResp.Body)
	require.NoError(t, err)

	ni, err := narinfo.Parse(strings.NewReader(string(narInfoBody)))
	require.NoError(t, err)

	// Parse the URL to get the normalized NAR hash
	narURL, err := nar.ParseURL(ni.URL)
	require.NoError(t, err)

	normalizedNarURL, err := narURL.Normalize()
	require.NoError(t, err)

	// Request the NAR using the normalized URL (as a client would after reading narinfo).
	// The test server only serves the NAR at the prefixed path (prefixedEntry.NarInfoNarHash),
	// not at the normalized path.
	// Without the fix, this would fail because GetNar would try to fetch from upstream
	// using the normalized URL when the NAR isn't in the store yet.
	narReqURL := ts.URL + "/nar/" + normalizedNarURL.Hash + ".nar"

	narReq, err := http.NewRequestWithContext(newContext(), http.MethodGet, narReqURL, nil)
	require.NoError(t, err)

	// Use DisableCompression to prevent Go HTTP client adding Accept-Encoding: gzip.
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}

	//nolint:bodyclose // closed below
	narResp, err := client.Do(narReq)
	require.NoError(t, err)

	defer narResp.Body.Close()

	require.Equal(t, http.StatusOK, narResp.StatusCode)

	narBody, err := io.ReadAll(narResp.Body)
	require.NoError(t, err)

	assert.Equal(t, prefixedEntry.NarText, string(narBody))
}
