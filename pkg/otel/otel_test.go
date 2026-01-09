package otel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/sdk/resource"

	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

func TestSetupOTelSDK(t *testing.T) {
	ctx := context.Background()
	res, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceNameKey.String("test-service")))
	assert.NoError(t, err)

	t.Run("Disabled", func(t *testing.T) {
		shutdown, err := SetupOTelSDK(ctx, false, "", res)
		assert.NoError(t, err)
		assert.NotNil(t, shutdown)
		assert.NoError(t, shutdown(ctx))
	})

	t.Run("EnabledStdout", func(t *testing.T) {
		shutdown, err := SetupOTelSDK(ctx, true, "", res)
		assert.NoError(t, err)
		assert.NotNil(t, shutdown)
		assert.NoError(t, shutdown(ctx))
	})

	// We refrain from testing the gRPC path as it would require a running collector
}
