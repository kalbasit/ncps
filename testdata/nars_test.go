package testdata

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNarsValid(t *testing.T) {
	t.Parallel()

	for i, nar := range Entries {
		var ni *narinfo.NarInfo

		t.Run(fmt.Sprintf("can parse the narinfo for nar%d", i), func(t *testing.T) {
			var err error
			ni, err = narinfo.Parse(strings.NewReader(nar.NarInfoText))
			require.NoError(t, err)
		})

		t.Run(fmt.Sprintf("narinfo for nar%d is valid", i), func(t *testing.T) {
			require.NoError(t, ni.Check())
		})

		t.Run(fmt.Sprintf("the nar is of the same size as it should be for nar%d", i), func(t *testing.T) {
			assert.EqualValues(t, ni.FileSize, len(nar.NarText))
		})
	}
}
