package upstream_test

import (
	"errors"
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
			_, err := upstream.New(logger, "https://cache.example.com", nil)
			if want, got := upstream.ErrHostnameMustNotContainScheme, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("hostname must not contain a path", func(t *testing.T) {
			_, err := upstream.New(logger, "cache.example.com/path/to", nil)
			if want, got := upstream.ErrHostnameMustNotContainPath, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("valid hostName must return no error", func(t *testing.T) {
			_, err := upstream.New(logger, "cache.example.com", nil)
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}
		})
	})

	t.Run("public keys", func(t *testing.T) {
		t.Parallel()

		t.Run("invalid public keys", func(t *testing.T) {
			_, err := upstream.New(logger, "cache.example.com", []string{"invalid"})
			if !strings.HasPrefix(err.Error(), "error parsing the public key: public key is corrupt:") {
				t.Errorf("expected error to say public key is corrupt, got %q", err)
			}
		})

		t.Run("valid public keys", func(t *testing.T) {
			_, err := upstream.New(
				logger,
				"cache.example.com",
				[]string{"cache.example.com:qG7MkB/k0JsR/jlI5HNuaKQLd3AKILQIuwUEAwZ/6LQ="},
			)
			if err != nil {
				t.Errorf("expected no error, got %s", err)
			}
		})
	})
}
