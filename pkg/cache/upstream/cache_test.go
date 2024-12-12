package upstream_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/inconshreveable/log15/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

func TestNew(t *testing.T) {
	t.Parallel()

	ts := testdata.HTTPTestServer(t, 40)
	defer ts.Close()

	//nolint:paralleltest
	t.Run("hostname must be valid with no scheme or path", func(t *testing.T) {
		//nolint:paralleltest
		t.Run("hostname must not be empty", func(t *testing.T) {
			_, err := upstream.New(logger, nil, nil)
			assert.ErrorIs(t, err, upstream.ErrURLRequired)
		})

		//nolint:paralleltest
		t.Run("hostname must not contain scheme", func(t *testing.T) {
			_, err := upstream.New(logger, testhelper.MustParseURL(t, "cache.nixos.org"), nil)
			assert.ErrorIs(t, err, upstream.ErrURLMustContainScheme)
		})

		t.Run("valid url with no path must not return no error", func(t *testing.T) {
			_, err := upstream.New(logger,
				testhelper.MustParseURL(t, ts.URL), nil)

			assert.NoError(t, err)
		})

		t.Run("valid url with only / must not return no error", func(t *testing.T) {
			_, err := upstream.New(logger,
				testhelper.MustParseURL(t, ts.URL), nil)

			assert.NoError(t, err)
		})
	})

	//nolint:paralleltest
	t.Run("public keys", func(t *testing.T) {
		//nolint:paralleltest
		t.Run("invalid public keys", func(t *testing.T) {
			_, err := upstream.New(logger, testhelper.MustParseURL(t, ts.URL), []string{"invalid"})
			assert.True(t, strings.HasPrefix(err.Error(), "error parsing the public key: public key is corrupt:"))
		})

		//nolint:paralleltest
		t.Run("valid public keys", func(t *testing.T) {
			_, err := upstream.New(
				logger,
				testhelper.MustParseURL(t, ts.URL),
				testdata.PublicKeys(),
			)
			assert.NoError(t, err)
		})
	})

	//nolint:paralleltest
	t.Run("priority parsed", func(t *testing.T) {
		c, err := upstream.New(
			logger,
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

			ts := testdata.HTTPTestServer(t, 40)
			defer ts.Close()

			if withKeys {
				c, err = upstream.New(
					logger,
					testhelper.MustParseURL(t, ts.URL),
					testdata.PublicKeys(),
				)
			} else {
				c, err = upstream.New(
					logger,
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
				hash := "broken-" + testdata.Nar1.NarInfoHash

				_, err = c.GetNarInfo(context.Background(), hash)
				assert.ErrorContains(t, err, "error while checking the narInfo: invalid Reference[0]: notfound-path")
			})

			for _, entry := range testdata.Entries {
				t.Run("check does not fail", func(t *testing.T) {
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

func TestGetNar(t *testing.T) {
	t.Parallel()

	ts := testdata.HTTPTestServer(t, 40)
	defer ts.Close()

	c, err := upstream.New(
		logger,
		testhelper.MustParseURL(t, ts.URL),
		testdata.PublicKeys(),
	)
	require.NoError(t, err)

	//nolint:paralleltest
	t.Run("not found", func(t *testing.T) {
		_, _, err := c.GetNar(context.Background(), "abc123", "xz")
		assert.ErrorIs(t, err, upstream.ErrNotFound)
	})

	//nolint:paralleltest
	t.Run("hash is found", func(t *testing.T) {
		cl, body, err := c.GetNar(context.Background(), testdata.Nar1.NarHash, "xz")
		require.NoError(t, err)

		defer func() {
			//nolint:errcheck
			io.Copy(io.Discard, body)
			body.Close()
		}()

		assert.EqualValues(t, 50160, cl)
	})
}
