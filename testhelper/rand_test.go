package testhelper_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cryptorand "crypto/rand"
	mathrand "math/rand"

	"github.com/kalbasit/ncps/testhelper"
)

func TestRandChars(t *testing.T) {
	t.Run("validate length", func(t *testing.T) {
		t.Parallel()

		s, err := testhelper.RandChars(5, testhelper.AllChars, cryptorand.Reader)
		require.NoError(t, err)

		assert.Len(t, s, 5)
	})

	t.Run("validate value based on deterministic source", func(t *testing.T) {
		t.Parallel()

		src := mathrand.NewSource(123)

		//nolint:gosec
		s, err := testhelper.RandChars(5, testhelper.AllChars, mathrand.New(src))
		require.NoError(t, err)

		assert.Equal(t, "a2lzq", s)
	})
}

func TestRandNarInfoHash(t *testing.T) {
	t.Run("validate length", func(t *testing.T) {
		t.Parallel()

		s, err := testhelper.RandNarInfoHash()
		require.NoError(t, err)

		assert.Len(t, s, 32)
	})

	t.Run("validate character set", func(t *testing.T) {
		t.Parallel()

		s, err := testhelper.RandNarInfoHash()
		require.NoError(t, err)

		for _, ch := range s {
			assert.Contains(t, testhelper.Nix32Chars, string(ch))
		}
	})

	t.Run("returns different values", func(t *testing.T) {
		t.Parallel()

		s1, err := testhelper.RandNarInfoHash()
		require.NoError(t, err)

		s2, err := testhelper.RandNarInfoHash()
		require.NoError(t, err)

		assert.NotEqual(t, s1, s2)
	})
}

func TestMustRandNarInfoHash(t *testing.T) {
	t.Run("returns valid hash", func(t *testing.T) {
		t.Parallel()

		s := testhelper.MustRandNarInfoHash()

		assert.Len(t, s, 32)

		for _, ch := range s {
			assert.Contains(t, testhelper.Nix32Chars, string(ch))
		}
	})

	t.Run("does not panic", func(t *testing.T) {
		t.Parallel()

		assert.NotPanics(t, func() {
			testhelper.MustRandNarInfoHash()
		})
	})
}

func TestRandNarHash(t *testing.T) {
	t.Run("validate length", func(t *testing.T) {
		t.Parallel()

		s, err := testhelper.RandNarHash()
		require.NoError(t, err)

		assert.Len(t, s, 52)
	})

	t.Run("validate character set", func(t *testing.T) {
		t.Parallel()

		s, err := testhelper.RandNarHash()
		require.NoError(t, err)

		for _, ch := range s {
			assert.Contains(t, testhelper.Nix32Chars, string(ch))
		}
	})

	t.Run("returns different values", func(t *testing.T) {
		t.Parallel()

		s1, err := testhelper.RandNarHash()
		require.NoError(t, err)

		s2, err := testhelper.RandNarHash()
		require.NoError(t, err)

		assert.NotEqual(t, s1, s2)
	})
}

func TestMustRandNarHash(t *testing.T) {
	t.Run("returns valid hash", func(t *testing.T) {
		t.Parallel()

		s := testhelper.MustRandNarHash()

		assert.Len(t, s, 52)

		for _, ch := range s {
			assert.Contains(t, testhelper.Nix32Chars, string(ch))
		}
	})

	t.Run("does not panic", func(t *testing.T) {
		t.Parallel()

		assert.NotPanics(t, func() {
			testhelper.MustRandNarHash()
		})
	})
}
