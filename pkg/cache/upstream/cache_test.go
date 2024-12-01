package upstream_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/inconshreveable/log15/v3"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
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

		hash := "7bn85d74qa0127p85rrswfyghxsqmcf7"

		ni, err := c.GetNarInfo(context.Background(), hash)
		if err != nil {
			t.Fatalf("expected no error, got %s", err)
		}

		if want, got := "/nix/store/7bn85d74qa0127p85rrswfyghxsqmcf7-iputils-20210722", ni.StorePath; want != got {
			t.Errorf("want %q got %q", want, got)
		}
	})

	t.Run("check has failed", func(t *testing.T) {
		t.Parallel()

		hash := "7bn85d74qa0127p85rrswfyghxsqmcf7"

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/nix-cache-info" {
				_, err := w.Write([]byte(`StoreDir: /nix/store
WantMassQuery: 1
Priority: 40`))
				if err != nil {
					t.Fatalf("expected no error, got %s", err)
				}

				return
			}

			if r.URL.Path == "/"+hash+".narinfo" {
				//nolint:lll
				_, err := w.Write([]byte(`StorePath: /nix/store/7bn85d74qa0127p85rrswfyghxsqmcf7-iputils-20210722
URL: nar/136jk8xlxqzqd16d00dpnnpgffmycwm66zgky6397x75yg7ylz00.nar.xz
Compression: xz
FileHash: sha256:136jk8xlxqzqd16d00dpnnpgffmycwm66zgky6397x75yg7ylz00
FileSize: 132228
NarHash: sha256:1rzb80kz42wy067pp160rridw41dnc09d2a3cqj2wdg6ylklhxkh
NarSize: 534160
References: notfound-path 7bn85d74qa0127p85rrswfyghxsqmcf7-iputils-20210722 892cxk44qxzzlw9h90a781zpy1j7gmmn-libidn2-2.3.2 l25bc19is0s27929kxkfhgdzhc7x9g5m-libcap-2.49-lib rir9pf0kz1mb84x5bd3yr0fx415yy423-glibc-2.33-123
Deriver: 9fs4vq4gdsb8r9ywawb5f6zl40ycp1bh-iputils-20210722.drv
Sig: cache.nixos.org-1:WzhkqDdkgPz2qU/0QyEA6wUIm7EMR5MY8nTb5jAmmoh5b80ACIp/+Zpgi5t1KvmO8uG8GVrkPejCxbyQ2gNXDQ==`))
				if err != nil {
					t.Fatalf("expected no error, got %s", err)
				}

				return
			}

			w.WriteHeader(http.StatusNotFound)
		}))
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
		if want, got := "error while checking the narInfo: invalid Reference[0]: notfound-path", err.Error(); want != got {
			t.Errorf("want %q got %q", want, got)
		}
	})
}
