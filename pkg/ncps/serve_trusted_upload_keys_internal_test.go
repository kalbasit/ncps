package ncps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTrustedUploadKeys(t *testing.T) {
	t.Parallel()

	t.Run("empty input or empty strings yield no keys and no error", func(t *testing.T) {
		t.Parallel()

		keys, err := parseTrustedUploadKeys(nil)
		require.NoError(t, err)
		assert.Empty(t, keys)

		// An empty env var can surface as a [""] slice; it must be ignored.
		keys, err = parseTrustedUploadKeys([]string{"", ""})
		require.NoError(t, err)
		assert.Empty(t, keys)
	})

	t.Run("valid nix-format keys parse", func(t *testing.T) {
		t.Parallel()

		raw := []string{
			"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=",
			"nasreddine-uploads-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs=",
		}

		keys, err := parseTrustedUploadKeys(raw)
		require.NoError(t, err)
		require.Len(t, keys, 2)
	})

	t.Run("malformed key returns an error", func(t *testing.T) {
		t.Parallel()

		_, err := parseTrustedUploadKeys([]string{"not-a-valid-key"})
		require.Error(t, err)
	})
}
