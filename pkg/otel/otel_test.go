package otel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/resource"

	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"

	"github.com/kalbasit/ncps/pkg/otel"
)

//nolint:paralleltest
func TestSetupOTelSDK(t *testing.T) {
	ctx := context.Background()
	res, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceNameKey.String("test-service")))
	require.NoError(t, err)

	t.Run("Disabled", func(t *testing.T) {
		shutdown, err := otel.SetupOTelSDK(ctx, false, "", res)
		require.NoError(t, err)
		assert.NotNil(t, shutdown)
		assert.NoError(t, shutdown(ctx))
	})

	t.Run("EnabledStdout", func(t *testing.T) {
		shutdown, err := otel.SetupOTelSDK(ctx, true, "", res)
		require.NoError(t, err)
		assert.NotNil(t, shutdown)
		assert.NoError(t, shutdown(ctx))
	})

	// We refrain from testing the gRPC path as it would require a running collector
}
