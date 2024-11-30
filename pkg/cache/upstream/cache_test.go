package upstream

import (
	"strings"
	"testing"

	"github.com/inconshreveable/log15/v3"
)

var logger = log15.New()

func init() {
	logger.SetHandler(log15.DiscardHandler())
}

func TestNew(t *testing.T) {
	t.Run("hostname must be valid with no scheme or path", func(t *testing.T) {
		t.Run("hostname must not be empty", func(t *testing.T) {
			_, err := New(logger, "", nil)
			if want, got := ErrHostnameRequired, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("hostname must not contain scheme", func(t *testing.T) {
			_, err := New(logger, "https://cache.example.com", nil)
			if want, got := ErrHostnameMustNotContainScheme, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("hostname must not contain a path", func(t *testing.T) {
			_, err := New(logger, "cache.example.com/path/to", nil)
			if want, got := ErrHostnameMustNotContainPath, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("valid hostName must return no error", func(t *testing.T) {
			_, err := New(logger, "cache.example.com", nil)
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}
		})
	})

	t.Run("public keys", func(t *testing.T) {
		t.Run("invalid public keys", func(t *testing.T) {
			_, err := New(logger, "cache.example.com", []string{"invalid"})
			if !strings.HasPrefix(err.Error(), "error parsing the public key: public key is corrupt:") {
				t.Errorf("expected error to say public key is corrupt, got %q", err)
			}
		})

		t.Run("valid public keys", func(t *testing.T) {
			_, err := New(logger, "cache.example.com", []string{"cache.example.com:qG7MkB/k0JsR/jlI5HNuaKQLd3AKILQIuwUEAwZ/6LQ="})
			if err != nil {
				t.Errorf("expected no error, got %s", err)
			}
		})
	})

	// t.Run("priority is extracted and stored", func(t *testing.T) {
	// 	c, err := New(logger, "cache.nixos.org", []string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="})
	// 	if err != nil {
	// 		t.Fatalf("expected no error, got %s", err)
	// 	}
	//
	// 	if want, got := 30, c.GetPriority(); want != got {
	// 		t.Errorf("want %d got %d", want, got)
	// 	}
	// })
}
