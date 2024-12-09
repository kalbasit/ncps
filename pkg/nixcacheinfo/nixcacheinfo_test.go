package nixcacheinfo_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nixcacheinfo"
)

func TestParse(t *testing.T) {
	t.Parallel()

	t.Run("parse correctly", func(t *testing.T) {
		const nixCacheInfoText = `StoreDir: /nix/store
WantMassQuery: 1
Priority: 40`

		nci, err := nixcacheinfo.ParseString(nixCacheInfoText)
		require.NoError(t, err)

		assert.Equal(t, "/nix/store", nci.StoreDir)
		assert.EqualValues(t, 1, nci.WantMassQuery)
		assert.EqualValues(t, 40, nci.Priority)
	})
}
