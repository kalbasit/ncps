package helper_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalbasit/ncps/pkg/helper"
)

func TestNarInfoURLPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		hash string
		path string
	}{
		{hash: "", path: "/.narinfo"}, // not really valid but it is what it is
		{hash: "abc123", path: "/abc123.narinfo"},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("NarInfoURLPath(%q) -> %q", test.hash, test.path), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.path, helper.NarInfoURLPath(test.hash))
		})
	}
}
