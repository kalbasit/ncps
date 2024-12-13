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

	"github.com/inconshreveable/log15/v3"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/server"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

//nolint:paralleltest
func TestServeHTTP(t *testing.T) {
	hts := testdata.HTTPTestServer(t, 40)
	defer hts.Close()

	uc, err := upstream.New(logger, testhelper.MustParseURL(t, hts.URL), testdata.PublicKeys())
	require.NoError(t, err)

	t.Run("DELETE requests", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
		testhelper.CreateMigrateDatabase(t, dbFile)

		c, err := cache.New(logger, "cache.example.com", dir)
		require.NoError(t, err)

		c.AddUpstreamCaches(uc)
		c.SetRecordAgeIgnoreTouch(0)

		t.Run("DELETE is not permitted", func(t *testing.T) {
			s := server.New(logger, c)
			s.SetDeletePermitted(false)

			ts := httptest.NewServer(s)
			defer ts.Close()

			t.Run("narInfo", func(t *testing.T) {
				url := ts.URL + "/" + testdata.Nar1.NarInfoHash + ".narinfo"

				r, err := http.NewRequestWithContext(context.Background(), "DELETE", url, nil)
				require.NoError(t, err)

				resp, err := ts.Client().Do(r)
				require.NoError(t, err)

				assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
			})

			t.Run("nar", func(t *testing.T) {
				url := ts.URL + "/nar/" + testdata.Nar1.NarHash + ".nar.xz"

				r, err := http.NewRequestWithContext(context.Background(), "DELETE", url, nil)
				require.NoError(t, err)

				resp, err := ts.Client().Do(r)
				require.NoError(t, err)

				assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
			})
		})

		t.Run("DELETE is permitted", func(t *testing.T) {
			s := server.New(logger, c)
			s.SetDeletePermitted(true)

			ts := httptest.NewServer(s)
			defer ts.Close()

			t.Run("narInfo", func(t *testing.T) {
				storePath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)

				t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
					assert.NoFileExists(t, storePath)
				})

				_, err := c.GetNarInfo(context.Background(), testdata.Nar1.NarInfoHash)
				require.NoError(t, err)

				t.Run("narinfo does exist in storage", func(t *testing.T) {
					assert.FileExists(t, storePath)
				})

				t.Run("DELETE returns no error", func(t *testing.T) {
					url := ts.URL + "/" + testdata.Nar1.NarInfoHash + ".narinfo"

					r, err := http.NewRequestWithContext(context.Background(), "DELETE", url, nil)
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

				nu := nar.URL{Hash: testdata.Nar2.NarHash, Compression: "xz"}
				_, _, err := c.GetNar(context.Background(), nu)
				require.NoError(t, err)

				t.Run("nar does exist in storage", func(t *testing.T) {
					assert.FileExists(t, storePath)
				})

				t.Run("DELETE returns no error", func(t *testing.T) {
					url := ts.URL + "/nar/" + testdata.Nar2.NarHash + ".nar.xz"

					r, err := http.NewRequestWithContext(context.Background(), "DELETE", url, nil)
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

		c, err := cache.New(logger, "cache.example.com", dir)
		require.NoError(t, err)

		c.AddUpstreamCaches(uc)
		c.SetRecordAgeIgnoreTouch(0)

		s := server.New(logger, c)

		t.Run("narinfo", func(t *testing.T) {
			t.Run("narinfo does not exist upstream", func(t *testing.T) {
				r := httptest.NewRequest("GET", "/doesnotexist.narinfo", nil)
				w := httptest.NewRecorder()

				s.ServeHTTP(w, r)

				assert.Equal(t, http.StatusNotFound, w.Code)
			})

			t.Run("narinfo exists upstream", func(t *testing.T) {
				r := httptest.NewRequest("GET", helper.NarInfoURLPath(testdata.Nar1.NarInfoHash), nil)
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
				r := httptest.NewRequest("GET", "/nar/doesnotexist.nar", nil)
				w := httptest.NewRecorder()

				s.ServeHTTP(w, r)

				assert.Equal(t, http.StatusNotFound, w.Code)
			})

			t.Run("nar exists upstream", func(t *testing.T) {
				u, err := url.Parse("http://example.com")
				require.NoError(t, err)

				nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: "xz"}
				r := httptest.NewRequest("GET", nu.JoinURL(u).String(), nil)
				w := httptest.NewRecorder()

				s.ServeHTTP(w, r)

				assert.Equal(t, http.StatusOK, w.Code)

				resp := w.Result()
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)

				if assert.Equal(t, len(testdata.Nar1.NarText), len(string(body))) {
					assert.Equal(t, testdata.Nar1.NarText, string(body))
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

		c, err := cache.New(logger, "cache.example.com", dir)
		require.NoError(t, err)

		c.AddUpstreamCaches(uc)
		c.SetRecordAgeIgnoreTouch(0)

		t.Run("PUT is not permitted", func(t *testing.T) {
			s := server.New(logger, c)
			s.SetPutPermitted(false)

			ts := httptest.NewServer(s)
			defer ts.Close()

			t.Run("narInfo", func(t *testing.T) {
				p := ts.URL + "/" + testdata.Nar1.NarInfoHash + ".narinfo"

				r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(testdata.Nar1.NarInfoText))
				require.NoError(t, err)

				resp, err := ts.Client().Do(r)
				require.NoError(t, err)

				assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
			})

			t.Run("nar", func(t *testing.T) {
				t.Run("without compression", func(t *testing.T) {
					p := ts.URL + "/nar/" + testdata.Nar1.NarInfoHash + ".nar"

					r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(testdata.Nar1.NarText))
					require.NoError(t, err)

					resp, err := ts.Client().Do(r)
					require.NoError(t, err)

					assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
				})

				t.Run("with compression", func(t *testing.T) {
					p := ts.URL + "/nar/" + testdata.Nar1.NarInfoHash + ".nar.xz"

					r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(testdata.Nar1.NarText))
					require.NoError(t, err)

					resp, err := ts.Client().Do(r)
					require.NoError(t, err)

					assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
				})
			})
		})

		t.Run("PUT is permitted", func(t *testing.T) {
			s := server.New(logger, c)
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

					r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(testdata.Nar1.NarInfoText))
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
						if sig.Name == "cache.example.com" {
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

				r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(testdata.Nar1.NarText))
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
