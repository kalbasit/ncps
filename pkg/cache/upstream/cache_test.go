package upstream_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

func TestNew(t *testing.T) {
	t.Parallel()

	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	//nolint:paralleltest
	t.Run("hostname must be valid with no scheme or path", func(t *testing.T) {
		//nolint:paralleltest
		t.Run("hostname must not be empty", func(t *testing.T) {
			_, err := upstream.New(newContext(), nil, nil, nil)
			assert.ErrorIs(t, err, upstream.ErrURLRequired)
		})

		//nolint:paralleltest
		t.Run("hostname must not contain scheme", func(t *testing.T) {
			_, err := upstream.New(newContext(), testhelper.MustParseURL(t, "cache.nixos.org"), nil, nil)
			assert.ErrorIs(t, err, upstream.ErrURLMustContainScheme)
		})

		t.Run("valid url with no path must not return no error", func(t *testing.T) {
			_, err := upstream.New(newContext(),
				testhelper.MustParseURL(t, ts.URL), nil, nil)

			assert.NoError(t, err)
		})

		t.Run("valid url with only / must not return no error", func(t *testing.T) {
			_, err := upstream.New(newContext(),
				testhelper.MustParseURL(t, ts.URL), nil, nil)

			assert.NoError(t, err)
		})
	})

	//nolint:paralleltest
	t.Run("public keys", func(t *testing.T) {
		//nolint:paralleltest
		t.Run("invalid public keys", func(t *testing.T) {
			_, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), []string{"invalid"}, nil)
			assert.True(t, strings.HasPrefix(err.Error(), "error parsing the public key: public key is corrupt:"))
		})

		//nolint:paralleltest
		t.Run("valid public keys", func(t *testing.T) {
			_, err := upstream.New(
				newContext(),
				testhelper.MustParseURL(t, ts.URL),
				testdata.PublicKeys(),
				nil,
			)
			assert.NoError(t, err)
		})
	})

	//nolint:paralleltest
	t.Run("priority parsed", func(t *testing.T) {
		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL),
			testdata.PublicKeys(),
			nil,
		)
		require.NoError(t, err)

		assert.EqualValues(t, 40, c.GetPriority())
	})

	//nolint:paralleltest
	t.Run("priority parsed from URL", func(t *testing.T) {
		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL+"?priority=42"),
			testdata.PublicKeys(),
			nil,
		)
		require.NoError(t, err)

		assert.EqualValues(t, 42, c.GetPriority())
	})

	//nolint:paralleltest
	t.Run("priority in URL is zero", func(t *testing.T) {
		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL+"?priority=0"),
			testdata.PublicKeys(),
			nil,
		)
		require.NoError(t, err)

		assert.EqualValues(t, 40, c.GetPriority())
	})

	//nolint:paralleltest
	t.Run("priority in URL is invalid", func(t *testing.T) {
		_, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL+"?priority=-1"),
			testdata.PublicKeys(),
			nil,
		)
		assert.ErrorContains(t, err, "error parsing the priority from the URL")
	})
}

func TestGetNarInfo(t *testing.T) {
	t.Parallel()

	testFn := func(withKeys bool) func(*testing.T) {
		return func(t *testing.T) {
			t.Parallel()

			var (
				c   *upstream.Cache
				err error
			)

			ts := testdata.NewTestServer(t, 40)
			defer ts.Close()

			if withKeys {
				c, err = upstream.New(
					newContext(),
					testhelper.MustParseURL(t, ts.URL),
					testdata.PublicKeys(),
					nil,
				)
			} else {
				c, err = upstream.New(
					newContext(),
					testhelper.MustParseURL(t, ts.URL),
					nil,
					nil,
				)
			}

			require.NoError(t, err)

			t.Run("hash not found", func(t *testing.T) {
				_, err := c.GetNarInfo(context.Background(), "abc123")
				assert.ErrorIs(t, err, upstream.ErrNotFound)
			})

			t.Run("hash is found", func(t *testing.T) {
				ni, err := c.GetNarInfo(context.Background(), testdata.Nar1.NarInfoHash)
				require.NoError(t, err)

				assert.Equal(t, "/nix/store/n5glp21rsz314qssw9fbvfswgy3kc68f-hello-2.12.1", ni.StorePath)
			})

			t.Run("check has failed", func(t *testing.T) {
				idx := ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
					for _, entry := range testdata.Entries {
						if r.URL.Path == "/broken-"+entry.NarInfoHash+".narinfo" {
							// mutate the inside
							b := entry.NarInfoText
							b = strings.ReplaceAll(b, "References:", "References: notfound-path")

							_, err := w.Write([]byte(b))
							if err != nil {
								http.Error(w, err.Error(), http.StatusInternalServerError)
							}

							return true
						}
					}

					return false
				})

				defer ts.RemoveMaybeHandler(idx)

				hash := "broken-" + testdata.Nar1.NarInfoHash

				_, err = c.GetNarInfo(context.Background(), hash)
				assert.ErrorContains(t, err, "error while checking the narInfo: invalid Reference[0]: notfound-path")
			})

			for i, entry := range testdata.Entries {
				t.Run(fmt.Sprintf("check Nar%d does not fail", i+1), func(t *testing.T) {
					hash := entry.NarInfoHash

					_, err = c.GetNarInfo(context.Background(), hash)
					assert.NoError(t, err)
				})
			}
		}
	}

	//nolint:paralleltest
	t.Run("upstream without public keys", testFn(false))

	//nolint:paralleltest
	t.Run("upstream with public keys", testFn(true))

	t.Run("timeout if server takes more than 3 seconds before first byte", func(t *testing.T) {
		t.Parallel()

		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Second)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer slowServer.Close()

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
			testdata.PublicKeys(),
			nil,
		)
		require.NoError(t, err)

		_, err = c.GetNarInfo(context.Background(), "hash")
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestHasNarInfo(t *testing.T) {
	t.Parallel()

	t.Run("narinfo does not exist", func(t *testing.T) {
		t.Parallel()

		ts := testdata.NewTestServer(t, 40)
		defer ts.Close()

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL),
			testdata.PublicKeys(),
			nil,
		)
		require.NoError(t, err)

		exists, err := c.HasNarInfo(context.Background(), "abc123")
		require.NoError(t, err)

		assert.False(t, exists)
	})

	for i, narEntry := range testdata.Entries {
		t.Run(fmt.Sprintf("Nar%d should exist", i+1), func(t *testing.T) {
			t.Parallel()

			ts := testdata.NewTestServer(t, 40)
			defer ts.Close()

			c, err := upstream.New(
				newContext(),
				testhelper.MustParseURL(t, ts.URL),
				testdata.PublicKeys(),
				nil,
			)
			require.NoError(t, err)

			exists, err := c.HasNarInfo(context.Background(), narEntry.NarInfoHash)
			require.NoError(t, err)

			assert.True(t, exists)
		})
	}

	t.Run("timeout if server takes more than 3 seconds before first byte", func(t *testing.T) {
		t.Parallel()

		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Second)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer slowServer.Close()

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
			testdata.PublicKeys(),
			nil,
		)
		require.NoError(t, err)

		_, err = c.HasNarInfo(context.Background(), "hash")
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestGetNar(t *testing.T) {
	t.Parallel()

	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	c, err := upstream.New(
		newContext(),
		testhelper.MustParseURL(t, ts.URL),
		testdata.PublicKeys(),
		nil,
	)
	require.NoError(t, err)

	//nolint:paralleltest
	t.Run("not found", func(t *testing.T) {
		nu := nar.URL{Hash: "abc123", Compression: nar.CompressionTypeXz}
		_, err := c.GetNar(context.Background(), nu)
		assert.ErrorIs(t, err, upstream.ErrNotFound)
	})

	//nolint:paralleltest
	t.Run("hash is found", func(t *testing.T) {
		nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}
		resp, err := c.GetNar(context.Background(), nu)
		require.NoError(t, err)

		defer func() {
			//nolint:errcheck
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}()

		assert.Equal(t, "50160", resp.Header.Get("Content-Length"))
	})

	t.Run("timeout if server takes more than 3 seconds before first byte", func(t *testing.T) {
		t.Parallel()

		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Second)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer slowServer.Close()

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
			testdata.PublicKeys(),
			nil,
		)
		require.NoError(t, err)

		nu := nar.URL{Hash: "abc123", Compression: nar.CompressionTypeXz}
		_, err = c.GetNar(context.Background(), nu)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestHasNar(t *testing.T) {
	t.Parallel()

	t.Run("nar does not exist", func(t *testing.T) {
		t.Parallel()

		ts := testdata.NewTestServer(t, 40)
		defer ts.Close()

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL),
			testdata.PublicKeys(),
			nil,
		)
		require.NoError(t, err)

		nu := nar.URL{Hash: "abc123", Compression: nar.CompressionTypeXz}
		exists, err := c.HasNar(context.Background(), nu)
		require.NoError(t, err)

		assert.False(t, exists)
	})

	for i, narEntry := range testdata.Entries {
		t.Run(fmt.Sprintf("Nar%d should exist", i+1), func(t *testing.T) {
			t.Parallel()

			ts := testdata.NewTestServer(t, 40)
			defer ts.Close()

			c, err := upstream.New(
				newContext(),
				testhelper.MustParseURL(t, ts.URL),
				testdata.PublicKeys(),
				nil,
			)
			require.NoError(t, err)

			nu := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}
			exists, err := c.HasNar(context.Background(), nu)
			require.NoError(t, err)

			assert.True(t, exists)
		})
	}

	t.Run("timeout if server takes more than 3 seconds before first byte", func(t *testing.T) {
		t.Parallel()

		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Second)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer slowServer.Close()

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
			testdata.PublicKeys(),
			nil,
		)
		require.NoError(t, err)

		nu := nar.URL{Hash: "abc123", Compression: nar.CompressionTypeXz}
		_, err = c.HasNar(context.Background(), nu)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestGetNarCanMutate(t *testing.T) {
	t.Parallel()

	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	c, err := upstream.New(
		newContext(),
		testhelper.MustParseURL(t, ts.URL),
		testdata.PublicKeys(),
		nil,
	)
	require.NoError(t, err)

	pingV := helper.MustRandString(10, nil)

	mutator := func(r *http.Request) {
		r.Header.Set("ping", pingV)
	}

	nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}
	resp, err := c.GetNar(context.Background(), nu, mutator)
	require.NoError(t, err)

	defer func() {
		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	assert.Equal(t, pingV, resp.Header.Get("pong"))
}

// basicAuth is a middleware function that checks for basic authentication credentials.
func basicAuth(expectedUser, expectedPass string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()

		if !ok || user != expectedUser || pass != expectedPass {
			w.Header().Set("WWW-Authenticate", `Basic realm="restricted", charset="UTF-8"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)

			return
		}

		next(w, r)
	}
}

func TestNetrcAuthentication(t *testing.T) {
	t.Parallel()

	protectedHandler := func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "StoreDir: /nix/store\nWantMassQuery: 1\nPriority: 30")
	}

	t.Run("WithCorrectCredentials", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewServer(basicAuth("testuser", "testpass", protectedHandler))
		defer ts.Close()

		creds := &upstream.NetrcCredentials{
			Username: "testuser",
			Password: "testpass",
		}

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL),
			nil,
			creds,
		)
		require.NoError(t, err)

		priority, err := c.ParsePriority(newContext())
		require.NoError(t, err)
		assert.Equal(t, uint64(30), priority)
	})

	t.Run("WithoutCredentials", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewServer(basicAuth("testuser", "testpass", protectedHandler))
		defer ts.Close()

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL),
			nil,
			nil,
		)
		require.NoError(t, err)

		_, err = c.ParsePriority(newContext())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected HTTP status code")
	})

	t.Run("WithIncorrectCredentials", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewServer(basicAuth("testuser", "testpass", protectedHandler))
		defer ts.Close()

		creds := &upstream.NetrcCredentials{
			Username: "testuser",
			Password: "wrongpass",
		}

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL),
			nil,
			creds,
		)
		require.NoError(t, err)

		_, err = c.ParsePriority(newContext())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected HTTP status code")
	})
}

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}
