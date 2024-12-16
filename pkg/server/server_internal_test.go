package server

import (
	"io"
	"os"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache"
)

//nolint:gochecknoglobals
var logger = zerolog.New(io.Discard)

func TestSetDeletePermitted(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	c, err := cache.New(logger, "cache.example.com", dir)
	require.NoError(t, err)

	t.Run("false", func(t *testing.T) {
		t.Parallel()

		s := New(logger, c)
		s.SetDeletePermitted(false)

		assert.False(t, s.deletePermitted)
	})

	t.Run("true", func(t *testing.T) {
		t.Parallel()

		s := New(logger, c)
		s.SetDeletePermitted(true)

		assert.True(t, s.deletePermitted)
	})
}
