//nolint:testpackage
package cmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestNewResource(t *testing.T) {
	t.Parallel()

	t.Run("ensure semconv points to the same version", func(t *testing.T) {
		cmd := &cli.Command{}
		_, err := newResource(context.Background(), cmd)
		require.NoError(t, err)
	})
}
