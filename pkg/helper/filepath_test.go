package helper_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/helper"
)

func TestNarInfoFilePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		hash string
		path string
	}{
		{hash: "abc123", path: filepath.Join("a", "ab", "abc123.narinfo")},
	}

	for _, test := range []string{"", "a", "ab"} {
		t.Run(fmt.Sprintf("NarInfoFilePath(%q) should panic", test), func(t *testing.T) {
			t.Parallel()

			assert.Panics(t, func() { _, _ = helper.NarInfoFilePath(test) })
		})
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("NarInfoFilePath(%q) -> %q", test.hash, test.path), func(t *testing.T) {
			t.Parallel()

			path, err := helper.NarInfoFilePath(test.hash)
			require.NoError(t, err)
			assert.Equal(t, test.path, path)
		})
	}

	t.Run("NarInfoFilePath with invalid hash", func(t *testing.T) {
		t.Parallel()

		_, err := helper.NarInfoFilePath("abc!@#")
		assert.ErrorIs(t, err, helper.ErrInvalidHash)
	})
}

func TestNarFilePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		hash        string
		compression string
		path        string
	}{
		{hash: "abc123", compression: "", path: filepath.Join("a", "ab", "abc123.nar")},
		{hash: "def456", compression: "xz", path: filepath.Join("d", "de", "def456.nar.xz")},
	}

	for _, test := range []string{"", "a", "ab"} {
		t.Run(fmt.Sprintf("NarFilePath(%q) should panic", test), func(t *testing.T) {
			t.Parallel()

			assert.Panics(t, func() { _, _ = helper.NarFilePath(test, "") })
		})
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("NarFilePath(%q, %q) -> %q", test.hash, test.compression, test.path), func(t *testing.T) {
			t.Parallel()

			path, err := helper.NarFilePath(test.hash, test.compression)
			require.NoError(t, err)
			assert.Equal(t, test.path, path)
		})
	}

	t.Run("NarFilePath with invalid hash", func(t *testing.T) {
		t.Parallel()

		_, err := helper.NarFilePath("abc!@#", "")
		assert.ErrorIs(t, err, helper.ErrInvalidHash)
	})
}

func TestFilePathWithSharding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fn   string
		path string
	}{
		{fn: "abc123.narinfo", path: filepath.Join("a", "ab", "abc123.narinfo")},
	}

	for _, test := range []string{"", "a", "ab"} {
		t.Run(fmt.Sprintf("FilePathWithSharding(%q) should panic", test), func(t *testing.T) {
			t.Parallel()

			assert.Panics(t, func() { _, _ = helper.FilePathWithSharding(test) })
		})
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("FilePathWithSharding(%q) -> %q", test.fn, test.path), func(t *testing.T) {
			t.Parallel()

			path, err := helper.FilePathWithSharding(test.fn)
			require.NoError(t, err)
			assert.Equal(t, test.path, path)
		})
	}
}
