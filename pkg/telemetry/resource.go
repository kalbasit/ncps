package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"

	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
)

// NewResource creates a new OpenTelemetry resource with standard attributes.
// This function consolidates the common resource creation logic used by both
// OpenTelemetry and Prometheus telemetry setups.
func NewResource(
	ctx context.Context,
	serviceName,
	serviceVersion string,
	extraAttrs ...attribute.KeyValue,
) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(serviceName),
		semconv.ServiceVersionKey.String(serviceVersion),
	}
	attrs = append(attrs, extraAttrs...)

	return resource.New(
		ctx,

		// Set the Schema URL.
		// NOTE: This will fail if the semconv version being used within the
		// detectors is different. If an error occurs, change the import path of
		// semconv in the imports section at the top of this file.
		resource.WithSchemaURL(semconv.SchemaURL),

		// Add Custom attributes.
		resource.WithAttributes(attrs...),

		// Discover and provide attributes from OTEL_RESOURCE_ATTRIBUTES and
		// OTEL_SERVICE_NAME environment variables.
		resource.WithFromEnv(),

		// Discover and provide information about the OpenTelemetry SDK used.
		resource.WithTelemetrySDK(),

		// Discover and provide process information.
		// Do not use resource.WithProcess(). It includes command-line arguments via
		// resource.WithProcessCommandArgs(), which can leak sensitive information like
		// credentials passed as flags. Instead, we explicitly include only safe attributes.
		resource.WithProcessPID(),
		resource.WithProcessExecutableName(),
		resource.WithProcessExecutablePath(),
		resource.WithProcessOwner(),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
		resource.WithProcessRuntimeDescription(),

		// Discover and provide OS information.
		resource.WithOS(),

		// Discover and provide container information.
		resource.WithContainer(),

		// Discover and provide host information.
		resource.WithHost(),
	)
}
