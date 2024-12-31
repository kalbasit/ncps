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

	for _, nar := range testdata.Entries {
		ni, err := narinfo.Parse(strings.NewReader(nar.NarInfoText))
		require.NoError(t, err)
		require.NoError(t, ni.Check())
		assert.EqualValues(t, ni.FileSize, len(nar.NarText)) //nolint:testifylint
	}
}
