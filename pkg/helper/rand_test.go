package helper_test

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/helper"
)

func TestRandString(t *testing.T) {
	t.Run("validate length", func(t *testing.T) {
		t.Parallel()

		s, err := helper.RandString(5, nil)
		require.NoError(t, err)

		assert.Len(t, s, 5)
	})

	t.Run("validate value based on deterministic source", func(t *testing.T) {
		t.Parallel()

		src := rand.NewSource(123)

		//nolint:gosec
		s, err := helper.RandString(5, rand.New(src))
		require.NoError(t, err)

		assert.Equal(t, "a2lzq", s)
	})
}
