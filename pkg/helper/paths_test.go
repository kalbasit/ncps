package helper_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalbasit/ncps/pkg/helper"
)

func TestNarInfoPath(t *testing.T) {
	t.Parallel()

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

			assert.Equal(t, test.path, helper.NarInfoPath(test.hash))
		})
	}
}

func TestNarPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		hash        string
		compression string
		path        string
	}{
		{hash: "", compression: "", path: "/nar/.nar"}, // not really valid but it is what it is
		{hash: "abc123", compression: "", path: "/nar/abc123.nar"},
		{hash: "def456", compression: "xz", path: "/nar/def456.nar.xz"},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("NarPath(%q, %q) -> %q", test.hash, test.compression, test.path), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.path, helper.NarPath(test.hash, test.compression))
		})
	}
}
