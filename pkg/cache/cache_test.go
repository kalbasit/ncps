package cache

import (
	"os"
	"strings"
	"testing"

	"github.com/inconshreveable/log15/v3"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
)

var logger = log15.New()

func init() {
	logger.SetHandler(log15.DiscardHandler())
}

func TestNew(t *testing.T) {
	t.Run("path must be absolute, must exist, and must be a writable directory", func(t *testing.T) {
		t.Run("path is required", func(t *testing.T) {
			_, err := New(logger, "cache.example.com", "hello")
			if want, got := ErrPathMustBeAbsolute, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path is not absolute", func(t *testing.T) {
			_, err := New(logger, "cache.example.com", "hello")
			if want, got := ErrPathMustBeAbsolute, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path must exist", func(t *testing.T) {
			_, err := New(logger, "cache.example.com", "/non-existing")
			if want, got := ErrPathMustExist, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path must be a directory", func(t *testing.T) {
			_, err := New(logger, "cache.example.com", "/proc/cpuinfo")
			if want, got := ErrPathMustBeADirectory, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path must be writable", func(t *testing.T) {
			_, err := New(logger, "cache.example.com", "/root")
			if want, got := ErrPathMustBeWritable, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("valid path must return no error", func(t *testing.T) {
			_, err := New(logger, "cache.example.com", os.TempDir())
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}
		})
	})

	t.Run("hostname must be valid with no scheme or path", func(t *testing.T) {
		t.Run("hostname must not be empty", func(t *testing.T) {
			_, err := New(logger, "", os.TempDir())
			if want, got := ErrHostnameRequired, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("hostname must not contain scheme", func(t *testing.T) {
			_, err := New(logger, "https://cache.example.com", os.TempDir())
			if want, got := ErrHostnameMustNotContainScheme, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("hostname must not contain a path", func(t *testing.T) {
			_, err := New(logger, "cache.example.com/path/to", os.TempDir())
			if want, got := ErrHostnameMustNotContainPath, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("valid hostName must return no error", func(t *testing.T) {
			_, err := New(logger, "cache.example.com", os.TempDir())
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}
		})
	})
}

func TestPublicKey(t *testing.T) {
	c, err := New(logger, "cache.example.com", "/tmp")
	if err != nil {
		t.Fatalf("error not expected, got an error: %s", err)
	}

	pubKey := c.PublicKey()

	t.Run("should return a public key with the correct prefix", func(t *testing.T) {
		if !strings.HasPrefix(pubKey, "cache.example.com:") {
			t.Errorf("public key should start with cache.example.com: but it does not: %s", pubKey)
		}
	})

	t.Run("should return a valid public key", func(t *testing.T) {
		pk, err := signature.ParsePublicKey(pubKey)
		if err != nil {
			t.Fatalf("error is not expected: %s", err)
		}

		if want, got := pubKey, pk.String(); want != got {
			t.Errorf("want %q got %q", want, got)
		}
	})
}
