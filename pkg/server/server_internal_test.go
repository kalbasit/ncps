//nolint:testpackage
package server

import (
	"os"
	"testing"

	"github.com/inconshreveable/log15/v3"
	"github.com/kalbasit/ncps/pkg/cache"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

func TestSetDeletePermitted(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "cache-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	c, err := cache.New(logger, "cache.example.com", dir, nil)
	if err != nil {
		t.Fatalf("expected no error, got %q", err)
	}

	t.Run("false", func(t *testing.T) {
		s := New(logger, c)
		s.SetDeletePermitted(false)

		if want, got := false, s.deletePermitted; want != got {
			t.Errorf("want %t got %t", want, got)
		}
	})

	t.Run("true", func(t *testing.T) {
		s := New(logger, c)
		s.SetDeletePermitted(true)

		if want, got := true, s.deletePermitted; want != got {
			t.Errorf("want %t got %t", want, got)
		}
	})
}
