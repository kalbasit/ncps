package ncps_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"

	"github.com/kalbasit/ncps/pkg/analytics"
	"github.com/kalbasit/ncps/pkg/otel"
)

func TestNewResource(t *testing.T) {
	t.Parallel()

	// This test ensures that the semconv version used in this package (which should match serve.go)
	// is compatible with the resource creation logic in pkg/otel and pkg/analytics.
	// If pkg/otel or pkg/analytics imports a different incompatible version of semconv
	// for their attribute keys, this test should ideally fail if OTel SDK enforces schema checks.

	t.Run("otel: ensure semconv points to the same version", func(t *testing.T) {
		t.Parallel()

		_, err := otel.NewResource(context.Background(), "ncps", "0.0.1", semconv.SchemaURL)
		require.NoError(t, err)
	})

	t.Run("analytics: ensure semconv points to the same version", func(t *testing.T) {
		t.Parallel()

		_, err := analytics.NewResource(context.Background(), "ncps", "0.0.1", semconv.SchemaURL)
		require.NoError(t, err)
	})
}
