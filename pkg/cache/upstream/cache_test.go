package upstream_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

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
			_, err := upstream.New(newContext(), nil, nil)
			assert.ErrorIs(t, err, upstream.ErrURLRequired)
		})

		//nolint:paralleltest
		t.Run("hostname must not contain scheme", func(t *testing.T) {
			_, err := upstream.New(newContext(), testhelper.MustParseURL(t, "cache.nixos.org"), nil)
			assert.ErrorIs(t, err, upstream.ErrURLMustContainScheme)
		})

		t.Run("valid url with no path must not return no error", func(t *testing.T) {
			_, err := upstream.New(newContext(),
				testhelper.MustParseURL(t, ts.URL), nil)

			assert.NoError(t, err)
		})

		t.Run("valid url with only / must not return no error", func(t *testing.T) {
			_, err := upstream.New(newContext(),
				testhelper.MustParseURL(t, ts.URL), nil)

			assert.NoError(t, err)
		})
	})

	//nolint:paralleltest
	t.Run("public keys", func(t *testing.T) {
		//nolint:paralleltest
		t.Run("invalid public keys", func(t *testing.T) {
			_, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), []string{"invalid"})
			assert.True(t, strings.HasPrefix(err.Error(), "error parsing the public key: public key is corrupt:"))
		})

		//nolint:paralleltest
		t.Run("valid public keys", func(t *testing.T) {
			_, err := upstream.New(
				newContext(),
				testhelper.MustParseURL(t, ts.URL),
				testdata.PublicKeys(),
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
		)
		require.NoError(t, err)

		assert.EqualValues(t, 40, c.GetPriority())
	})
}

func TestGetNarInfo(t *testing.T) {
	t.Parallel()

	testFn := func(withKeys bool) func(*testing.T) {
		return func(t *testing.T) {
			t.Parallel()

			var (
				c   upstream.Cache
				err error
			)

			ts := testdata.NewTestServer(t, 40)
			defer ts.Close()

			if withKeys {
				c, err = upstream.New(
					newContext(),
					testhelper.MustParseURL(t, ts.URL),
					testdata.PublicKeys(),
				)
			} else {
				c, err = upstream.New(
					newContext(),
					testhelper.MustParseURL(t, ts.URL),
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
							b = strings.Replace(b, "References:", "References: notfound-path", -1)

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
			)
			require.NoError(t, err)

			exists, err := c.HasNarInfo(context.Background(), narEntry.NarInfoHash)
			require.NoError(t, err)

			assert.True(t, exists)
		})
	}
}

func TestGetNar(t *testing.T) {
	t.Parallel()

	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	c, err := upstream.New(
		newContext(),
		testhelper.MustParseURL(t, ts.URL),
		testdata.PublicKeys(),
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
			)
			require.NoError(t, err)

			nu := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}
			exists, err := c.HasNar(context.Background(), nu)
			require.NoError(t, err)

			assert.True(t, exists)
		})
	}
}

func TestGetNarCanMutate(t *testing.T) {
	t.Parallel()

	ts := testdata.NewTestServer(t, 40)
	defer ts.Close()

	c, err := upstream.New(
		newContext(),
		testhelper.MustParseURL(t, ts.URL),
		testdata.PublicKeys(),
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

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}
