package nar_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
)

func TestNarFilePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		hash        string
		compression string
		path        string
	}{
		{
			hash:        "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
			compression: "",
			path:        filepath.Join("1", "1m", "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar"),
		},
		{
			hash:        "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
			compression: "xz",
			path:        filepath.Join("1", "1m", "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.xz"),
		},
	}

	for _, test := range []string{"", "a", "ab"} {
		t.Run(fmt.Sprintf("NarFilePath(%q) should return error", test), func(t *testing.T) {
			t.Parallel()

			_, err := nar.FilePath(test, "")
			assert.ErrorContains(t, err, "is less than 3 characters long")
		})
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("NarFilePath(%q, %q) -> %q", test.hash, test.compression, test.path), func(t *testing.T) {
			t.Parallel()

			path, err := nar.FilePath(test.hash, test.compression)
			require.NoError(t, err)
			assert.Equal(t, test.path, path)
		})
	}

	t.Run("NarFilePath with invalid hash", func(t *testing.T) {
		t.Parallel()

		_, err := nar.FilePath("abc!@#", "")
		assert.ErrorIs(t, err, nar.ErrInvalidHash)
	})
}
