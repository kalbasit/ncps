package helper_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalbasit/ncps/pkg/helper"
)

func TestNarInfoFilePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		hash string
		path string
	}{
		{hash: "abc123", path: "abc123.narinfo"},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("NarInfoFilePath(%q) -> %q", test.hash, test.path), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.path, helper.NarInfoFilePath(test.hash))
		})
	}
}

func TestNarFilePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		hash        string
		compression string
		path        string
	}{
		{hash: "abc123", compression: "", path: "abc123.nar"},
		{hash: "def456", compression: "xz", path: "def456.nar.xz"},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("NarFilePath(%q, %q) -> %q", test.hash, test.compression, test.path), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.path, helper.NarFilePath(test.hash, test.compression))
		})
	}
}
