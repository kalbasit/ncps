package helper_test

import (
	"fmt"
	"testing"

	"github.com/kalbasit/ncps/pkg/helper"
)

func TestNarInfoPath(t *testing.T) {
	tests := []struct {
		hash string
		path string
	}{
		{hash: "", path: "/.narinfo"}, // not really valid but it is what it is
		{hash: "abc123", path: "/abc123.narinfo"},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("NarInfoPath(%q) -> %q", test.hash, test.path), func(t *testing.T) {
			t.Parallel()

			if want, got := test.path, helper.NarInfoPath(test.hash); want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})
	}
}
