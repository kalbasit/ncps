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
