package telemetry_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"

	"github.com/kalbasit/ncps/pkg/analytics"
	"github.com/kalbasit/ncps/pkg/telemetry"
)

func TestNewResource(t *testing.T) {
	t.Parallel()

	// This test ensures that the semconv version used in this package (which should match serve.go)
	// is compatible with the resource creation logic in pkg/telemetry and pkg/analytics.
	// If pkg/telemetry or pkg/analytics imports a different incompatible version of semconv
	// for their attribute keys, this test should ideally fail if OTel SDK enforces schema checks.

	t.Run("telemetry: ensure semconv points to the same version", func(t *testing.T) {
		t.Parallel()

		_, err := telemetry.NewResource(context.Background(), "ncps", "0.0.1", semconv.SchemaURL)
		require.NoError(t, err)
	})

	t.Run("analytics: ensure semconv points to the same version", func(t *testing.T) {
		t.Parallel()

		_, err := analytics.NewResource(context.Background(), "ncps", "0.0.1", semconv.SchemaURL)
		require.NoError(t, err)
	})
}
