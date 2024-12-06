package upstream_test

import (
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/inconshreveable/log15/v3"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/testdata"
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
			_, err := upstream.New(logger, "", nil)
			if want, got := upstream.ErrHostnameRequired, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("hostname must not contain scheme", func(t *testing.T) {
			_, err := upstream.New(logger, "https://cache.nixos.org", nil)
			if want, got := upstream.ErrHostnameMustNotContainScheme, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("hostname must not contain a path", func(t *testing.T) {
			_, err := upstream.New(logger, "cache.nixos.org/path/to", nil)
			if want, got := upstream.ErrHostnameMustNotContainPath, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("valid hostName must return no error", func(t *testing.T) {
			_, err := upstream.New(logger, "cache.nixos.org", nil)
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}
		})
	})

	t.Run("public keys", func(t *testing.T) {
		t.Parallel()

		t.Run("invalid public keys", func(t *testing.T) {
			_, err := upstream.New(logger, "cache.nixos.org", []string{"invalid"})
			if !strings.HasPrefix(err.Error(), "error parsing the public key: public key is corrupt:") {
				t.Errorf("expected error to say public key is corrupt, got %q", err)
			}
		})

		t.Run("valid public keys", func(t *testing.T) {
			_, err := upstream.New(
				logger,
				"cache.nixos.org",
				[]string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="},
			)
			if err != nil {
				t.Errorf("expected no error, got %s", err)
			}
		})
	})

	t.Run("priority parsed", func(t *testing.T) {
		t.Parallel()

		c, err := upstream.New(
			logger,
			"cache.nixos.org",
			[]string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="},
		)
		if err != nil {
			t.Errorf("expected no error, got %s", err)
		}

		if want, got := uint64(40), c.GetPriority(); want != got {
			t.Errorf("want %d got %d", want, got)
		}
	})
}

func TestGetNarInfo(t *testing.T) {
	c, err := upstream.New(
		logger,
		"cache.nixos.org",
		[]string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="},
	)
	if err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	t.Run("hash not found", func(t *testing.T) {
		t.Parallel()

		_, err := c.GetNarInfo(context.Background(), "abc123")
		if want, got := upstream.ErrNotFound, err; !errors.Is(got, want) {
			t.Errorf("want %q got %q", want, got)
		}
	})

	t.Run("hash is found", func(t *testing.T) {
		t.Parallel()

		ni, err := c.GetNarInfo(context.Background(), testdata.Nar1.NarInfoHash)
		if err != nil {
			t.Fatalf("expected no error, got %s", err)
		}

		if want, got := "/nix/store/7bn85d74qa0127p85rrswfyghxsqmcf7-iputils-20210722", ni.StorePath; want != got {
			t.Errorf("want %q got %q", want, got)
		}
	})

	t.Run("check has failed", func(t *testing.T) {
		t.Parallel()

		hash := "broken-" + testdata.Nar1.NarInfoHash

		ts := testdata.HTTPTestServer(t, 40)
		defer ts.Close()

		tu, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("error not expected: %s", err)
		}

		c, err := upstream.New(
			logger,
			tu.Host,
			[]string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="},
		)
		if err != nil {
			t.Fatalf("expected no error, got %s", err)
		}

		_, err = c.GetNarInfo(context.Background(), hash)
		if err == nil {
			t.Fatal("error expected but got none")
		}

		if want, got := "error while checking the narInfo: invalid Reference[0]: notfound-path", err.Error(); want != got {
			t.Errorf("want %q got %q", want, got)
		}
	})

	for _, entry := range testdata.Entries {
		t.Run("check does not fail", func(t *testing.T) {
			t.Parallel()

			hash := entry.NarInfoHash

			ts := testdata.HTTPTestServer(t, 40)
			defer ts.Close()

			tu, err := url.Parse(ts.URL)
			if err != nil {
				t.Fatalf("error not expected: %s", err)
			}

			c, err := upstream.New(
				logger,
				tu.Host,
				[]string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="},
			)
			if err != nil {
				t.Fatalf("expected no error, got %s", err)
			}

			_, err = c.GetNarInfo(context.Background(), hash)
			if err != nil {
				t.Fatalf("error not expected getting narinfo %q got: %s", hash, err)
			}
		})
	}
}

func TestGetNar(t *testing.T) {
	c, err := upstream.New(
		logger,
		"cache.nixos.org",
		[]string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="},
	)
	if err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		_, _, err := c.GetNar(context.Background(), "abc123", "")
		if want, got := upstream.ErrNotFound, err; !errors.Is(got, want) {
			t.Errorf("want %q got %q", want, got)
		}
	})

	t.Run("hash is found", func(t *testing.T) {
		t.Parallel()

		hash := testdata.Nar1.NarHash

		cl, body, err := c.GetNar(context.Background(), hash, "xz")
		if err != nil {
			t.Fatalf("expected no error, got %s", err)
		}

		defer func() {
			//nolint:errcheck
			io.Copy(io.Discard, body)
			body.Close()
		}()

		if want, got := int64(132228), cl; want != got {
			t.Errorf("want %d got %d", want, got)
		}
	})
}
