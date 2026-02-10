package narinfo_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/narinfo"
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
		t.Run(fmt.Sprintf("NarInfoFilePath(%q) should return error", test), func(t *testing.T) {
			t.Parallel()

			_, err := narinfo.FilePath(test)
			assert.ErrorContains(t, err, "is less than 3 characters long")
		})
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("NarInfoFilePath(%q) -> %q", test.hash, test.path), func(t *testing.T) {
			t.Parallel()

			path, err := narinfo.FilePath(test.hash)
			require.NoError(t, err)
			assert.Equal(t, test.path, path)
		})
	}

	t.Run("NarInfoFilePath with invalid hash", func(t *testing.T) {
		t.Parallel()

		_, err := narinfo.FilePath("abc!@#")
		assert.ErrorIs(t, err, narinfo.ErrInvalidHash)
	})
}
