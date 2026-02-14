package testdata_test

import (
	"strings"
	"testing"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/testdata"
)

func TestNarsValid(t *testing.T) {
	t.Parallel()

	for _, entry := range testdata.Entries {
		ni, err := narinfo.Parse(strings.NewReader(entry.NarInfoText))
		require.NoError(t, err)
		require.NoError(t, ni.Check())

		// For Compression: none, FileSize may be 0 (omitted by nix-serve style upstreams).
		// In that case, the NarText length should match NarSize (uncompressed = NarSize).
		// For compressed NARs, FileSize is the compressed size and must match NarText length.
		sizeToCompare := ni.FileSize
		if sizeToCompare == 0 {
			sizeToCompare = ni.NarSize
		}

		assert.EqualValues(t, sizeToCompare, len(entry.NarText)) //nolint:testifylint
	}
}
