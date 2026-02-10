package narinfo_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/narinfo"
)

func TestFilePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		hash string
		path string
	}{
		{
			hash: "n5glp21rsz314qssw9fbvfswgy3kc68f",
			path: filepath.Join("n", "n5", "n5glp21rsz314qssw9fbvfswgy3kc68f.narinfo"),
		},
	}

	for _, test := range []string{"", "a", "ab"} {
		t.Run(fmt.Sprintf("FilePath(%q) should return error", test), func(t *testing.T) {
			t.Parallel()

			_, err := narinfo.FilePath(test)
			assert.ErrorContains(t, err, "is less than 3 characters long")
		})
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("FilePath(%q) -> %q", test.hash, test.path), func(t *testing.T) {
			t.Parallel()

			path, err := narinfo.FilePath(test.hash)
			require.NoError(t, err)
			assert.Equal(t, test.path, path)
		})
	}

	t.Run("FilePath with invalid hash", func(t *testing.T) {
		t.Parallel()

		_, err := narinfo.FilePath("abc!@#")
		assert.ErrorIs(t, err, narinfo.ErrInvalidHash)
	})
}
