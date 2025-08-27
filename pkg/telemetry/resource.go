package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/sdk/resource"

	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
)

// NewResource creates a new OpenTelemetry resource with standard attributes.
// This function consolidates the common resource creation logic used by both
// OpenTelemetry and Prometheus telemetry setups.
func NewResource(ctx context.Context, serviceName, serviceVersion string) (*resource.Resource, error) {
	return resource.New(
		ctx,

		// Set the Schema URL.
		// NOTE: This will fail if the semconv version being used within the
		// detectors is different. If an error occurs, change the import path of
		// semconv in the imports section at the top of this file.
		resource.WithSchemaURL(semconv.SchemaURL),

		// Add Custom attributes.
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersionKey.String(serviceVersion),
		),

		// Discover and provide command-line information.
		resource.WithProcessCommandArgs(),

		// Discover and provide runtime information.
		resource.WithProcessRuntimeVersion(),

		// Discover and provide attributes from OTEL_RESOURCE_ATTRIBUTES and
		// OTEL_SERVICE_NAME environment variables.
		resource.WithFromEnv(),

		// Discover and provide information about the OpenTelemetry SDK used.
		resource.WithTelemetrySDK(),

		// Discover and provide process information.
		resource.WithProcess(),

		// Discover and provide OS information.
		resource.WithOS(),

		// Discover and provide container information.
		resource.WithContainer(),

		// Discover and provide host information.
		resource.WithHost(),
	)
}
