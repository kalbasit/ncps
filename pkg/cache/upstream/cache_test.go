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

	t.Run("hostname must be valid with no scheme or path", func(t *testing.T) {
		t.Parallel()

		t.Run("hostname must not be empty", func(t *testing.T) {
			_, err := upstream.New(logger, nil, nil)
			assert.ErrorIs(t, err, upstream.ErrURLRequired)
		})

		t.Run("hostname must not contain scheme", func(t *testing.T) {
			_, err := upstream.New(logger, testhelper.MustParseURL(t, "cache.nixos.org"), nil)
			assert.ErrorIs(t, err, upstream.ErrURLMustContainScheme)
		})

		t.Run("valid url with no path must not return no error", func(t *testing.T) {
			_, err := upstream.New(logger,
				testhelper.MustParseURL(t, "https://cache.nixos.org"), nil)

			assert.NoError(t, err)
		})

		t.Run("valid url with only / must not return no error", func(t *testing.T) {
			_, err := upstream.New(logger,
				testhelper.MustParseURL(t, "https://cache.nixos.org/"), nil)

			assert.NoError(t, err)
		})
	})

	t.Run("public keys", func(t *testing.T) {
		t.Parallel()

		t.Run("invalid public keys", func(t *testing.T) {
			_, err := upstream.New(logger, testhelper.MustParseURL(t, "https://cache.nixos.org"), []string{"invalid"})
			assert.True(t, strings.HasPrefix(err.Error(), "error parsing the public key: public key is corrupt:"))
		})

		t.Run("valid public keys", func(t *testing.T) {
			_, err := upstream.New(
				logger,
				testhelper.MustParseURL(t, "https://cache.nixos.org"),
				testdata.PublicKeys(),
			)
			assert.NoError(t, err)
		})
	})

	t.Run("priority parsed", func(t *testing.T) {
		t.Parallel()

		c, err := upstream.New(
			logger,
			testhelper.MustParseURL(t, "https://cache.nixos.org"),
			testdata.PublicKeys(),
		)
		require.NoError(t, err)

		assert.EqualValues(t, 40, c.GetPriority())
	})
}

func TestGetNarInfo(t *testing.T) {
	c, err := upstream.New(
		logger,
		testhelper.MustParseURL(t, "https://cache.nixos.org"),
		testdata.PublicKeys(),
	)
	require.NoError(t, err)

	t.Run("hash not found", func(t *testing.T) {
		t.Parallel()

		_, err := c.GetNarInfo(context.Background(), "abc123")
		assert.ErrorIs(t, err, upstream.ErrNotFound)
	})

	t.Run("hash is found", func(t *testing.T) {
		t.Parallel()

		ni, err := c.GetNarInfo(context.Background(), testdata.Nar1.NarInfoHash)
		require.NoError(t, err)

		assert.Equal(t, "/nix/store/n5glp21rsz314qssw9fbvfswgy3kc68f-hello-2.12.1", ni.StorePath)
	})

	t.Run("check has failed", func(t *testing.T) {
		t.Parallel()

		hash := "broken-" + testdata.Nar1.NarInfoHash

		ts := testdata.HTTPTestServer(t, 40)
		defer ts.Close()

		c, err := upstream.New(
			logger,
			testhelper.MustParseURL(t, ts.URL),
			testdata.PublicKeys(),
		)
		require.NoError(t, err)

		_, err = c.GetNarInfo(context.Background(), hash)
		assert.ErrorContains(t, err, "error while checking the narInfo: invalid Reference[0]: notfound-path")
	})

	for _, entry := range testdata.Entries {
		t.Run("check does not fail", func(t *testing.T) {
			t.Parallel()

			hash := entry.NarInfoHash

			ts := testdata.HTTPTestServer(t, 40)
			defer ts.Close()

			c, err := upstream.New(
				logger,
				testhelper.MustParseURL(t, ts.URL),
				testdata.PublicKeys(),
			)
			require.NoError(t, err)

			_, err = c.GetNarInfo(context.Background(), hash)
			assert.NoError(t, err)
		})
	}
}

func TestGetNar(t *testing.T) {
	c, err := upstream.New(
		logger,
		testhelper.MustParseURL(t, "https://cache.nixos.org"),
		testdata.PublicKeys(),
	)
	require.NoError(t, err)

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		_, _, err := c.GetNar(context.Background(), "abc123", "")
		assert.ErrorIs(t, err, upstream.ErrNotFound)
	})

	t.Run("hash is found", func(t *testing.T) {
		t.Parallel()

		hash := testdata.Nar1.NarHash

		cl, body, err := c.GetNar(context.Background(), hash, "xz")
		require.NoError(t, err)

		defer func() {
			//nolint:errcheck
			io.Copy(io.Discard, body)
			body.Close()
		}()

		assert.EqualValues(t, 50160, cl)
	})
}
