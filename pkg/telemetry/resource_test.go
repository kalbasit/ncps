package telemetry_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/telemetry"
)

func TestNewResource(t *testing.T) {
	t.Parallel()

	t.Run("ensure semconv points to the same version", func(t *testing.T) {
		_, err := telemetry.NewResource(context.Background(), "ncps", "0.0.1")
		require.NoError(t, err)
	})
}
