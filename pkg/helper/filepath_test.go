package helper_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/helper"
)

func TestFilePathWithSharding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fn   string
		path string
	}{
		{fn: "abc123.narinfo", path: filepath.Join("a", "ab", "abc123.narinfo")},
	}

	for _, test := range []string{"", "a", "ab"} {
		t.Run(fmt.Sprintf("FilePathWithSharding(%q) should return error", test), func(t *testing.T) {
			t.Parallel()

			_, err := helper.FilePathWithSharding(test)
			assert.ErrorContains(t, err, "is less than 3 characters long")
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
