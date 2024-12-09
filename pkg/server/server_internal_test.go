package server

import (
	"os"
	"testing"

	"github.com/inconshreveable/log15/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

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
