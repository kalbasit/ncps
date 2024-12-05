package helper_test

import (
	"math/rand"
	"testing"

	"github.com/kalbasit/ncps/pkg/helper"
)

func TestRandString(t *testing.T) {
	t.Run("validate length", func(t *testing.T) {
		t.Parallel()

		s, err := helper.RandString(5, nil)
		if err != nil {
			t.Errorf("expected no error got: %s", err)
		}

		if want, got := 5, len(s); want != got {
			t.Errorf("want %d got %d", want, got)
		}
	})

	t.Run("validate value based on deterministic source", func(t *testing.T) {
		t.Parallel()

		src := rand.NewSource(123)

		//nolint:gosec
		s, err := helper.RandString(5, rand.New(src))
		if err != nil {
			t.Errorf("expected no error got: %s", err)
		}

		if want, got := "a2lzq", s; want != got {
			t.Errorf("want %q got %q", want, got)
		}
	})
}
