package cache_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/inconshreveable/log15/v3"
	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("path must be absolute, must exist, and must be a writable directory", func(t *testing.T) {
		t.Parallel()

		t.Run("path is required", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "hello", nil)
			if want, got := cache.ErrPathMustBeAbsolute, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path is not absolute", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "hello", nil)
			if want, got := cache.ErrPathMustBeAbsolute, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path must exist", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "/non-existing", nil)
			if want, got := cache.ErrPathMustExist, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path must be a directory", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "/proc/cpuinfo", nil)
			if want, got := cache.ErrPathMustBeADirectory, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path must be writable", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "/root", nil)
			if want, got := cache.ErrPathMustBeWritable, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("valid path must return no error", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", os.TempDir(), nil)
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}
		})
	})

	t.Run("hostname must be valid with no scheme or path", func(t *testing.T) {
		t.Parallel()

		t.Run("hostname must not be empty", func(t *testing.T) {
			_, err := cache.New(logger, "", os.TempDir(), nil)
			if want, got := cache.ErrHostnameRequired, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("hostname must not contain scheme", func(t *testing.T) {
			_, err := cache.New(logger, "https://cache.example.com", os.TempDir(), nil)
			if want, got := cache.ErrHostnameMustNotContainScheme, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("hostname must not contain a path", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com/path/to", os.TempDir(), nil)
			if want, got := cache.ErrHostnameMustNotContainPath, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("valid hostName must return no error", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", os.TempDir(), nil)
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}
		})
	})
}

func TestPublicKey(t *testing.T) {
	t.Parallel()

	c, err := cache.New(logger, "cache.example.com", "/tmp", nil)
	if err != nil {
		t.Fatalf("error not expected, got an error: %s", err)
	}

	pubKey := c.PublicKey()

	t.Run("should return a public key with the correct prefix", func(t *testing.T) {
		t.Parallel()

		if !strings.HasPrefix(pubKey, "cache.example.com:") {
			t.Errorf("public key should start with cache.example.com: but it does not: %s", pubKey)
		}
	})

	t.Run("should return a valid public key", func(t *testing.T) {
		t.Parallel()

		pk, err := signature.ParsePublicKey(pubKey)
		if err != nil {
			t.Fatalf("error is not expected: %s", err)
		}

		if want, got := pubKey, pk.String(); want != got {
			t.Errorf("want %q got %q", want, got)
		}
	})
}
